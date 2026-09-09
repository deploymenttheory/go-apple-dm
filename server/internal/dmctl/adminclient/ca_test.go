package adminclient_test

import (
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/adminclient"
)

func TestExplicitServerTrust(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer operator" {
			t.Error("missing operator credential")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	dir := t.TempDir()
	file := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(
		file,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	c, err := adminclient.New(adminclient.Config{BaseURL: srv.URL, Token: "operator", CAFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(t.Context(), http.MethodGet, "/config", nil, nil); err != nil {
		t.Fatalf("explicit trust did not work: %v", err)
	}
	if err := os.WriteFile(file, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{file, filepath.Join(dir, "missing.pem")} {
		if _, err := adminclient.New(
			adminclient.Config{BaseURL: srv.URL, CAFile: invalid},
		); !errors.Is(
			err,
			adminclient.ErrConfig,
		) {
			t.Fatalf("invalid trust file accepted: %v", err)
		}
	}
	// An explicit transport remains the caller's TLS policy.
	if _, err := adminclient.New(
		adminclient.Config{BaseURL: srv.URL, CAFile: file, HTTPClient: srv.Client()},
	); err != nil {
		t.Fatalf("caller transport was overridden: %v", err)
	}
}
