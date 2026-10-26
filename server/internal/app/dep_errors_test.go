package app_test

import (
	"bytes"
	"crypto/x509"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	depinmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/dep/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// syncBuffer collects log output written by handler goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends one log record.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns the collected records.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestDEPUpstreamFailuresAreReported checks that an Apple rejection during token
// import names Apple's status and code without its body, while other server
// failures keep the fixed message; both are logged.
func TestDEPUpstreamFailuresAreReported(t *testing.T) {
	fake := deptest.NewServer(deptest.Options{})
	t.Cleanup(fake.Close)
	store := &deptest.Failing{Store: depinmem.New()}
	logs := &syncBuffer{}
	a := build(t, app.Config{
		Storage:        "inmem",
		BootstrapToken: "t",
		Logger:         slog.New(slog.NewTextHandler(logs, nil)),
		DEP:            app.DEPConfig{BaseURL: fake.URL(), HTTPClient: fake.Client(), Store: store},
	})
	srv := serve(t, a)

	res := do(t, srv, http.MethodPut, "/admin/v1/dep/accounts/abm/keypair", "t", nil)
	certPEM, _ := io.ReadAll(res.Body)
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatalf("keypair = %d %s", res.StatusCode, certPEM)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	p7m, err := fake.TokenP7M(cert)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, want string
		script     deptest.Scripted
	}{
		{"code", "Apple device enrollment service returned HTTP 400 OAUTH_PROBLEM", deptest.Scripted{Status: http.StatusBadRequest, Code: "OAUTH_PROBLEM"}},
		{"no code", "Apple device enrollment service returned HTTP 503", deptest.Scripted{Status: http.StatusServiceUnavailable, Body: "upstream private detail"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake.Script("/session", tc.script)
			var body struct{ Error string }
			decode(t, do(t, srv, http.MethodPut, "/admin/v1/dep/accounts/abm/token", "t", p7m), http.StatusBadGateway, &body)
			if body.Error != tc.want {
				t.Fatalf("error = %q, want %q", body.Error, tc.want)
			}
		})
	}

	store.SetFail(map[string]error{"ListAccounts": errors.New("private store detail")})
	res = do(t, srv, http.MethodGet, "/admin/v1/dep/accounts", "t", nil)
	raw, _ := io.ReadAll(res.Body)
	var body struct{ Error string }
	if err := json.Unmarshal(raw, &body); err != nil || res.StatusCode != http.StatusInternalServerError || body.Error != "internal error" {
		t.Fatalf("store failure = %d %s", res.StatusCode, raw)
	}

	out := logs.String()
	for _, want := range []string{"DEP administration failed", "HTTP 400 OAUTH_PROBLEM", "private store detail"} {
		if !strings.Contains(out, want) {
			t.Errorf("logs lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "upstream private detail") {
		t.Error("logs included Apple's response body")
	}
}
