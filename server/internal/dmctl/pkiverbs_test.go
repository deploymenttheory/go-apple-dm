package dmctl_test

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

func TestCertificateAdministration(t *testing.T) {
	if _, _, err := run(t, noConfig(t), "certificates", "status", "-h"); err != nil {
		t.Fatal("help requires server configuration", err)
	}
	path := filepath.Join(t.TempDir(), "leaf.der")
	if err := os.WriteFile(path, []byte("certificate DER"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeAdmin{}
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reuse the request recorder; the PKI response is supplied here.
		recorder := httptest.NewRecorder()
		f.serve(recorder, r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"Status":"issued"}`))
	}))
	defer srv.Close()
	env := noConfig(t)
	env["DMCTL_SERVER"], env["DMCTL_TOKEN"] = srv.URL, "tok"
	for _, tc := range []struct {
		args               []string
		method, path, body string
	}{
		{[]string{"status", "issuer", "abcd"}, "GET", "/admin/v1/pki/certificates/issuer/abcd", ""},
		{[]string{"revoke", "issuer", "abcd", "-reason", "1"}, "POST", "/admin/v1/pki/certificates/issuer/abcd/revoke", `"reason":1`},
		{[]string{"import", "issuer", "-file", path}, "POST", "/admin/v1/pki/certificates/import", base64.StdEncoding.EncodeToString([]byte("certificate DER"))},
	} {
		if _, _, err := run(t, env, append([]string{"certificates"}, tc.args...)...); err != nil {
			t.Fatal(err)
		}
		got := f.last()
		if got.method != tc.method || got.path != tc.path || !strings.Contains(got.body, tc.body) {
			t.Fatalf("request: %+v", got)
		}
	}
	for _, args := range [][]string{nil, {"bogus"}, {"status"}, {"revoke", "issuer"}, {"import", "issuer"}, {"status", "-bogus"}} {
		if _, _, err := run(
			t,
			env,
			append([]string{"certificates"}, args...)...); !errors.Is(
			err,
			dmctl.ErrUsage,
		) {
			t.Fatal(args, err)
		}
	}
	if _, _, err := run(
		t,
		env,
		"certificates",
		"import",
		"issuer",
		"-file",
		"/no/such/cert",
	); err == nil {
		t.Fatal("missing file accepted")
	}
	status = 500
	if _, _, err := run(t, env, "certificates", "status", "issuer", "abcd"); err == nil {
		t.Fatal("server error hidden")
	}
	delete(env, "DMCTL_SERVER")
	if _, _, err := run(t, env, "certificates", "status", "issuer", "abcd"); err == nil {
		t.Fatal("missing server accepted")
	}
}
