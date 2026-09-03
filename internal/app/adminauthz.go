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
	ActionReadAudit            = "readAudit"
	ActionDisableEnrollment    = "disableEnrollment"
	ActionEnqueueCommand       = "enqueueCommand"
	ActionReadCommands         = "readCommands"
	ActionClearCommands        = "clearCommands"
	ActionPushEnrollment       = "pushEnrollment"
	ActionManagePushCerts      = "managePushCertificates"
	ActionExportEnrollments    = "exportEnrollments"
	ActionImportEnrollments    = "importEnrollments"
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
		{ID: ActionDisableEnrollment, Help: "Stop an enrollment receiving commands and pushes, as a check-out would.", Resource: adminauth.EntityEnrollment},
		{ID: ActionEnqueueCommand, Help: "Send any MDM command to a device, including erase and lock.", Resource: adminauth.EntityEnrollment},
		{ID: ActionReadCommands, Help: "Read an enrollment's command queue and the results devices returned.", Resource: adminauth.EntityEnrollment},
		{ID: ActionClearCommands, Help: "Discard an enrollment's pending commands.", Resource: adminauth.EntityEnrollment},
		{ID: ActionPushEnrollment, Help: "Wake a device now with an APNs push, without queueing anything.", Resource: adminauth.EntityEnrollment},
		{ID: ActionManagePushCerts, Help: "Read push certificate topics and expiry, and upload or renew one.", Resource: adminauth.EntitySystem},
		{ID: ActionExportEnrollments, Help: "Export enrollments, including bootstrap and unlock tokens, for migration.", Resource: adminauth.EntitySystem},
		{ID: ActionImportEnrollments, Help: "Write an exported enrollment record, tokens and pins included.", Resource: adminauth.EntitySystem},
		{ID: ActionReadAudit, Help: "Read the audit trail: who did what, when, and to which enrollment.", Resource: adminauth.EntitySystem},
		{ID: ActionReadConfig, Help: "Read the server's role and route table. Authenticated callers always may; a policy does not gate it.", Resource: adminauth.EntitySystem},
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
	Family string
	// Introspection marks a route that answers what the server is rather than
	// what it holds: the role it runs, and the route table itself. Those are
	// authenticated but not policy-gated, because a caller needs them to
	// interpret a 404 that is really a role split, and a policy that had to
	// grant them first would make the explanation unreachable exactly when it
	// is needed. They return no fleet data.
	Introspection bool
	Handler       http.Handler
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
// caller. With neither a principal store nor a static token the API is not
// mounted at all, rather than mounted and unguarded.
func (a *App) adminEnabled() bool {
	return a.cfg.AdminStore != nil || a.cfg.AdminStoreEnabled || a.cfg.AdminToken != ""
}

// mustAdminRegistry builds the action registry from the action table. The
// table is a compile-time constant, so a failure here is a programming error
// in this package rather than a configuration one.
func mustAdminRegistry() *adminauth.Registry {
	reg, err := adminauth.NewRegistry(AdminActions()...)
	if err != nil {
		panic("app: admin action registry: " + err.Error())
	}
	return reg
}

// buildAdminMux registers every route through authorized, so authentication,
// the policy check, and the audit record are applied in one place and cannot
// be forgotten on a new route. It also records the table for GET /routes and
// for the test that asserts the table and the mux agree.
func (a *App) buildAdminMux(routes []adminRoute) (http.Handler, error) {
	reg := mustAdminRegistry()
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

// BreakGlassActor is the audit actor for a request authenticated by the
// static MDM_ADMIN_TOKEN rather than by a stored principal. It is a fixed
// string, so an operator can alert on it: after the first principals exist,
// a record carrying this actor means someone used the standing root
// credential that should have been removed.
const BreakGlassActor = "break-glass"

// breakGlassPrincipal is who the static token authenticates as. It is root
// and bypasses policy by design. Alongside a principal store it is the
// bootstrap credential, because an empty store authenticates nobody and the
// route that creates the first principal is itself authorized.
var breakGlassPrincipal = adminauth.Principal{Name: BreakGlassActor, Root: true, TokenID: "static"}

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
		if !bypass && !rt.Introspection {
			if err := a.checkPolicy(r, p, rt); err != nil {
				a.auditDenied(r, p, rt, err)
				writeError(w, http.StatusForbidden, fmt.Errorf("%w: %s requires %q", ErrForbidden, p.Name, rt.Action))
				return
			}
		}
		a.auditAction(r, p, rt)
		rec := &statusRecorder{ResponseWriter: w}
		rt.Handler.ServeHTTP(rec, r)
		a.kickNotifier(rt, r, rec.status)
	})
}

// statusRecorder remembers the status a handler wrote. A handler that writes
// a body without calling WriteHeader has implicitly sent 200.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	if err != nil {
		return n, fmt.Errorf("app: write response: %w", err)
	}
	return n, nil
}

// kickNotifier shortens the wait after a declarative change.
//
// The durable signal is the change rows the engine writes inside its
// transaction, which the notifier drains on its poll; this only saves the
// poll interval. It lives here, in the one wrapper every admin route passes
// through, rather than in each handler: KMFDDM repeats the equivalent call in
// nine places, and a tenth route added later would silently not notify.
//
// Kick never blocks and a drain with no rows does nothing, so a successful
// mutating request on the ddm family is a good enough trigger without asking
// the engine whether anything actually changed.
func (a *App) kickNotifier(rt adminRoute, r *http.Request, status int) {
	if a.Notifier == nil || rt.Family != "ddm" {
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return
	}
	if status != 0 && status >= 300 {
		return
	}
	a.Notifier.Kick()
}

// principal authenticates the caller, reporting whether policy is bypassed.
func (a *App) principal(r *http.Request) (adminauth.Principal, bool, error) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return adminauth.Principal{}, false, ErrUnauthorized
	}
	// The break-glass token is tried first, and in constant time, so it still
	// works when the principal store is empty or unreachable -- which is
	// exactly when it is needed. It never short-circuits on a prefix match.
	if a.cfg.AdminToken != "" && constantTimeEqual(tok, a.cfg.AdminToken) {
		if a.admin != nil {
			// Only worth saying when principals exist: until they do, the
			// static token is the intended and only way in.
			a.cfg.Logger.WarnContext(r.Context(), "app: admin request used the break-glass token, which bypasses policy; create principals and unset MDM_ADMIN_TOKEN",
				"actor", BreakGlassActor, "method", r.Method, "path", r.URL.Path)
		}
		return breakGlassPrincipal, true, nil
	}
	if a.admin != nil {
		p, err := a.admin.Authenticate(r.Context(), adminauth.Token(tok))
		if err != nil {
			return adminauth.Principal{}, false, err
		}
		return p, false, nil
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
