package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/ddm"
)

func TestWriteJSONMarshalError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, map[string]any{"ch": make(chan int)})
	if !strings.Contains(rec.Body.String(), `"Error"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestCloseCollectsErrors(t *testing.T) {
	a := &App{closers: []func() error{func() error { return errors.New("one") }, func() error { return nil }}}
	if err := a.Close(); err == nil || !strings.Contains(err.Error(), "one") {
		t.Fatalf("Close = %v", err)
	}
}

func TestRunSurfacesNonCancelErrors(t *testing.T) {
	a, err := Build(context.Background(), Config{Role: RoleDDM, Storage: "inmem"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := a.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v, want the deadline error wrapped", err)
	}
	if a.Notifier == nil {
		t.Fatal("notifier missing")
	}
	var _ ddm.TokenSource = a.Engine
}

// buildAdminMux is what makes an action mandatory. Both refusals are
// programming errors in this package rather than configuration ones, so they
// are asserted here rather than left to a reviewer.
func TestBuildAdminMuxRefusesUnguardedRoutes(t *testing.T) {
	a := &App{}
	if _, err := a.buildAdminMux([]adminRoute{{Pattern: "GET /x", Family: "f"}}); err == nil {
		t.Fatal("a route with no action was mounted")
	}
	if _, err := a.buildAdminMux([]adminRoute{{Pattern: "GET /x", Action: "noSuchAction", Family: "f"}}); err == nil {
		t.Fatal("a route naming an unregistered action was mounted")
	}
	// The happy path still builds.
	if _, err := a.buildAdminMux([]adminRoute{{Pattern: "GET /x", Action: ActionNotify, Family: "f"}}); err != nil {
		t.Fatalf("a well-formed route was refused: %v", err)
	}
}

// Every action the route table names is in the registry, and every registered
// action carries operator-facing prose, so `mdmctl policy actions` can say
// what granting one means.
func TestAdminActionsAreComplete(t *testing.T) {
	reg := mustAdminRegistry()
	for _, act := range AdminActions() {
		if act.Help == "" {
			t.Errorf("action %q has no help text", act.ID)
		}
		if act.Resource == "" {
			t.Errorf("action %q names no resource type", act.ID)
		}
		if _, ok := reg.Lookup(act.ID); !ok {
			t.Errorf("action %q is not in the registry", act.ID)
		}
	}
}

func TestBuildVersion(t *testing.T) {
	if buildVersion() == "" {
		t.Fatal("buildVersion returned nothing")
	}
}
