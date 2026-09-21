package bench

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestAttachExistingLiveServer checks attach existing live server.
func TestAttachExistingLiveServer(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, "live", "sqlite", "127.0.0.1:8443"); err != nil {
		t.Fatal(err)
	}
	w, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"http://127.0.0.1:8443", "https://", "://", "https://user:secret@localhost", "https://localhost/path", "https://localhost?", "https://localhost?a=b", "https://localhost#fragment"} {
		if _, err := AttachURL(w, address); err == nil {
			t.Fatal("invalid direct address accepted", address)
		}
	}
	if _, err := AttachURL(nil, "https://localhost"); err == nil {
		t.Fatal("missing workspace accepted")
	}
	e, err := AttachURL(w, "https://127.0.0.1:8443/")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Client.CloseIdleConnections()
	if e.URL != "https://127.0.0.1:8443" || e.Mode != "live" || e.Token == "" || e.ControlURL != "" {
		t.Fatal("direct attachment lost its configuration")
	}
	if e.Client.CheckRedirect == nil || !errors.Is(e.Client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("direct attachment must reject redirects")
	}
	if _, err := os.Stat(filepath.Join(dir, "running.json")); !os.IsNotExist(err) {
		t.Fatal("direct attachment wrote supervisor state")
	}
	w.Mode = "simulated"
	if _, err := AttachURL(w, e.URL); err == nil {
		t.Fatal("direct attachment accepted simulated mode")
	}
	w.Mode = "live"
	if err := os.WriteFile(filepath.Join(dir, "mdm", "admin-token"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachURL(w, e.URL); err == nil {
		t.Fatal("empty admin credential accepted")
	}
	if err := os.Remove(filepath.Join(dir, "mdm", "admin-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachURL(w, e.URL); err == nil {
		t.Fatal("missing admin credential accepted")
	}
	if err := os.Remove(filepath.Join(dir, "mdm", "ca.pem")); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachURL(w, e.URL); err == nil {
		t.Fatal("missing trust accepted")
	}
}
