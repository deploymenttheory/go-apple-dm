package target_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// TestUnsupportedOperations checks that embedded drivers refuse every device operation.
func TestUnsupportedOperations(t *testing.T) {
	for _, tgt := range []target.Target{target.Simulator{}, target.Attached{UDID: "U"}} {
		ctx := t.Context()
		if _, err := tgt.Exec(ctx, target.Command{Args: []string{"true"}}); !errors.Is(err, target.ErrUnsupported) {
			t.Fatal(err)
		}
		if _, err := tgt.UserScript(ctx, "return 1"); !errors.Is(err, target.ErrUnsupported) {
			t.Fatal(err)
		}
		for _, err := range []error{
			tgt.OpenURL(ctx, "https://example.test"), tgt.Screenshot(ctx, "x.png"),
			tgt.Checkpoint(ctx, "c"), tgt.Restore(ctx, "c"),
		} {
			if !errors.Is(err, target.ErrUnsupported) {
				t.Fatal(err)
			}
		}
		if missing, ok := target.Has(tgt, target.CapExec); ok || missing != target.CapExec {
			t.Fatal(missing, ok)
		}
		if _, ok := target.Has(tgt); !ok {
			t.Fatal("no requirement reported missing")
		}
	}
}

// TestDescribe checks the built-in target descriptions.
func TestDescribe(t *testing.T) {
	sim, err := target.Simulator{}.Describe(t.Context())
	if err != nil || sim.Kind != target.KindSimulator {
		t.Fatal(sim, err)
	}
	dev, err := target.Attached{UDID: "U", UserID: "G"}.Describe(t.Context())
	if err != nil || dev.Kind != target.KindDevice || dev.UDID != "U" || dev.UserID != "G" {
		t.Fatal(dev, err)
	}
}
