package app_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/app"
)

// pathFor turns a route pattern into a request path a mux can resolve,
// substituting a plausible value for each wildcard.
func pathFor(pattern string) (method, path string) {
	method, rest, ok := strings.Cut(pattern, " ")
	if !ok {
		method, rest = http.MethodGet, pattern
	}
	var out []string
	for _, seg := range strings.Split(strings.TrimPrefix(rest, "/"), "/") {
		switch {
		case seg == "{channel}":
			out = append(out, "device")
		case strings.HasPrefix(seg, "{"):
			out = append(out, "x")
		case seg == "":
			// A trailing empty segment is a sub-tree mount; give it a child
			// so the request reaches the sub-handler rather than the prefix.
			out = append(out, "x")
		default:
			out = append(out, seg)
		}
	}
	return method, "/admin/v1/" + strings.Join(out, "/")
}

func adminApp(t *testing.T, bus *event.Bus) *app.App {
	t.Helper()
	return build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", AdminToken: "secret",
		Listen: ":0", Bus: bus,
	})
}

// The route table is the authorization model: a route with no action, or an
// action no policy could name, would be unguarded. step-ca infers the
// equivalent from a URL prefix and the Nano family from which mux a handler
// was registered on; neither can be asserted.
func TestAdminRoutes(t *testing.T) {
	a := adminApp(t, nil)
	routes := a.AdminRoutes()
	if len(routes) == 0 {
		t.Fatal("no admin routes were registered")
	}

	t.Run("EveryRouteHasAnAction", func(t *testing.T) {
		known := make(map[string]bool)
		for _, act := range app.AdminActions() {
			known[act.ID] = true
		}
		for _, rt := range routes {
			if rt.RouteAction() == "" {
				t.Fatalf("route %q declares no action", rt.RoutePattern())
			}
			if !known[rt.RouteAction()] {
				t.Fatalf("route %q names action %q, which is not in the registry", rt.RoutePattern(), rt.RouteAction())
			}
			if rt.RouteFamily() == "" {
				t.Fatalf("route %q declares no family", rt.RoutePattern())
			}
		}
	})

	// Every table entry is mounted and wrapped: an unauthenticated request
	// gets 401 from the wrapper rather than 404 from an unmounted pattern.
	// The reverse direction holds by construction, since buildAdminMux
	// registers only from this table.
	t.Run("MatchesMountedMux", func(t *testing.T) {
		srv := serve(t, a)
		for _, rt := range routes {
			method, path := pathFor(rt.RoutePattern())
			req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s %s (from %q) = %d, want 401 from the authorization wrapper",
					method, path, rt.RoutePattern(), resp.StatusCode)
			}
			if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
				t.Fatalf("%s %s: WWW-Authenticate = %q", method, path, got)
			}
		}
	})

	t.Run("UnknownPathIsNotFound", func(t *testing.T) {
		srv := serve(t, a)
		resp, err := srv.Client().Get(srv.URL + "/admin/v1/no-such-route")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("unknown admin path = %d, want 404", resp.StatusCode)
		}
	})
}

// recorder collects events off the bus.
type recorder struct {
	mu     sync.Mutex
	events []event.Event
}

func (r *recorder) handle(_ context.Context, e event.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) ofType(t event.Type) []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []event.Event
	for _, e := range r.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// Neither Fleet nor Zentral records an authorization denial as an event:
// Fleet logs role denials at debug and never writes them to its activity
// feed, and Zentral emits none at all. This is that record.
func TestAdminAudit(t *testing.T) {
	bus := event.New()
	rec := &recorder{}
	bus.Subscribe(event.All, rec.handle)
	a := adminApp(t, bus)
	srv := serve(t, a)

	t.Run("PublishesAdminDenied", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut,
			srv.URL+"/admin/v1/declarations", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer wrong")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		denied := rec.ofType(event.AdminDenied)
		if len(denied) == 0 {
			t.Fatal("a refused admin request published no AdminDenied event")
		}
		got := denied[len(denied)-1]
		data, ok := got.Data.(map[string]any)
		if !ok {
			t.Fatalf("event data = %T", got.Data)
		}
		if data["Action"] != app.ActionPutDeclaration {
			t.Fatalf("denied action = %v", data["Action"])
		}
		if got.Actor != "unauthenticated" {
			t.Fatalf("actor = %q", got.Actor)
		}
	})

	// A token in a log line or an event is a credential in a log aggregator.
	t.Run("NeverLogsToken", func(t *testing.T) {
		for _, e := range rec.events {
			data, ok := e.Data.(map[string]any)
			if !ok {
				continue
			}
			for k, v := range data {
				if s, isStr := v.(string); isStr && strings.Contains(s, "secret") {
					t.Fatalf("event %s field %s carries the admin token: %q", e.Type, k, s)
				}
			}
		}
	})

	t.Run("PublishesAdminAction", func(t *testing.T) {
		before := len(rec.ofType(event.AdminAction))
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
			srv.URL+"/admin/v1/notify", nil)
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("notify = %d, want 200", resp.StatusCode)
		}
		actions := rec.ofType(event.AdminAction)
		if len(actions) <= before {
			t.Fatal("an allowed mutation published no AdminAction event")
		}
		got := actions[len(actions)-1]
		if got.Actor == "" {
			t.Fatal("AdminAction carries no actor")
		}
		data := got.Data.(map[string]any)
		if data["Action"] != app.ActionNotify {
			t.Fatalf("action = %v, want %v", data["Action"], app.ActionNotify)
		}
	})

	// Reads are the bulk of admin traffic and change nothing, so they are not
	// audited; only mutations are.
	t.Run("ReadsAreNotAudited", func(t *testing.T) {
		before := len(rec.ofType(event.AdminAction))
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
			srv.URL+"/admin/v1/declarations/nope", nil)
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if got := len(rec.ofType(event.AdminAction)); got != before {
			t.Fatalf("a GET published %d new AdminAction events", got-before)
		}
	})
}

// The admin API must not mount without a way to authenticate a caller.
// KMFDDM logs a line and keeps serving; NanoCMD does not even log.
func TestAdminNotMountedWithoutCredential(t *testing.T) {
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", Listen: ":0"})
	srv := serve(t, a)
	resp, err := srv.Client().Get(srv.URL + "/admin/v1/declarations/x")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 with no admin credential configured", resp.StatusCode)
	}
}
