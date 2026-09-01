package enroll_test

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/cms"
	"github.com/deploymenttheory/go-apple-mdm/enroll"
	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/plist"
	"github.com/deploymenttheory/go-apple-mdm/profile"
)

func signedAttrs(t *testing.T, ca *testpki.CA, cn string, attrs map[string]any) []byte {
	t.Helper()
	id, err := ca.Issue(cn, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := plist.Marshal(attrs)
	signed, err := cms.SignAttached(body, id.Cert, id.Key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestOTAProfileBuild(t *testing.T) {
	t.Parallel()
	p, err := enroll.OTAProfile{Identifier: "com.example.ota", DisplayName: "Enroll", URL: "https://mdm.example.com/ota", Challenge: "c1"}.Build()
	if err != nil {
		t.Fatal(err)
	}
	data, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := profile.Parse(data, profile.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := profile.Find[*profile.Raw](back.Profile)
	if !ok || raw.Type != enroll.PayloadTypeProfileService {
		t.Fatalf("%+v", raw)
	}
	content, _ := raw.Keys["PayloadContent"].(map[string]any)
	if content["URL"] != "https://mdm.example.com/ota" || content["Challenge"] != "c1" {
		t.Fatalf("content %v", content)
	}
	if attrs, _ := content["DeviceAttributes"].([]any); len(attrs) != len(enroll.DefaultDeviceAttributes) {
		t.Fatalf("attrs %v", content["DeviceAttributes"])
	}
	if _, err := (enroll.OTAProfile{}).Build(); !errors.Is(err, enroll.ErrOTA) {
		t.Fatal("empty")
	}
	custom, _ := enroll.OTAProfile{Identifier: "i", URL: "https://x", DeviceAttributes: []string{enroll.AttrUDID}}.Build()
	if c := custom.Payloads[0].Content.(*profile.Raw).Keys["PayloadContent"].(map[string]any); len(c["DeviceAttributes"].([]string)) != 1 || c["Challenge"] != nil {
		t.Fatalf("%v", c)
	}
}

func TestOTAPhase1Verify(t *testing.T) {
	t.Parallel()
	deviceCA, _ := testpki.NewCA("Apple iPhone Device CA (test)")
	identityCA, _ := testpki.NewCA("MDM CA (test)")
	strangerCA, _ := testpki.NewCA("stranger")
	attrs := map[string]any{"UDID": "UDID-1", "VERSION": "23A1", "PRODUCT": "iPhone17,1", "SERIAL": "S1", "IMEI": "i", "MEID": "m", "ICCID": "c", "CHALLENGE": "c1"}

	var seen []*enroll.OTARequest
	svc := &enroll.OTAService{
		DeviceRoots: deviceCA.Pool(), IdentityRoots: identityCA.Pool(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authorize: func(_ context.Context, r *enroll.OTARequest) error {
			if r.Phase == enroll.PhaseDevice && r.Attributes.Challenge != "c1" {
				return errors.New("bad challenge")
			}
			return nil
		},
		Profile: func(_ context.Context, r *enroll.OTARequest) ([]byte, error) {
			seen = append(seen, r)
			if r.Attributes.UDID == "boom" {
				return nil, errors.New("store down")
			}
			return []byte("<plist/>"), nil
		},
	}
	srv := httptest.NewServer(svc.Handler())
	defer srv.Close()
	post := func(body []byte) *http.Response {
		resp, err := srv.Client().Post(srv.URL, "application/pkcs7-signature", strings.NewReader(string(body))) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	if resp := post(signedAttrs(t, deviceCA, "device", attrs)); resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != enroll.ContentTypeProfile {
		t.Fatalf("phase 1: %d", resp.StatusCode)
	}
	if resp := post(signedAttrs(t, identityCA, "UDID-1", attrs)); resp.StatusCode != http.StatusOK {
		t.Fatalf("phase 2: %d", resp.StatusCode)
	}
	if len(seen) != 2 || seen[0].Phase != enroll.PhaseDevice || seen[1].Phase != enroll.PhaseIdentity || seen[0].Attributes.Serial != "S1" || seen[0].Attributes.IMEI != "i" || seen[0].Attributes.Raw["MEID"] != "m" || seen[1].Signer.Subject.CommonName != "UDID-1" {
		t.Fatalf("seen %+v", seen)
	}
	bad := map[string]any{"UDID": "UDID-1", "CHALLENGE": "wrong"}
	if resp := post(signedAttrs(t, deviceCA, "device", bad)); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad challenge: %d", resp.StatusCode)
	}
	if resp := post(signedAttrs(t, strangerCA, "device", attrs)); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("stranger CA: %d", resp.StatusCode)
	}
	if resp := post([]byte("garbage")); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("garbage: %d", resp.StatusCode)
	}
	if resp := post(signedAttrs(t, deviceCA, "device", map[string]any{"CHALLENGE": "c1"})); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no UDID: %d", resp.StatusCode)
	}
	if resp := post(signedAttrs(t, deviceCA, "device", map[string]any{"UDID": "boom", "CHALLENGE": "c1"})); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("profile error: %d", resp.StatusCode)
	}
	if resp := post([]byte(strings.Repeat("x", 64<<10+1))); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized: %d", resp.StatusCode)
	}
	resp, _ := srv.Client().Get(srv.URL) //nolint:noctx // test
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", resp.StatusCode)
	}

	// Signed plist that is not a dictionary.
	id, _ := deviceCA.Issue("device", time.Now().Add(-time.Minute))
	notDict, _ := plist.Marshal([]string{"x"})
	signedNotDict, _ := cms.SignAttached(notDict, id.Cert, id.Key)
	if _, err := svc.Verify(signedNotDict); !errors.Is(err, enroll.ErrOTA) {
		t.Fatalf("not dict: %v", err)
	}
	if _, err := (&enroll.OTAService{}).Verify(nil); !errors.Is(err, enroll.ErrOTA) {
		t.Fatal("no roots")
	}
	// Device-roots-only and identity-roots-only services still classify.
	if r, err := (&enroll.OTAService{DeviceRoots: deviceCA.Pool()}).Verify(signedAttrs(t, deviceCA, "d", attrs)); err != nil || r.Phase != enroll.PhaseDevice {
		t.Fatalf("device only: %v", err)
	}
	if _, err := (&enroll.OTAService{DeviceRoots: deviceCA.Pool()}).Verify(signedAttrs(t, identityCA, "d", attrs)); !errors.Is(err, enroll.ErrOTA) {
		t.Fatal("identity signer accepted by device-only service")
	}
	// Missing Profile callback and nil Authorize.
	bare := httptest.NewServer((&enroll.OTAService{DeviceRoots: deviceCA.Pool(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBytes: 1 << 20}).Handler())
	defer bare.Close()
	resp, _ = bare.Client().Post(bare.URL, "", strings.NewReader(string(signedAttrs(t, deviceCA, "d", attrs)))) //nolint:noctx // test
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("no Profile: %d", resp.StatusCode)
	}
	_ = x509.NewCertPool
	_ = pkix.Name{}
}
