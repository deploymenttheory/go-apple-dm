package bench

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/profile"
)

func TestEnrollmentReplacementCatalogue(t *testing.T) {
	for _, id := range []string{"E2E-027", "E2E-028", "E2E-029", "E2E-030", "E2E-031"} {
		t.Run(id, func(t *testing.T) {
			scenarios, _ := Select(id)
			s := scenarios[0]
			w := testWorkspace(t, "simulated")
			w.Settings = s.Settings
			e, err := Start(t.Context(), w, "", io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			transport := e.Client.Transport
			e.Client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				r, err := transport.RoundTrip(req)
				if err == nil && r.StatusCode >= 400 {
					b, _ := io.ReadAll(r.Body)
					r.Body.Close()
					r.Body = io.NopCloser(bytes.NewReader(b))
					t.Logf("fixture HTTP %s %d: %s", req.URL.Path, r.StatusCode, b)
				}
				return r, err
			})
			if err := s.Run(t.Context(), e, ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEnrollmentPreflightAndTrustExport(t *testing.T) {
	w := testWorkspace(t, "live")
	if ready, _ := EnrollmentPreflight(w, "acme")["Ready"].(bool); ready {
		t.Fatal("missing MDM certificate passed")
	}
	w.Mode = "simulated"
	if ready, _ := EnrollmentPreflight(w, "scep")["Ready"].(bool); !ready {
		t.Fatal(EnrollmentPreflight(w, "scep"))
	}
	if ready, _ := EnrollmentPreflight(w, "invalid")["Ready"].(bool); ready {
		t.Fatal("invalid method passed")
	}
	file := filepath.Join(t.TempDir(), "trust.mobileconfig")
	if err := ExportTrust(w, file); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	p, err := profile.Parse(b, profile.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Profile.Payloads) != 1 ||
		p.Profile.Payloads[0].Content.PayloadTypeName() != "com.apple.security.root" {
		t.Fatal("trust profile contents")
	}
	if err := ExportTrust(w, file); err == nil {
		t.Fatal("existing profile overwritten")
	}
	if err := os.WriteFile(w.path("mdm", "ca.pem"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExportTrust(w, filepath.Join(t.TempDir(), "bad.mobileconfig")); err == nil {
		t.Fatal("broken certificate exported")
	}
	if ready, _ := EnrollmentPreflight(w, "scep")["Ready"].(bool); ready {
		t.Fatal("broken trust passed")
	}
}
