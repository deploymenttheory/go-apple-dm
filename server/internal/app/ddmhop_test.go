package app_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestDDMHopNeedsACredential holds both ends of the declarative management hop
// to an authenticated channel. The hop forwards a check-in verbatim and the
// ddm role resolves the enrollment from that body, so without a credential any
// caller reaching the listener names any enrollment and reads its declarations
// or writes its status reports.

// TestCertHeaderIsVerifiedAgainstCARoots holds the proxy header source to the
// enrollment CA when one is configured. A device certificate is not secret, so
// a header alone lets anyone who reaches the listener past the proxy present
// another device's identity; the chain narrows that to certificates the CA
// issued.
func TestCertHeaderIsVerifiedAgainstCARoots(t *testing.T) {
	t.Parallel()
	ours, err := testpki.NewCA("ours")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := testpki.NewCA("theirs")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := theirs.Issue("UDID-1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	mine, err := ours.Issue("UDID-1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	header := func(c *x509.Certificate) string {
		return ":" + base64.StdEncoding.EncodeToString(c.Raw) + ":"
	}

	a := build(t, app.Config{
		Storage:        "inmem",
		CertHeader:     "X-Client-Cert",
		CARoots:        ours.Pool(),
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	})
	srv := serve(t, a)
	checkin := func(t *testing.T, cert *x509.Certificate) int {
		t.Helper()
		raw, err := plist.Marshal(map[string]any{
			"MessageType": "Authenticate", "Topic": "com.apple.mgmt.t", "UDID": "UDID-1",
			"Model": "Mac", "ModelName": "MacBook", "DeviceName": "d", "SerialNumber": "S1",
		})
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(
			context.Background(), http.MethodPut, srv.URL+"/mdm", bytes.NewReader(raw),
		)
		req.Header.Set("Content-Type", "application/x-apple-aspen-mdm-checkin")
		req.Header.Set("X-Client-Cert", header(cert))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func(body io.Closer) { _ = body.Close() }(res.Body)
		return res.StatusCode
	}

	if got := checkin(t, foreign.Cert); got != http.StatusBadRequest {
		t.Fatalf("certificate from another CA = %d, want 400", got)
	}
	if got := checkin(t, mine.Cert); got != http.StatusOK {
		t.Fatalf("certificate from our CA = %d, want 200", got)
	}
}
