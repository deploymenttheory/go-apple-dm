package app

import (
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
)

// ActionReadConfig is declared by the introspection routes. They are
// authenticated but not policy-gated (adminRoute.Introspection), so the action
// exists for the route table's sake rather than to be granted.
const ActionReadConfig = "readConfig"

// introspectionRoutes describe the server to a client.
//
//	GET /config   role, version, and the families this process serves
//	GET /routes   the mounted route table with the action each route requires
//
// GET /routes is generated from the same slice the mux is built from, so it
// cannot drift from what is served. That is the whole reason it exists rather
// than a hand-written API document: no reference server publishes its route
// table at all, and a document that is written separately is wrong the moment
// a route moves.
func (a *App) introspectionRoutes() []adminRoute {
	return []adminRoute{
		{
			Pattern: "GET /config", Action: ActionReadConfig, Family: "introspection", Introspection: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{
					"Role":     string(a.cfg.Role),
					"Version":  buildVersion(),
					"Families": a.adminFamilies(),
					"Policy":   a.admin != nil,
					// Reported so an operator can see the standing root
					// credential without reading logs, and so mdmctl can say
					// it out loud after bootstrap.
					"BreakGlass": a.cfg.AdminToken != "",
				})
			}),
		},
		{
			Pattern: "GET /routes", Action: ActionReadConfig, Family: "introspection", Introspection: true,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				type view struct{ Method, Pattern, Action, Family string }
				out := make([]view, 0, len(a.adminTable))
				for _, rt := range a.adminTable {
					method, pattern, ok := strings.Cut(rt.Pattern, " ")
					if !ok {
						method, pattern = "", rt.Pattern
					}
					out = append(out, view{method, pattern, rt.Action, rt.Family})
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

// buildVersion reports the module version the binary was built from, or
// "devel" outside a build. It is read once from the build info rather than
// stamped, so nothing needs to be passed at link time for it to be truthful.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "devel"
	}
	return info.Main.Version
}
