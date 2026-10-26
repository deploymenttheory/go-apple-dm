package lab

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// TestABMAssignmentScenario requires persisted acceptance, terminal logs and observed convergence.
func TestABMAssignmentScenario(t *testing.T) {
	t.Parallel()
	w := testWorkspace(t, "simulated")
	selected, e := Select("E2E-021")
	if e != nil {
		t.Fatal(e)
	}
	s := selected[0]
	w.Settings = s.Settings
	environment, e := Start(t.Context(), w, "", io.Discard)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(environment.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if e = s.Execute(ctx, environment, target.Simulator{}); e != nil {
		t.Fatal(e)
	}
}
