package app

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cedar-policy/cedar-go/types"

	"github.com/deploymenttheory/go-apple-mdm/adminauth"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
)

// Admin action ids. Every admin route declares one, and the set below is the
// registry a stored policy is validated against, so a policy naming an action
// that no route serves is refused when it is written rather than silently
// never granting (decision record 0034).
const (
	ActionPutDeclaration       = "putDeclaration"
	ActionGetDeclaration       = "getDeclaration"
	ActionDeleteDeclaration    = "deleteDeclaration"
	ActionAssignSet            = "assignSet"
	ActionReadEnrollment       = "readEnrollment"
	ActionReadEnrollmentStatus = "readEnrollmentStatus"
	ActionNotify               = "notify"
	ActionManageDEP            = "manageDEP"
	ActionManageBusinessMgr    = "manageBusinessManager"
	ActionReadACME             = "readACME"
	ActionManagePrincipals     = "managePrincipals"
	ActionManagePolicies       = "managePolicies"
)

// AdminActions describes every action, with operator-facing prose naming the
// consequence. `mdmctl policy actions` prints these, so an operator granting
// an action knows what they are granting rather than guessing from its name.
func AdminActions() []adminauth.Action {
	return []adminauth.Action{
		{ID: ActionPutDeclaration, Help: "Publish or replace a declaration, which changes what devices apply.", Resource: adminauth.EntityDeclaration},
		{ID: ActionGetDeclaration, Help: "Read a declaration's stored JSON.", Resource: adminauth.EntityDeclaration},
		{ID: ActionDeleteDeclaration, Help: "Remove a declaration from every set and device that has it.", Resource: adminauth.EntityDeclaration},
		{ID: ActionAssignSet, Help: "Change which declarations a device receives.", Resource: adminauth.EntityEnrollment},
		{ID: ActionReadEnrollment, Help: "Read an enrollment's declarations, tokens, and assignments.", Resource: adminauth.EntityEnrollment},
		{ID: ActionReadEnrollmentStatus, Help: "Read the status a device reported, including its inventory.", Resource: adminauth.EntityEnrollment},
		{ID: ActionNotify, Help: "Drain pending declaration changes and wake the affected devices.", Resource: adminauth.EntitySystem},
		{ID: ActionManageDEP, Help: "Administer device enrollment service accounts, tokens, and profiles.", Resource: adminauth.EntityDEPAccount},
		{ID: ActionManageBusinessMgr, Help: "List and reassign hardware in Apple Business Manager.", Resource: adminauth.EntitySystem},
		{ID: ActionReadACME, Help: "Read issued ACME identities and the hardware Apple attested for each.", Resource: adminauth.EntitySystem},
		{ID: ActionManagePrincipals, Help: "Create, rotate, and revoke admin credentials.", Resource: adminauth.EntitySystem},
		{ID: ActionManagePolicies, Help: "Edit the policies that decide what every other principal may do.", Resource: adminauth.EntitySystem},
	}
}

// adminRoute is one entry of the route table the admin mux is built from.
//
// Declaring the action beside the pattern is what makes authorization data
// rather than a call a handler might forget: the mux and the table are built
// from the same slice, and a test asserts they agree. step-ca infers the
// equivalent from a URL prefix, and the Nano family infers it from which mux
// object a handler was registered on; both are silent when wrong.
type adminRoute struct {
	// Pattern is a net/http mux pattern, "METHOD /path". A pattern ending in
	// "/" mounts a sub-tree whose routes share one action.
	Pattern string
	// Action is the adminauth action id this route requires.
	Action string
	// Family names the group a role must be able to back, so a role that did
	// not build the dependency does not register the route.
	Family  string
	Handler http.Handler
}

// Admin authorization errors.
var (
	// ErrForbidden is a caller authenticated but not permitted.
	ErrForbidden = errors.New("app: forbidden")
	// ErrAdminUnconfigured is an admin API with neither a principal store nor
	// a static token. It is a Build error rather than a silently disabled
	// API: KMFDDM logs a line and keeps serving, and NanoCMD does not even
	// log (decision record 0034).
	ErrAdminUnconfigured = errors.New("app: admin API needs MDM_ADMIN_TOKEN or an admin principal store")
)

// adminEnabled reports whether the admin API has a way to authenticate a
// caller. Build refuses to mount it otherwise rather than serving 404s.
func (a *App) adminEnabled() bool {
	return a.admin != nil || a.cfg.AdminToken != ""
}

// buildAdminMux registers every route through authorized, so authentication,
// the policy check, and the audit record are applied in one place and cannot
// be forgotten on a new route. It also records the table for GET /routes and
// for the test that asserts the table and the mux agree.
func (a *App) buildAdminMux(routes []adminRoute) (http.Handler, error) {
	reg, err := adminauth.NewRegistry(AdminActions()...)
	if err != nil {
		return nil, fmt.Errorf("app: admin actions: %w", err)
	}
	mux := http.NewServeMux()
	for _, rt := range routes {
		if rt.Action == "" {
			return nil, fmt.Errorf("app: admin route %q declares no action", rt.Pattern)
		}
		if _, ok := reg.Lookup(rt.Action); !ok {
			return nil, fmt.Errorf("app: admin route %q names unknown action %q", rt.Pattern, rt.Action)
		}
		mux.Handle(rt.Pattern, a.authorized(rt))
	}
	a.adminTable = routes
	return mux, nil
}

// AdminRoutes returns the mounted admin route table: pattern, action, and
// family, with no handlers. It is what GET /routes serves and what mdmctl
// reads to explain a 404.
func (a *App) AdminRoutes() []adminRoute { return a.adminTable }

// Pattern and action accessors keep adminRoute's fields readable from tests
// in another package without exporting the handler.
func (r adminRoute) RoutePattern() string { return r.Pattern }
func (r adminRoute) RouteAction() string  { return r.Action }
func (r adminRoute) RouteFamily() string  { return r.Family }

// staticPrincipal is who the configured single token authenticates as. It
// bypasses policy by design: MDM_ADMIN_TOKEN is the documented development
// opt-out, and a deployment that wants least privilege configures principals.
var staticPrincipal = adminauth.Principal{Name: "static-token", Root: true, TokenID: "static"}

// authorized wraps a route with authentication, the policy check, and the
// audit record. It is applied once, where the mux is built, so no route can
// be added without it.
func (a *App) authorized(rt adminRoute) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, bypass, err := a.principal(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mdm-admin"`)
			a.auditDenied(r, adminauth.Principal{}, rt, err)
			writeError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		if !bypass {
			if err := a.checkPolicy(r, p, rt); err != nil {
				a.auditDenied(r, p, rt, err)
				writeError(w, http.StatusForbidden, fmt.Errorf("%w: %s requires %q", ErrForbidden, p.Name, rt.Action))
				return
			}
		}
		a.auditAction(r, p, rt)
		rt.Handler.ServeHTTP(w, r)
	})
}

// principal authenticates the caller, reporting whether policy is bypassed.
func (a *App) principal(r *http.Request) (adminauth.Principal, bool, error) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return adminauth.Principal{}, false, ErrUnauthorized
	}
	if a.admin != nil {
		p, err := a.admin.Authenticate(r.Context(), adminauth.Token(tok))
		if err != nil {
			return adminauth.Principal{}, false, err
		}
		return p, false, nil
	}
	// The static token is compared in constant time and never short-circuits
	// on a prefix match.
	if a.cfg.AdminToken != "" && constantTimeEqual(tok, a.cfg.AdminToken) {
		return staticPrincipal, true, nil
	}
	return adminauth.Principal{}, false, ErrUnauthorized
}

// checkPolicy evaluates one route's action for the principal.
func (a *App) checkPolicy(r *http.Request, p adminauth.Principal, rt adminRoute) error {
	// Policy administration is gated by Root in Go, outside the policy system,
	// because a policy that can edit policies can grant itself anything.
	// Credential administration is an ordinary action a policy may grant; the
	// escalation guard in adminauth bounds what it can hand out (record 0034).
	if rt.Action == ActionManagePolicies {
		if !p.Root {
			return fmt.Errorf("%w: %s is not a root principal", adminauth.ErrDenied, p.Name)
		}
		return nil
	}
	d, err := a.admin.Authorize(r.Context(), p, rt.Action, a.adminResource(r), adminContext(r))
	if err != nil {
		return err
	}
	if !d.Allowed {
		return fmt.Errorf("%w: %s on %s", adminauth.ErrDenied, p.Name, rt.Action)
	}
	return nil
}

// adminResource maps the request path onto the entity a policy can name, so a
// rule can be written about one enrollment or one declaration rather than the
// deployment as a whole.
func (a *App) adminResource(r *http.Request) types.EntityUID {
	if id := r.PathValue("id"); id != "" {
		if ch := r.PathValue("channel"); ch != "" {
			return types.NewEntityUID(adminauth.EntityEnrollment, types.String(ch+"/"+id))
		}
		return types.NewEntityUID(adminauth.EntityDeclaration, types.String(id))
	}
	if name := r.PathValue("name"); name != "" {
		return types.NewEntityUID(adminauth.EntityDEPAccount, types.String(name))
	}
	return adminauth.SystemResource
}

// adminContext are the request facts a policy condition may read. Only
// bounded, server-derived values go in: never a request body, never a header.
func adminContext(r *http.Request) map[string]types.Value {
	ctx := map[string]types.Value{
		"method": types.String(r.Method),
	}
	if ch := r.PathValue("channel"); ch != "" {
		ctx["channel"] = types.String(ch)
	}
	if set := r.PathValue("set"); set != "" {
		ctx["set"] = types.String(set)
	}
	return ctx
}

// auditAction records an allowed mutating request. Reads are not audited:
// they are the bulk of admin traffic and carry no change to attribute.
func (a *App) auditAction(r *http.Request, p adminauth.Principal, rt adminRoute) {
	if a.cfg.Bus == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return
	}
	a.publishAdmin(r, event.AdminAction, p, rt, nil)
}

// auditDenied records a refusal. This is the record neither Fleet nor Zentral
// keeps: Fleet logs role denials at debug and never writes them to its
// activity feed, and Zentral emits no event at all.
func (a *App) auditDenied(r *http.Request, p adminauth.Principal, rt adminRoute, cause error) {
	if a.cfg.Bus == nil {
		return
	}
	a.publishAdmin(r, event.AdminDenied, p, rt, cause)
}

func (a *App) publishAdmin(r *http.Request, t event.Type, p adminauth.Principal, rt adminRoute, cause error) {
	data := map[string]any{
		"Action":  rt.Action,
		"Method":  r.Method,
		"Path":    r.URL.Path,
		"TokenID": p.TokenID,
	}
	if cause != nil {
		data["Reason"] = cause.Error()
	}
	actor := p.Name
	if actor == "" {
		actor = "unauthenticated"
	}
	if err := a.cfg.Bus.Publish(r.Context(), event.Event{
		Type:       t,
		At:         a.cfg.Clock.Now(),
		Enrollment: mdm.EnrollmentID{},
		Actor:      actor,
		Data:       data,
	}); err != nil {
		a.cfg.Logger.WarnContext(r.Context(), "app: publish admin event", "type", t, "error", err)
	}
}

// constantTimeEqual compares two strings in constant time. Length mismatch is
// already handled by ConstantTimeCompare, which returns 0 rather than
// short-circuiting on the first differing byte.
func constantTimeEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
