package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func TestProfileProxyAuthenticatesBeforeFetchingAndHandlesUpstreamFailures(t *testing.T) {
	ca, err := testpki.NewCA("profile proxy")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ca.Issue("device", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Build(t.Context(), Config{Storage: "inmem", CARoots: ca.Pool()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, CertHash: cms.Fingerprint(identity.Cert)}}); err != nil {
		t.Fatal(err)
	}
	const revision = "profile-revision"
	for _, tc := range []struct {
		name, query                string
		certificate                bool
		upstreamStatus, wantStatus int
		upstreamError              error
		wantCalls                  int
	}{
		{name: "success", query: "channel=device&id=device", certificate: true, upstreamStatus: 200, wantStatus: 200, wantCalls: 1},
		{name: "upstream-missing", query: "channel=device&id=device", certificate: true, upstreamStatus: 404, wantStatus: 404, wantCalls: 1},
		{name: "upstream-error", query: "channel=device&id=device", certificate: true, upstreamError: errors.New("private upstream details"), wantStatus: 503, wantCalls: 1},
		{name: "invalid-channel", query: "channel=invalid&id=device", certificate: true, wantStatus: 404},
		{name: "missing-certificate", query: "channel=device&id=device", wantStatus: 403},
		{name: "wrong-device", query: "channel=device&id=other", certificate: true, wantStatus: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			mux := http.NewServeMux()
			a.wireConfigurationProfileDownloads(mux, func(_ context.Context, got mdm.EnrollmentID, rev string) (service.DMResponse, error) {
				calls++
				if got != id || rev != revision {
					t.Fatalf("proxy target changed: %+v %q", got, rev)
				}
				return service.DMResponse{Status: tc.upstreamStatus, Body: []byte("profile bytes"), ContentType: "application/x-apple-aspen-config"}, tc.upstreamError
			})
			r := httptest.NewRequestWithContext(t.Context(), "GET", "https://mdm.example"+configurationprofile.Path+revision+"?"+tc.query, nil)
			if tc.certificate {
				r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{identity.Cert}, VerifiedChains: [][]*x509.Certificate{{identity.Cert, ca.Cert}}}
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.wantStatus || calls != tc.wantCalls {
				t.Fatalf("HTTP %d, upstream calls %d: %s", w.Code, calls, w.Body.String())
			}
			if tc.wantStatus == 200 {
				if w.Body.String() != "profile bytes" || w.Header().Get("Content-Type") != "application/x-apple-aspen-config" || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("profile response changed", w.Header(), w.Body.String())
				}
			} else if strings.Contains(w.Body.String(), "profile bytes") || strings.Contains(w.Body.String(), "private upstream") {
				t.Fatal("failed download disclosed upstream data")
			}
		})
	}
}
