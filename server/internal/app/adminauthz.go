package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// Administrative action IDs form the registry used to validate stored policy
// references. Each route declares its action (decision record 0034).
const (
	ActionManageAppPush          = "manageAppPushCredentials"
	ActionSendAppPush            = "sendAppPush"
	ActionIssueEnrollmentProfile = "issueEnrollmentProfile"
	ActionReplaceEnrollment      = "replaceEnrollmentProfile"
	ActionPutDeclaration         = "putDeclaration"
	ActionGetDeclaration         = "getDeclaration"
	ActionDeleteDeclaration      = "deleteDeclaration"
	ActionAssignSet              = "assignSet"
	ActionReadEnrollment         = "readEnrollment"
	ActionReadEnrollmentStatus   = "readEnrollmentStatus"
	ActionNotify                 = "notify"
	ActionReadACME               = "readACME"
	ActionManagePrincipals       = "managePrincipals"
	ActionReadAudit              = "readAudit"
	ActionRetryEvents            = "retryEvents"
	ActionDisableEnrollment      = "disableEnrollment"
	ActionReadCommands           = "readCommands"
	ActionClearCommands          = "clearCommands"
	ActionPushEnrollment         = "pushEnrollment"
	ActionManagePushCerts        = "managePushCertificates"
	ActionExportEnrollments      = "exportEnrollments"
	ActionImportEnrollments      = "importEnrollments"
	ActionManagePolicies         = "managePolicies"
)

// baseAdminActions describes the static actions, with operator-facing prose naming the
// consequence. `dmctl policy actions` prints these, so an operator granting
// an action knows what they are granting rather than guessing from its name.
func baseAdminActions() []adminauth.Action {
	actions := append(setupActions(), blueprintActions()...)
	actions = append(actions, []adminauth.Action{
		{ID: ActionReadWebhooks, Help: "Read webhook subscriptions and catalogue.", Resource: adminauth.EntitySystem},
		{ID: ActionManageWebhooks, Help: "Manage webhook destinations and export server event summaries. Sensitive destinations require a separate grant.", Resource: adminauth.EntitySystem},
		{ID: ActionReadWebhookDeliveries, Help: "Inspect webhook delivery metadata and failures.", Resource: adminauth.EntitySystem},
		{ID: ActionReplayWebhooks, Help: "Retry and replay retained webhook occurrences. Sensitive captures and destinations require a separate grant.", Resource: adminauth.EntitySystem},
	}...)
	actions = append(actions, applicationIdentityActions()...)
	return append(append(actions, contentCacheActions()...), []adminauth.Action{
		{
			ID:       ActionReplaceEnrollment,
			Help:     "Replace an enrolled device's MDM profile and rotate its identity, or cancel a pending replacement.",
			Resource: adminauth.EntityEnrollment,
		},
		{
			ID:       ActionManageAppPush,
			Help:     "Import, renew and list app push credentials.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionSendAppPush,
			Help:     "Send app alert and background notifications.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionIssueEnrollmentProfile,
			Help:     "Issue a configured device enrollment profile.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionReadCertificates,
			Help:     "Read certificate issuance and revocation status.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionImportCertificates,
			Help:     "Register an existing CA-issued certificate for status enforcement.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionRevokeCertificates,
			Help:     "Permanently revoke a device certificate, stopping its use and renewal.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionPutDeclaration,
			Help:     "Publish or replace a declaration, which changes what devices apply.",
			Resource: adminauth.EntityDeclaration,
		},
		{
			ID:       ActionGetDeclaration,
			Help:     "Read a declaration's stored JSON.",
			Resource: adminauth.EntityDeclaration,
		},
		{
			ID:       ActionDeleteDeclaration,
			Help:     "Remove a declaration from every set and device that has it.",
			Resource: adminauth.EntityDeclaration,
		},
		{
			ID:       ActionAssignSet,
			Help:     "Change which declarations a device receives.",
			Resource: adminauth.EntityEnrollment,
		},
		{
			ID:       ActionReadEnrollment,
			Help:     "Read an enrollment's declarations, tokens, and assignments.",
			Resource: adminauth.EntityEnrollment,
		},
		{
			ID:       ActionReadEnrollmentStatus,
			Help:     "Read the status a device reported, including its inventory.",
			Resource: adminauth.EntityEnrollment,
		},
		{
			ID:       ActionNotify,
			Help:     "Drain pending declaration changes and wake the affected devices.",
			Resource: adminauth.EntitySystem,
		},

		{
			ID:       ActionReadACME,
			Help:     "Read issued ACME identities and the hardware Apple attested for each.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionManagePrincipals,
			Help:     "Create, rotate, and revoke admin credentials.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionManagePolicies,
			Help:     "Edit the policies that decide what every other principal may do.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionDisableEnrollment,
			Help:     "Stop an enrollment receiving commands and pushes, as a check-out would.",
			Resource: adminauth.EntityEnrollment,
		},

		{
			ID:       ActionReadCommands,
			Help:     "Read command queue metadata without raw response content.",
			Resource: adminauth.EntityEnrollment,
		},
		{
			ID:       ActionClearCommands,
			Help:     "Discard an enrollment's pending commands.",
			Resource: adminauth.EntityEnrollment,
		},
		{
			ID:       ActionPushEnrollment,
			Help:     "Wake a device now with an APNs push, without queueing anything.",
			Resource: adminauth.EntityEnrollment,
		},
		{
			ID:       ActionManagePushCerts,
			Help:     "Read push certificate topics and expiry, and upload or renew one.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionExportEnrollments,
			Help:     "Export enrollments, including bootstrap and unlock tokens, for migration.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionImportEnrollments,
			Help:     "Write an exported enrollment record, tokens and pins included.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionReadAudit,
			Help:     "Read the audit trail: who did what, when, and to which enrollment.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionRetryEvents,
			Help:     "Retry a blocked or waiting event destination.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionReadConfig,
			Help:     "Read the server configuration and route table. Authenticated callers always may; a policy does not gate it.",
			Resource: adminauth.EntitySystem,
		},
	}...)
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
	// Family groups related routes for introspection and enrollment resolution.
	Family string
	// LocalMutation means the handler's mutations use the shared SQL pool and
	// perform no remote calls. Its response is withheld until event capture and
	// the mutation commit together.
	LocalMutation bool
	// Introspection routes expose role and route metadata without fleet data. They
	// require authentication but bypass policy evaluation so authenticated clients
	// can determine which families the process serves.
	Introspection      bool
	Handler            http.Handler
	ResourceParam      string
	NotifyDeclarations bool
	Command            bool
	RequestType        string
	Sensitive          bool
	// MaxResponseBytes bounds buffered responses; zero uses MaxAdminBody.
	MaxResponseBytes int
}

// Admin authorization errors.
var (
	// ErrForbidden is a caller authenticated but not permitted.
	ErrForbidden = errors.New("app: forbidden")
)

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
	for i := range routes {
		rt := routes[i]
		if rt.Action == "" {
			return nil, fmt.Errorf("%w: admin route %q declares no action", ErrConfig, rt.Pattern)
		}
		action, ok := reg.Lookup(rt.Action)
		if !ok {
			return nil, fmt.Errorf(
				"%w: admin route %q names unknown action %q",
				ErrConfig, rt.Pattern,
				rt.Action,
			)
		}
		rt.ResourceParam = resourceParameter(action.Resource)
		rt.Sensitive = action.Sensitive
		routes[i] = rt
		mux.Handle(rt.Pattern, a.authorized(rt))
	}
	a.adminTable = routes
	return mux, nil
}

// AdminRoutes returns the mounted admin route table: pattern, action, and
// family, with no handlers. It is what GET /routes serves and what dmctl
// reads to explain a 404.
func (a *App) AdminRoutes() []adminRoute { return a.adminTable }

// Pattern and action accessors keep adminRoute's fields readable from tests
// in another package without exporting the handler.
func (r adminRoute) RoutePattern() string { return r.Pattern }
func (r adminRoute) RouteAction() string  { return r.Action }
func (r adminRoute) RouteFamily() string  { return r.Family }

// authorized wraps a route with authentication, the policy check, and the
// audit record. It is applied once, where the mux is built, so no route can
// be added without it.
func (a *App) authorized(route adminRoute) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt := route // Dynamic command actions belong to this request only.
		w.Header().Set("Cache-Control", "no-store")
		p, err := a.principal(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mdm-admin"`)
			a.auditDenied(r, adminauth.Principal{}, rt, err)
			writeError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		if r.PathValue("channel") != "" && r.PathValue("id") != "" {
			channel, err := channelFromName(r.PathValue("channel"))
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			canonical, err := a.resolveAdminEnrollment(r, rt.Family)
			if err != nil {
				status := http.StatusInternalServerError
				if errors.Is(err, storage.ErrNotFound) || errors.Is(err, ddm.ErrNotFound) {
					status = http.StatusNotFound
				}
				if errors.Is(err, mdm.ErrInvalidEnrollment) {
					status = http.StatusBadRequest
				}
				writeError(w, status, err)
				return
			}
			if canonical.Channel != channel ||
				(r.URL.Query().Get("parent") != "" && r.URL.Query().Get("parent") != canonical.ParentID) {
				writeError(w, http.StatusNotFound, storage.ErrNotFound)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), canonicalEnrollmentKey{}, canonical))
		}
		if !rt.Introspection {
			var err error
			r, err = a.resolvePermission(r, &rt)
			if err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, ErrBodyTooLarge) {
					status = http.StatusRequestEntityTooLarge
				}
				writeError(w, status, err)
				return
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), actorKey{}, p))
		if !rt.Introspection {
			if err := a.checkPolicy(r, p, rt); err != nil {
				a.auditDenied(r, p, rt, err)
				writeError(
					w,
					http.StatusForbidden,
					fmt.Errorf("%w: %s requires %q", ErrForbidden, p.Name, rt.Action),
				)
				return
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), setupActorKey{}, p.Name))
		if rt.LocalMutation && a.eventPublisher != nil && r.Method != http.MethodGet &&
			r.Method != http.MethodHead {
			a.localAdmin(w, r, p, rt)
			return
		}
		if rt.Sensitive && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			a.sensitiveAdminRead(w, r, p, rt)
			return
		}
		if err := a.auditAction(r, p, rt); errors.Is(err, event.ErrCapture) {
			w.Header().Set("Retry-After", "5")
			writeError(w, http.StatusServiceUnavailable, event.ErrCapture)
			return
		}
		rec := &statusRecorder{ResponseWriter: w}
		rt.Handler.ServeHTTP(rec, r)
		setAdminOutcome(r, rec.status)
		if err := a.auditAction(r, p, rt); err != nil {
			a.cfg.Logger.ErrorContext(r.Context(), "app: record administrative outcome", "error", err)
		}
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

// kickNotifier requests a drain after successful DDM mutations. Persistent
// change rows remain the notification signal; this wrapper reduces polling
// latency. Kick is nonblocking, and a drain without pending rows has no effect.
func (a *App) kickNotifier(rt adminRoute, r *http.Request, status int) {
	if a.Notifier == nil || !rt.NotifyDeclarations {
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

// principal authenticates a stored administrator credential.
func (a *App) principal(r *http.Request) (adminauth.Principal, error) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return adminauth.Principal{}, ErrUnauthorized
	}

	if a.admin != nil {
		p, err := a.admin.Authenticate(r.Context(), adminauth.Token(tok))
		if err != nil {
			return adminauth.Principal{}, fmt.Errorf(
				"app: authenticate administrator: %w",
				err,
			)
		}
		return p, nil
	}
	return adminauth.Principal{}, ErrUnauthorized
}

// checkPolicy evaluates one route's action for the principal.
func (a *App) checkPolicy(r *http.Request, p adminauth.Principal, rt adminRoute) error {
	if p.Root && (authorityMutation(rt) || rt.Action == ActionReadPrincipals || rt.Action == ActionReadRoles || rt.Action == ActionReadPolicies) {
		if req, ok := r.Context().Value(authorizationKey{}).(*authorizationRequest); ok {
			version, err := a.admin.Version(r.Context())
			if err != nil {
				return err
			}
			req.Decision = adminauth.Decision{Allowed: true, Version: version}
		}
		return nil
	}
	if authorityMutation(rt) {
		if !p.Root {
			return fmt.Errorf("%w: authority administration requires root", adminauth.ErrDenied)
		}
		return nil
	}
	req, ok := r.Context().Value(authorizationKey{}).(*authorizationRequest)
	if !ok {
		return adminauth.ErrDenied
	}
	d, err := a.admin.Authorize(r.Context(), p, rt.Action, req.Resource, req.Context)
	req.Decision = d
	if err != nil {
		req.Decision.Errors = []string{err.Error()}
		return fmt.Errorf("app: authorize administrator: %w", err)
	}
	if !d.Allowed {
		return fmt.Errorf("%w: %s on %s", adminauth.ErrDenied, p.Name, rt.Action)
	}
	return nil
}

// auditAction records allowed mutations and sensitive reads.
func (a *App) auditAction(r *http.Request, p adminauth.Principal, rt adminRoute) error {
	if a.cfg.publisher() == nil || (!rt.Sensitive && (r.Method == http.MethodGet || r.Method == http.MethodHead)) {
		return nil
	}
	return a.publishAdmin(r, event.AdminAction, p, rt, nil)
}

// auditDenied records a refusal. A denial is evidence: it is what shows an
// operator that a credential is reaching for something it should not have
// (decision record 0034).
func (a *App) auditDenied(r *http.Request, p adminauth.Principal, rt adminRoute, cause error) {
	if a.cfg.publisher() == nil {
		return
	}
	if err := a.publishAdmin(r, event.AdminDenied, p, rt, cause); err != nil {
		a.cfg.Logger.ErrorContext(
			r.Context(),
			"app: admin denial could not be recorded",
			"error",
			err,
		)
	}
}

func (a *App) publishAdmin(
	r *http.Request,
	t event.Type,
	p adminauth.Principal,
	rt adminRoute,
	cause error,
) error {
	data := map[string]any{
		"Action":  rt.Action,
		"Method":  r.Method,
		"Path":    r.URL.Path,
		"Outcome": "authorized",
		"TokenID": p.TokenID,
	}
	if req, ok := r.Context().Value(authorizationKey{}).(*authorizationRequest); ok {
		data["Resource"] = req.Resource.String()
		if req.Outcome != "" {
			data["Outcome"] = req.Outcome
			data["Status"] = req.Status
		}
		data["Policies"] = req.Decision.Policies
		data["PolicyVersion"] = req.Decision.Version
		if len(req.Decision.Errors) > 0 {
			data["EvaluationErrors"] = req.Decision.Errors
		}
	}
	if cause != nil {
		data["Reason"] = cause.Error()
		data["Outcome"] = "denied"
	}
	actor := p.Name
	if actor == "" {
		actor = "unauthenticated"
	}
	err := a.cfg.publisher().Publish(r.Context(), event.Event{
		Type:       t,
		At:         a.cfg.Clock.Now(),
		Enrollment: mdm.EnrollmentID{},
		Actor:      actor,
		Data:       data,
	})
	if err != nil && !errors.Is(err, event.ErrQueueFull) {
		a.cfg.Logger.WarnContext(r.Context(), "app: publish admin event", "type", t, "error", err)
	}
	return wrapError(err)
}

// constantTimeEqual compares two strings in constant time. Length mismatch is
// already handled by ConstantTimeCompare, which returns 0 rather than
// short-circuiting on the first differing byte.
func constantTimeEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (a *App) resolveAdminEnrollment(r *http.Request, family string) (mdm.EnrollmentID, error) {
	e, err := a.Store.EnrollmentByID(r.Context(), r.PathValue("id"))
	if err == nil {
		return e.ID, nil
	}
	if !errors.Is(err, storage.ErrNotFound) || family != "ddm" {
		return mdm.EnrollmentID{}, fmt.Errorf("app: resolve MDM identity: %w", err)
	}
	id, err := a.Engine.EnrollmentIdentity(r.Context(), r.PathValue("id"))
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ddm.ErrNotFound) {
		return mdm.EnrollmentID{}, fmt.Errorf("app: resolve DDM identity: %w", err)
	}
	// Declaration preassignments are supported before the MDM row exists.
	// The first authorized assignment establishes an immutable DDM identity.
	return enrollmentFromPath(r)
}
