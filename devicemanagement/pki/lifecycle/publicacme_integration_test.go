//go:build integration

package lifecycle

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

type dropACMEResponse struct {
	base    http.RoundTripper
	dropped atomic.Bool
}

// RoundTrip discards the first successful account-creation response to simulate a lost
// registration reply.
func (d *dropACMEResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := d.base.RoundTrip(r)
	if err == nil && response.StatusCode == http.StatusCreated && d.dropped.CompareAndSwap(false, true) {
		_ = response.Body.Close()
		return nil, errors.New("test: lost account registration response")
	}
	return response, err
}

// TestPebbleHTTP01Recovery uses actual HTTP-01 validation, including the host and
// token check, and deliberately loses the first account registration response.
func TestPebbleHTTP01Recovery(t *testing.T) {
	binary, source := os.Getenv("PEBBLE_BINARY"), os.Getenv("PEBBLE_SOURCE")
	if binary == "" || source == "" {
		t.Skip("set PEBBLE_BINARY and PEBBLE_SOURCE to a built Pebble v2.8.0 checkout")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	store := state.NewMemory()
	var offset atomic.Int64
	store.Now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	manager := &Manager{Store: store}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "[::]:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: manager.HTTP01Handler(), ReadHeaderTimeout: time.Second}
	defer func(cleanup func() error) { _ = cleanup() }(server.Close)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	}()
	port := requireType[*net.TCPAddr](t, listener.Addr()).Port
	address := func() string {
		l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func(cleanup func() error) { _ = cleanup() }(l.Close)
		return l.Addr().String()
	}
	api, management := address(), address()
	config := map[string]any{"pebble": map[string]any{
		"listenAddress": api, "managementListenAddress": management,
		"certificate": filepath.Join(source, "test/certs/localhost/cert.pem"),
		"privateKey":  filepath.Join(source, "test/certs/localhost/key.pem"),
		"httpPort":    port, "tlsPort": 5001,
		"retryAfter": map[string]int{"authz": 1, "order": 1},
		"profiles":   map[string]any{"default": map[string]any{"validityPeriod": 7776000}},
	}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pebble.json")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G204 G702 -- Execute the explicitly configured test binary with argument separation; no shell.
	command := exec.CommandContext(ctx, binary, "-config", path)
	command.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_VA_ALWAYS_VALID=0", "PEBBLE_AUTHZREUSE=0", "PEBBLE_WFE_NONCEREJECT=0")
	var logs bytes.Buffer
	command.Stdout, command.Stderr = &logs, &logs
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = command.Wait()
		if t.Failed() {
			t.Log(logs.String())
		}
	}()
	// #nosec G304 G703 -- The test controls this fixture path within its private workspace.
	root, err := os.ReadFile(filepath.Join(source, "test/certs/pebble.minica.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(root) {
		t.Fatal("test TLS root")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	get := func(raw string) []byte {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		for {
			response, err := client.Do(request)
			if err == nil {
				data, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || response.StatusCode != 200 {
					t.Fatalf("test CA response: %v %d", err, response.StatusCode)
				}
				return data
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	directory := "https://" + api + "/dir"
	get(directory)
	signingRoot := get("https://" + management + "/roots/0")
	manager.Trust.HTTPSRoots = x509.NewCertPool()
	if !manager.Trust.HTTPSRoots.AppendCertsFromPEM(signingRoot) {
		t.Fatal("issued-certificate test root")
	}
	req := Request{ID: "https", Kind: HTTPS, Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}}
	item, err := manager.Begin(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	before, err := manager.LoadMaterial(ctx, req.ID, item.Pending)
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.ConfigurePublicACME(ctx, req.ID, PublicACMEOptions{Directory: directory, Contact: "operator@example.test", AcceptTerms: true}); err != nil {
		t.Fatal(err)
	}
	failedClient := &http.Client{Transport: &dropACMEResponse{base: transport}, Timeout: 5 * time.Second}
	if _, err = manager.RunPublicACME(ctx, req.ID, failedClient); err == nil {
		t.Fatal("lost response did not fail")
	}
	status, err := manager.PublicACMEStatus(ctx, req.ID)
	if err != nil || status.Failures != 1 || status.NextAttempt.IsZero() {
		t.Fatal("missing persistent backoff", status, err)
	}
	if _, err = manager.RunPublicACME(ctx, req.ID, client); !errors.Is(err, ErrConflict) {
		t.Fatal("retry ignored backoff", err)
	}
	offset.Store(int64(3 * time.Minute))
	manager = &Manager{Store: store, Trust: manager.Trust}
	item, err = manager.RunPublicACME(ctx, req.ID, client)
	if err != nil {
		t.Fatal(err)
	}
	if item.Active != "1" || item.Pending != "" {
		t.Fatal(item)
	}
	after, err := manager.LoadMaterial(ctx, req.ID, "")
	if err != nil || !bytes.Equal(before.Key, after.Key) || !bytes.Equal(before.CSR, after.CSR) {
		t.Fatal("restart changed pending identity", err)
	}
	pair, err := tls.X509KeyPair(after.Certificate, after.Key)
	if err != nil || pair.Leaf.VerifyHostname("localhost") != nil {
		t.Fatal("issued HTTPS identity", err)
	}
	status, err = manager.PublicACMEStatus(ctx, req.ID)
	if err != nil || status.Failures != 0 || !status.NextAttempt.IsZero() {
		t.Fatal("retry state not cleared", status, err)
	}
	// Host isolation is checked on every replica; a token cannot be replayed for
	// another requested hostname, and persisted challenges expire automatically.
	rows, err := store.List(ctx, "pki/lifecycle/http01/", "", 100)
	if err != nil || len(rows) == 0 {
		t.Fatal("no actual HTTP-01 challenge published", err)
	}
}
