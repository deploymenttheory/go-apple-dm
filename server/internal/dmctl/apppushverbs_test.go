package dmctl_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/testpki"
)

func TestAppPushAdministration(t *testing.T) {
	dir := t.TempDir()
	ca, err := testpki.NewCA("CLI app identity")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ca.IssueApp("com.example.app", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"cert.der": id.Cert.Raw, "key.pem": key,
		"registration.json": []byte(`{"token":"aabb","topic":"com.example.app","environment":"development"}`),
		"payload.json":      []byte(`{"aps":{"alert":"bench"}}`),
		"bad.json":          []byte(`{`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer operator" {
			t.Error("missing authorization")
		}
		var body map[string]json.RawMessage
		if r.Method != http.MethodGet {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
		switch r.Method + " " + r.URL.Path {
		case "PUT /admin/v1/apppush/credentials":
			var cert, private string
			_ = json.Unmarshal(body["CertPEM"], &cert)
			_ = json.Unmarshal(body["KeyPEM"], &private)
			if _, err := pushcert.ParseApp([]byte(cert), []byte(private)); err != nil {
				t.Errorf("DER identity was not normalized correctly: %v", err)
			}
		case "POST /admin/v1/apppush/send":
			if string(body["Token"]) != `"aabb"` ||
				string(body["Environment"]) != `"development"` ||
				!strings.Contains(string(body["Payload"]), `"aps"`) {
				t.Errorf("incorrect app request: %s", body["Payload"])
			}
		case "GET /admin/v1/apppush/credentials":
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Accepted":true}`))
	}))
	defer srv.Close()
	env := noConfig(t)
	invoke := func(args ...string) error {
		t.Helper()
		base := []string{"-server", srv.URL, "-token", "operator", "apppush"}
		out, stderr, err := run(t, env, append(base, args...)...)
		if strings.Contains(out+stderr, "PRIVATE KEY") {
			t.Fatal("CLI exposed the private key")
		}
		return err
	}
	send := []string{
		"send",
		"-topic",
		"com.example.app",
		"-environment",
		"development",
		"-token-file",
		filepath.Join(dir, "registration.json"),
		"-payload-file",
		filepath.Join(dir, "payload.json"),
	}
	for _, args := range [][]string{
		{"put", "-cert", filepath.Join(dir, "cert.der"), "-key", filepath.Join(dir, "key.pem")},
		{"list"},
		send,
	} {
		if err := invoke(args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{},
		{"unknown"},
		{"put"},
		{"send"},
		{"list", "-invalid"},
		{"put", "-cert", filepath.Join(dir, "missing"), "-key", filepath.Join(dir, "key.pem")},
		{"put", "-cert", filepath.Join(dir, "bad.json"), "-key", filepath.Join(dir, "key.pem")},
		append(append([]string{}, send...), "-environment", "production"),
		append(append([]string{}, send...), "-topic", "com.other.app"),
		append(append([]string{}, send...), "-payload-file", filepath.Join(dir, "bad.json")),
		append(append([]string{}, send...), "-token-file", filepath.Join(dir, "bad.json")),
	} {
		if err := invoke(args...); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	if requests.Load() != 3 {
		t.Fatal("invalid local inputs reached the server")
	}
}
