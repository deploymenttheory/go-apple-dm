package bench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

type scenarioFault struct {
	base      http.RoundTripper
	at, count int
	hit       bool
	paths     []string
}

func (f *scenarioFault) RoundTrip(r *http.Request) (*http.Response, error) {
	// Cleanup and the explicit unauthorized probe are outside scenario success evidence.
	if r.Method != "DELETE" && r.Header.Get("Authorization") != "Bearer invalid" {
		f.count++
		f.paths = append(f.paths, r.Method+" "+r.URL.Path)
		if f.at == f.count {
			f.hit = true
			return nil, errors.New("fixture connection lost")
		}
	}
	return f.base.RoundTrip(r)
}

func testWorkspace(t *testing.T, mode string) *Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir, mode, "inmem", "all", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	w, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// Exercise the real scenario orchestration against the reference server, breaking
// one exchange at a time. A successful earlier enrollment must not mask a later
// outage, lost acknowledgement, or unavailable administrative result.
func TestScenariosRejectInterruptedExchanges(t *testing.T) {
	for _, s := range Catalogue() {
		if s.ID == "LIVE-001" || s.ID == "E2E-003" || s.ID == "E2E-010" {
			continue
		}
		t.Run(s.ID, func(t *testing.T) {
			w := testWorkspace(t, "simulated")
			w.Settings = s.Settings
			e, err := Start(t.Context(), w, "", io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			base := e.Client.Transport
			probe := &scenarioFault{base: base}
			e.Client.Transport = probe
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			if err := s.Run(ctx, e, ""); err != nil {
				t.Fatalf("baseline: %v", err)
			}
			for n, path := range probe.paths {
				t.Run(fmt.Sprintf("%02d_%s", n+1, path), func(t *testing.T) {
					fault := &scenarioFault{base: base, at: n + 1}
					e.Client.Transport = fault
					err := s.Run(ctx, e, "")
					if !fault.hit {
						t.Fatalf("exchange was not reached: %v", err)
					}
					if err == nil {
						t.Error("interrupted scenario reported success")
					}
				})
			}
			e.Client.Transport = base
		})
	}
}
