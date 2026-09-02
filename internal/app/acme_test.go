package app_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/acme/attest/attesttest"
	"github.com/deploymenttheory/go-apple-mdm/cms"
	"github.com/deploymenttheory/go-apple-mdm/enroll"
	"github.com/deploymenttheory/go-apple-mdm/internal/app"
	"github.com/deploymenttheory/go-apple-mdm/profile"
	"github.com/deploymenttheory/go-apple-mdm/schema/ddm"
	"github.com/deploymenttheory/go-apple-mdm/simulator"
)

// acmeFixture is an enrollment fixture whose profiles carry an ACME
// identity, with a stand-in attestation authority the server trusts.
type acmeAppFixture struct {
	*enrollFixture
	attestation *attesttest.CA
}

func newACMEAppFixture(t *testing.T, mutate func(*app.Config)) *acmeAppFixture {
	t.Helper()
	attestCA, err := attesttest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	f := &acmeAppFixture{attestation: attestCA}
	f.enrollFixture = newEnrollFixture(t, "", func(cfg *app.Config) {
		cfg.Enroll.Identity = app.IdentityACME
		cfg.Enroll.ACME.Anchors = attestCA.Anchors()
		cfg.Enroll.ACME.HMACKey = []byte("an app identifier key of ample length")
		if mutate != nil {
			mutate(cfg)
		}
	})
	return f
}

func TestACME(t *testing.T) {
	ctx := context.Background()

	t.Run("ProfileBindsTheDevice", func(t *testing.T) {
		// The client identifier in an automated enrollment profile carries
		// the serial number and UDID the server expects, so intercepting
		// one does not let another device use it. step-ca instead requires
		// the identifier to be the serial number, which is printed on the
		// case.
		f := newACMEAppFixture(t, nil)
		d := f.acmeDevice(t, "ACME-APP-1", "Mac16,1")
		data := adeProfile(t, f, d)
		p, err := enroll.Parse(data, profile.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if p.ACME == nil {
			t.Fatalf("the profile has no ACME payload: %+v", p)
		}
		if p.SCEP != nil {
			t.Fatal("the profile carries a SCEP payload as well")
		}
		if !p.ACME.Attest || !p.ACME.HardwareBound || p.ACME.KeyType != enroll.KeyTypeEC {
			t.Fatalf("payload = %+v", p.ACME)
		}
		if !strings.HasPrefix(p.ACME.DirectoryURL, f.publicURL+app.PathACME) {
			t.Fatalf("directory = %q", p.ACME.DirectoryURL)
		}
		// The identifier is opaque, and specifically is not the serial
		// number.
		if strings.Contains(p.ACME.ClientIdentifier, d.SerialNumber) {
			t.Fatalf("the client identifier leaks the serial number: %q", p.ACME.ClientIdentifier)
		}
		// A device that presents this profile and attests to the right
		// hardware enrols.
		d2 := f.acmeDevice(t, "ACME-APP-1b", "Mac16,1")
		d2.SerialNumber, d2.UDID = d.SerialNumber, d.UDID
		if err := applyACME(ctx, d2, data, f.attestation); err != nil {
			t.Fatal(err)
		}
		if d2.Identity == nil {
			t.Fatal("the device has no identity")
		}
		// A different device presenting an identifier issued for this one
		// does not. It needs its own profile, because the first identifier
		// was spent by the enrollment above.
		other := f.acmeDevice(t, "ACME-APP-1c", "Mac16,1")
		other.SerialNumber = "NOT-THIS-ONE"
		err = applyACME(ctx, other, adeProfile(t, f, d), f.attestation)
		if err == nil || !strings.Contains(err.Error(), "badAttestationStatement") {
			t.Fatalf("another device enrolled with the identifier: %v", err)
		}
	})

	t.Run("SCEPRemainsTheDefault", func(t *testing.T) {
		// Moving to ACME is a decision an operator takes when the hardware
		// and the anchors are ready, so an unset identity is still SCEP.
		f := &acmeAppFixture{enrollFixture: newEnrollFixture(t, "", nil)}
		d := f.device(t, "SCEP-APP-1", "Mac16,1")
		p, err := enroll.Parse(adeProfile(t, f, d), profile.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if p.SCEP == nil || p.ACME != nil {
			t.Fatalf("identity = %+v", p)
		}
		// The ACME endpoints are still mounted, because a declarative
		// credential can use them while profiles carry SCEP.
		res, err := f.client().Get(f.publicURL + app.PathACME + "/directory")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("directory = %d", res.StatusCode)
		}
	})

	t.Run("CredentialRequiresDeviceIdentity", func(t *testing.T) {
		// The credential document contains a client identifier, which Apple
		// calls an anti-replay code, so a URL is not enough to fetch one.
		f := newACMEAppFixture(t, nil)
		res, err := f.client().Get(f.publicURL + app.PathACMECredential)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("without a device identity = %d", res.StatusCode)
		}
		// An enrolled device gets a credential bound to itself.
		d := f.acmeDevice(t, "ACME-APP-2", "Mac16,1")
		if err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := d.Authenticate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := d.TokenUpdate(ctx); err != nil {
			t.Fatal(err)
		}
		credential := fetchCredential(t, f, d)
		if credential.DirectoryURL == "" || credential.ClientIdentifier == "" {
			t.Fatalf("credential = %+v", credential)
		}
		if credential.Attest == nil || !*credential.Attest || !credential.HardwareBound {
			t.Fatalf("credential does not ask for an attestation: %+v", credential)
		}
		// The identifier is fresh each time, so a second declaration gets a
		// second one-time code rather than reusing a spent one.
		again := fetchCredential(t, f, d)
		if again.ClientIdentifier == credential.ClientIdentifier {
			t.Fatal("the credential reused a client identifier")
		}
	})

	t.Run("AdminListsCertificatesByDevice", func(t *testing.T) {
		// What Apple attested is stored with the certificate, so an
		// operator can ask which hardware holds an identity without
		// trusting a subject name.
		f := newACMEAppFixture(t, func(cfg *app.Config) { cfg.AdminToken = "t" })
		d := f.acmeDevice(t, "ACME-APP-3", "Mac16,1")
		if err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
			t.Fatal(err)
		}
		var listed struct {
			Items []struct {
				Serial string
				Device struct {
					SerialNumber string
					UDID         string
				}
			}
		}
		admin := f.publicURL + "/admin/v1/acme/certificates?serial=" + d.SerialNumber
		if err := getJSON(t, f, admin, &listed); err != nil {
			t.Fatal(err)
		}
		if len(listed.Items) != 1 {
			t.Fatalf("listed %d certificates", len(listed.Items))
		}
		if listed.Items[0].Device.SerialNumber != d.SerialNumber {
			t.Fatalf("device = %+v", listed.Items[0].Device)
		}
		// A serial that issued nothing lists nothing.
		listed.Items = nil
		if err := getJSON(t, f, f.publicURL+"/admin/v1/acme/certificates?serial=NOBODY", &listed); err != nil {
			t.Fatal(err)
		}
		if len(listed.Items) != 0 {
			t.Fatalf("listed %d certificates for an unknown serial", len(listed.Items))
		}
		// The orders endpoint needs an account.
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.publicURL+"/admin/v1/acme/orders", nil)
		req.Header.Set("Authorization", "Bearer t")
		res, err := f.client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("orders without an account = %d", res.StatusCode)
		}
	})

	t.Run("Env", func(t *testing.T) {
		env := func(m map[string]string) func(string) string {
			return func(k string) string { return m[k] }
		}
		cfg, err := app.ParseEnv(env(map[string]string{
			app.EnvIdentity:       app.IdentityACME,
			app.EnvACMEPolicy:     app.ACMEPolicyDEP,
			app.EnvACMEKey:        "ec256",
			app.EnvACMEHMACKey:    "a key of quite sufficient length",
			app.EnvACMEUnattested: "true",
			app.EnvACMEIdentTTL:   "30m",
		}))
		if err != nil {
			t.Fatal(err)
		}
		a := cfg.Enroll.ACME
		if cfg.Enroll.Identity != app.IdentityACME || a.Policy != app.ACMEPolicyDEP ||
			a.KeyType != enroll.KeyTypeEC || a.KeySize != 256 ||
			!a.AllowUnattested || a.IdentifierTTL != 30*time.Minute ||
			string(a.HMACKey) != "a key of quite sufficient length" {
			t.Fatalf("config = %+v %q", a, cfg.Enroll.Identity)
		}
		enabled := func(extra map[string]string) map[string]string {
			m := map[string]string{
				app.EnvPublicURL:     "https://mdm.example",
				app.EnvPushTopic:     "com.apple.mgmt.External.x",
				app.EnvSCEPChallenge: "secret",
			}
			for k, v := range extra {
				m[k] = v
			}
			return m
		}
		for _, bad := range []map[string]string{
			{app.EnvACMEKey: "ed25519"},
			{app.EnvACMEIdentTTL: "soon"},
			{app.EnvACMEUnattested: "maybe"},
			{app.EnvIdentity: "pkcs12"},
		} {
			if _, err := app.ParseEnv(env(enabled(bad))); !errors.Is(err, app.ErrConfig) {
				t.Fatalf("%v = %v", bad, err)
			}
		}
		// An ACME identity needs no SCEP challenge, where a SCEP one does.
		for _, c := range []struct {
			env  map[string]string
			want bool
		}{
			{map[string]string{app.EnvIdentity: app.IdentityACME}, true},
			{map[string]string{app.EnvIdentity: app.IdentitySCEP}, false},
		} {
			m := map[string]string{
				app.EnvPublicURL: "https://mdm.example",
				app.EnvPushTopic: "com.apple.mgmt.External.x",
			}
			for k, v := range c.env {
				m[k] = v
			}
			_, err := app.ParseEnv(env(m))
			_ = err
			if (err == nil) != c.want {
				t.Fatalf("%v = %v", c.env, err)
			}
		}
	})

	t.Run("BadPolicy", func(t *testing.T) {
		_, err := app.Build(ctx, app.Config{
			Role: app.RoleAll, Storage: "inmem", Logger: quiet,
			Enroll: app.EnrollConfig{
				PublicURL: "https://mdm.example", Topic: "com.apple.mgmt.External.x",
				Identity: app.IdentityACME,
				ACME:     app.ACMEConfig{Policy: "whatever"},
			},
		})
		if !errors.Is(err, app.ErrConfig) {
			t.Fatalf("Build = %v", err)
		}
	})
}

// device builds a simulated device that attests under the fixture's
// authority, so applying an ACME profile completes.
func (f *acmeAppFixture) acmeDevice(t *testing.T, udid, product string) *simulator.Device {
	t.Helper()
	d := f.device(t, udid, product)
	simulator.WithACME(simulator.ACMEOptions{Attestation: f.attestation})(d)
	return d
}

// adeProfile fetches the automated enrollment profile without applying it,
// so a test can look at what the server offered.
func adeProfile(t *testing.T, f *acmeAppFixture, d *simulator.Device) []byte {
	t.Helper()
	signed, err := d.SignedMachineInfo(simulator.ADEOptions{})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost,
		f.publicURL+app.PathADE, bytes.NewReader(signed),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", simulator.ContentTypeDeviceInfo)
	res, err := f.client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ADE profile = %d %s", res.StatusCode, data)
	}
	return data
}

// applyACME installs a profile whose identity comes from ACME, with the
// device attesting under the given authority.
func applyACME(
	ctx context.Context,
	d *simulator.Device,
	data []byte,
	attestation *attesttest.CA,
) error {
	p, err := enroll.Parse(data, profile.ParseOptions{})
	if err != nil {
		return err
	}
	return d.ACMEEnroll(ctx, p.ACME, simulator.ACMEOptions{Attestation: attestation})
}

// fetchCredential reads the declarative ACME credential as the device,
// presenting its identity the way an asset with MDM authentication does.
func fetchCredential(t *testing.T, f *acmeAppFixture, d *simulator.Device) ddm.ACMECredential {
	t.Helper()
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, f.publicURL+app.PathACMECredential, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	// An asset with MDM authentication is fetched with the device identity,
	// which on this deployment arrives as a detached signature over the
	// body exactly as a check-in does.
	signature, err := cms.Sign(nil, d.Identity.Cert, d.Identity.Key)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(cms.HeaderName, cms.EncodeHeader(signature))
	res, err := f.client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("credential = %d %s", res.StatusCode, data)
	}
	var credential ddm.ACMECredential
	if err := json.Unmarshal(data, &credential); err != nil {
		t.Fatal(err)
	}
	return credential
}

func getJSON(t *testing.T, f *acmeAppFixture, url string, v any) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer t")
	res, err := f.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s = %d %s", url, res.StatusCode, data)
	}
	return json.Unmarshal(data, v)
}
