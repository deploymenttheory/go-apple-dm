package app

import (
	"net/http"
	"sort"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/internal/buildinfo"
)

// ActionReadConfig is declared by the introspection routes. They are
// authenticated but not policy-gated (adminRoute.Introspection), so the action
// exists for the route table's sake rather than to be granted.
const ActionReadConfig = "readConfig"

// introspectionRoutes describes the unified server to an authenticated client.
//
//	GET /config   service, version, bootstrap state and enabled families
//	GET /routes   the mounted route table with the action each route requires
//
// The route response reads the same table used to build the mux, including
// resource types, required context and sensitive-operation metadata.
func (a *App) introspectionRoutes() []adminRoute {
	return []adminRoute{
		{
			Pattern:       "GET /config",
			Action:        ActionReadConfig,
			Family:        "introspection",
			Introspection: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{
					"Service":  "device-management",
					"Version":  buildVersion(),
					"Families": a.adminFamilies(),
					"Policy":   a.admin != nil,
					// Reports whether the one-time first-root exchange is available.
					"BootstrapPending": a.bootstrapPending(r),
					"EventDelivery":    a.eventStats(),
				})
			}),
		},
		{
			Pattern:       "GET /routes",
			Action:        ActionReadConfig,
			Family:        "introspection",
			Introspection: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				type view struct {
					Method, Pattern, Action, Family string
					Resource                        string
					Context                         map[string]adminauth.ContextAttribute
					Sensitive, RootOnly             bool
					CommandActions                  []string
				}
				out := make([]view, 0, len(a.adminTable))
				for _, rt := range a.adminTable {
					method, pattern, ok := strings.Cut(rt.Pattern, " ")
					if !ok {
						method, pattern = "", rt.Pattern
					}
					action, _ := a.admin.Registry().Lookup(rt.Action)
					v := view{Method: method, Pattern: pattern, Action: rt.Action, Family: rt.Family, Resource: string(action.Resource), Context: action.Context, Sensitive: action.Sensitive, RootOnly: action.RootOnly}
					if rt.Command {
						v.CommandActions = commandActionIDs()
					}
					out = append(out, v)
				}
				sort.Slice(out, func(i, j int) bool {
					if out[i].Pattern != out[j].Pattern {
						return out[i].Pattern < out[j].Pattern
					}
					return out[i].Method < out[j].Method
				})
				writeJSON(w, http.StatusOK, map[string]any{"Routes": out})
			}),
		},
	}
}

// adminFamilies lists the route families this process mounted, sorted.
func (a *App) adminFamilies() []string {
	seen := make(map[string]bool, len(a.adminTable))
	var out []string
	for _, rt := range a.adminTable {
		if !seen[rt.Family] {
			seen[rt.Family] = true
			out = append(out, rt.Family)
		}
	}
	sort.Strings(out)
	return out
}

// buildVersion reports the packaged release or go install module version.
func buildVersion() string {
	return buildinfo.Version()
}

// bootstrapPending reports whether a configured bootstrap secret can still create the
// first root; store failures return false.
func (a *App) bootstrapPending(r *http.Request) bool {
	initialized, err := a.admin.Initialized(r.Context())
	return err == nil && !initialized && a.cfg.BootstrapToken != ""
}
