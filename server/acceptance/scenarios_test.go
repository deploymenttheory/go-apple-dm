//go:build acceptance || e2e

package acceptance

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/bench"
)

// The same catalogue runs against an embedded runtime or a built executable.
func TestScenarios(t *testing.T) {
	binary := os.Getenv("BENCH_DMSERVER")
	adapter := "inprocess"
	if binary != "" {
		adapter = "process"
	}
	for _, topology := range []string{"all", "split"} {
		t.Run(topology, func(t *testing.T) {
			dir := t.TempDir()
			if err := bench.Init(dir, "simulated", "sqlite", topology, "127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			w, err := bench.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e, err := bench.Start(ctx, w, binary, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			var results []bench.Result
			for _, s := range bench.Catalogue() {
				if s.ID == "LIVE-001" {
					continue
				}
				if topology == "all" && s.ID == "E2E-010" {
					continue
				}
				if topology == "split" && s.ID != "E2E-010" {
					continue
				}
				t.Run(s.ID, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
					defer cancel()
					r := bench.Run(ctx, e, s, adapter, os.Getenv("BENCH_REVISION"), "")
					results = append(results, r)
					if r.Status != "passed" {
						t.Errorf("%s: %s", r.Status, r.Detail)
					}
				})
			}
			report := os.Getenv("BENCH_REPORT_DIR")
			if report == "" {
				report = t.TempDir()
			}
			if err = bench.WriteReports(
				filepath.Join(report, adapter, topology),
				results,
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}
