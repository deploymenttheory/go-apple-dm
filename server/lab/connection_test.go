package lab

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestConnectUsesExistingTrustAndCredential proves attachment creates no workspace
// state, rejects redirects and releases resources without stopping the server.
func TestConnectUsesExistingTrustAndCredential(t *testing.T) {
	redirect := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirect {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		if r.URL.Path != "/admin/v1/auth/me" || r.Header.Get("Authorization") != "Bearer managed" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"Name":"lab"}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	token := filepath.Join(dir, "operator")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("managed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Connection{URL: srv.URL, CAFile: ca, TokenFile: token, EvidenceDirectory: dir}
	e, err := Connect(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	if _, err = os.Stat(filepath.Join(dir, WorkspaceFile)); !os.IsNotExist(err) {
		t.Fatal("attachment wrote a workspace")
	}
	redirect = true
	if e, err = Connect(t.Context(), cfg); err == nil {
		e.Close()
		t.Fatal("redirect accepted as credential verification")
	}
	for _, origin := range []string{"http://lab.invalid", "https://user:secret@lab.invalid", "https://lab.invalid/path", "https://lab.invalid?", "https://lab.invalid/#x"} {
		cfg.URL = origin
		if _, err = Connect(context.Background(), cfg); err == nil {
			t.Fatalf("accepted %s", origin)
		}
	}
	cfg.URL = srv.URL
	cfg.CAFile = filepath.Join(dir, "absent")
	if _, err = Connect(t.Context(), cfg); err == nil {
		t.Fatal("missing CA accepted")
	}
	cfg.CAFile = token
	if _, err = Connect(t.Context(), cfg); err == nil {
		t.Fatal("invalid CA accepted")
	}
	cfg.CAFile = ca
	cfg.TokenFile = filepath.Join(dir, "absent")
	if _, err = Connect(t.Context(), cfg); err == nil {
		t.Fatal("missing token accepted")
	}
	cfg.TokenFile = token
	if err = os.WriteFile(token, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = Connect(t.Context(), cfg); err == nil {
		t.Fatal("empty token accepted")
	}
}

// TestSelectFromExtendsThePublicCatalogue preserves custom IDs and rejects typos.
func TestSelectFromExtendsThePublicCatalogue(t *testing.T) {
	modules := []Module{{ID: "LAB-custom", Theme: "custom"}}
	if got, err := SelectFrom(modules, "custom"); err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := SelectFrom(modules, "unknown"); err == nil {
		t.Fatal("unknown selection accepted")
	}
}
