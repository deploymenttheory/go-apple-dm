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
