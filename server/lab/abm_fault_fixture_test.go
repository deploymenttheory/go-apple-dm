package lab

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// withABMFaultFixture isolates durable intent and drives ordinary workers in virtual time.
// Apple uses the real OAuth/resource/activity fixture over TLS; administrative HTTP is synchronous.
func withABMFaultFixture(t *testing.T, run func(*testing.T, *Environment)) {
	t.Helper()
	fake := axmtest.NewServer()
	defer fake.Close()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	der, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	fake.RegisterKey("BUSINESSAPI.bench", "bench-key", &key.PublicKey)
	fake.AddMDMServer("bench", nil)
	fake.SetConsistencyLag(time.Second)
	for _, serial := range []string{"BENCH-ABM-1", "BENCH-ABM-2", "BENCH-ABM-3"} {
		fake.AddOrgDevice(serial, nil)
	}
	// Align fixture timestamps with the bubble without moving its listener goroutines into it.
	var now atomic.Int64
	fake.SetNow(func() time.Time { return time.Unix(0, now.Load()) })
	// Run all real socket exchanges outside the bubble. The bridge channels are
	// created here so external network goroutines never touch bubble-owned channels.
	network := fake.Client()
	network.Timeout = 5 * time.Second
	requests := make(chan *http.Request)
	type result struct {
		response *http.Response
		err      error
	}
	responses := make(chan result)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for request := range requests {
			networkRequest := request.Clone(context.Background())
			// http.Client also populates the legacy Cancel channel; it must stay in its bubble.
			networkRequest.Cancel = nil //nolint:staticcheck // SA1019: http.Client populated this channel inside the bubble; clearing it prevents cross-bubble use.
			response, e := network.Do(networkRequest)
			if e == nil {
				raw, readErr := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
				closeErr := response.Body.Close()
				e = errors.Join(readErr, closeErr)
				if len(raw) > 32<<20 {
					e = errors.New("fixture response exceeded bridge bound")
				}
				response.Body = io.NopCloser(bytes.NewReader(raw))
			}
			responses <- result{response, e}
		}
	}()
	defer func() { close(requests); <-stopped; network.CloseIdleConnections() }()
	var networkMu sync.Mutex
	client := &http.Client{}
	var mu sync.Mutex
	reads := map[string]int{}
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		now.Store(time.Now().UnixNano())
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/orgDeviceActivities/") && !strings.HasSuffix(r.URL.Path, "/download") {
			mu.Lock()
			reads[r.URL.Path]++
			complete := reads[r.URL.Path] > 1
			mu.Unlock()
			if complete {
				fake.Complete()
			}
		}
		networkMu.Lock()
		defer networkMu.Unlock()
		requests <- r
		result := <-responses
		return result.response, result.err
	})
	synctest.Test(t, func(t *testing.T) {
		application, e := app.Build(t.Context(), app.Config{Storage: "inmem", BootstrapToken: "fixture-admin", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), AxM: app.AxMConfig{ClientID: "BUSINESSAPI.bench", KeyID: "bench-key", KeyPEM: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), BaseURL: fake.URL, TokenURL: fake.TokenURL, HTTPClient: client}})
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- application.Run(ctx) }()
		defer func() {
			cancel()
			if e := <-done; e != nil {
				t.Error(e)
			}
			if e := application.Close(); e != nil {
				t.Error(e)
			}
		}()
		synctest.Wait()
		admin := synchronousHTTPClient(application.Handler)
		adminBase := admin.Transport
		admin.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { synctest.Wait(); return adminBase.RoundTrip(r) })
		workspace := &Workspace{Directory: t.TempDir()}
		if e = os.MkdirAll(workspace.path("mdm"), 0o700); e != nil {
			t.Fatal(e)
		}
		if e = privateFile(workspace.path("mdm", "admin-token"), []byte("fixture-admin")); e != nil {
			t.Fatal(e)
		}
		environment := &Environment{Instance: Instance{URL: "https://lab.invalid", Mode: "simulated"}, Client: admin, Token: "fixture-admin", Workspace: workspace}
		if e = environment.authorizeLab(t.Context()); e != nil {
			t.Fatal(e)
		}
		run(t, environment)
	})
}

// checkABMInterruptedExchanges replays every baseline exchange fault against independent durable intent state.
func checkABMInterruptedExchanges(t *testing.T, s Module) {
	t.Helper()
	var selection faultSelection
	withABMFaultFixture(t, func(t *testing.T, e *Environment) {
		t.Helper()
		probe := &scenarioFault{base: e.Client.Transport, record: true}
		e.Client.Transport = probe
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		if err := s.Execute(ctx, e, target.Simulator{}); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		assertABMTraceBoundaries(t, probe.observations)
		selection = selectABMFaults(probe.observations)
	})
	traceDir := t.TempDir()
	if configured := os.Getenv("DM_LAB_FAULT_TRACE_DIR"); configured != "" {
		traceDir = configured
		// #nosec G703 -- The test runner explicitly selects this private artifact directory; protocol inputs never control it.
		if e := os.MkdirAll(traceDir, 0o700); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := json.MarshalIndent(selection, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	// #nosec G703 -- The test runner explicitly selects the artifact directory and this filename is fixed.
	if e = os.WriteFile(filepath.Join(traceDir, "abm-fault-selection.json"), raw, 0o600); e != nil {
		t.Fatal(e)
	}
	t.Logf("ABM fault coverage: %d original exchanges, %d semantic classes, %d selected exchanges; trace %s; fallback %q", len(selection.Trace), len(selection.Groups), len(selection.Selected), filepath.Join(traceDir, "abm-fault-selection.json"), selection.Fallback)
	for _, n := range selection.Selected {
		path := selection.Trace[n].Method + " " + selection.Trace[n].Path
		t.Run(fmt.Sprintf("%02d_%s", n+1, path), func(t *testing.T) {
			withABMFaultFixture(t, func(t *testing.T, e *Environment) {
				t.Helper()
				fault := &scenarioFault{base: e.Client.Transport, at: n + 1}
				e.Client.Transport = fault
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
				defer cancel()
				err := s.Execute(ctx, e, target.Simulator{})
				if !fault.hit {
					t.Fatalf("exchange was not reached: %v", err)
				}
				if err == nil {
					t.Error("interrupted scenario reported success")
				}
			})
		})
	}
}
