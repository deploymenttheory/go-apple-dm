//go:build acceptance || e2e

package acceptance

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/lab"
	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// The same catalogue runs against an embedded runtime or a built executable.
func TestScenarios(t *testing.T) {
	binary := os.Getenv("LAB_DMSERVER")
	adapter := "inprocess"
	if binary != "" {
		adapter = "process"
	}
	for _, topology := range []string{"device-management"} {
		t.Run(topology, func(t *testing.T) {
			dir := t.TempDir()
			if err := lab.Init(dir, "simulated", "sqlite", "127.0.0.1:0", lab.AdapterProcess, nil); err != nil {
				t.Fatal(err)
			}
			w, err := lab.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e, err := lab.Start(ctx, w, binary, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			report := os.Getenv("LAB_REPORT_DIR")
			if report == "" {
				report = t.TempDir()
			}
			evidence := filepath.Join(report, adapter, topology)
			opts := lab.Options{Adapter: adapter, Revision: os.Getenv("LAB_REVISION"), Evidence: evidence}
			var results []lab.Result
			for _, m := range lab.Catalogue() {
				if strings.HasPrefix(m.ID, "LIVE-") {
					continue
				}
				t.Run(m.ID, func(t *testing.T) {
					r := lab.Run(ctx, e, target.Simulator{}, m, opts)
					results = append(results, r)
					if r.Status != lab.StatusPassed {
						t.Errorf("%s: %s", r.Status, r.Detail)
					}
				})
			}
			if err = lab.WriteReports(evidence, results); err != nil {
				t.Fatal(err)
			}
		})
	}
}
