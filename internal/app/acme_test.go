package app_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/acme/acmetest"
	"github.com/deploymenttheory/go-apple-dm/acme/attest/attesttest"
	acmeinmem "github.com/deploymenttheory/go-apple-dm/acme/inmem"
	"github.com/deploymenttheory/go-apple-dm/cms"
	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/dep/deptest"
	depinmem "github.com/deploymenttheory/go-apple-dm/dep/inmem"
	"github.com/deploymenttheory/go-apple-dm/enroll"
	"github.com/deploymenttheory/go-apple-dm/internal/app"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/profile"
	"github.com/deploymenttheory/go-apple-dm/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/simulator"
)

// acmeFixture is an enrollment fixture whose profiles carry an ACME
// identity, with a stand-in attestation authority the server trusts.
type acmeAppFixture struct {
	*enrollFixture
	attestation *attesttest.CA
}

func newACMEAppFixture(t *testing.T, mutate func(*app.Config)) *acmeAppFixture {
	t.Helper()
	return newACMEAppFixtureWith(t, func(cfg *app.Config, _ *attesttest.CA) {
		if mutate != nil {
			mutate(cfg)
		}
	})
}

// newACMEAppFixtureWith gives the caller the attestation authority as well,
// for the settings that have to name it.
func newACMEAppFixtureWith(
	t *testing.T,
	mutate func(*app.Config, *attesttest.CA),
) *acmeAppFixture {
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
			mutate(cfg, attestCA)
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
			return func(k string) string {
				if k == app.EnvStorageKeys && m[k] == "" {
					return "test"
				}
				return m[k]
			}
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

func TestACMEWiring(t *testing.T) {
	ctx := context.Background()

	t.Run("Status", func(t *testing.T) {
		for err, want := range map[error]int{
			acme.ErrNotFound:   http.StatusNotFound,
			acme.ErrConflict:   http.StatusConflict,
			acme.ErrInvalid:    http.StatusBadRequest,
			errors.New("boom"): http.StatusInternalServerError,
		} {
			if got := app.ACMEStatusForTests(err); got != want {
				t.Errorf("acmeStatus(%v) = %d, want %d", err, got, want)
			}
		}
	})

	t.Run("KeyFromFile", func(t *testing.T) {
		// An operator keeps a key in a file rather than in the process
		// environment, exactly as the SCEP HMAC key allows.
		path := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(path, []byte("a key of quite sufficient length"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := app.ACMEKeyFromEnvForTests("@" + path)
		if err != nil || string(got) != "a key of quite sufficient length" {
			t.Fatalf("key = %q %v", got, err)
		}
		if _, err := app.ACMEKeyFromEnvForTests("@" + path + ".missing"); !errors.Is(err, app.ErrConfig) {
			t.Fatalf("missing file = %v", err)
		}
		if got, err := app.ACMEKeyFromEnvForTests(""); err != nil || got != nil {
			t.Fatalf("empty = %q %v", got, err)
		}
	})

	t.Run("AnchorFile", func(t *testing.T) {
		// The anchors come from a file rather than from the configuration
		// struct, which is how a lab trusts a stand-in authority.
		var f *acmeAppFixture
		f = newACMEAppFixtureWith(t, func(cfg *app.Config, ca *attesttest.CA) {
			cfg.Enroll.ACME.Anchors = nil
			cfg.Enroll.ACME.AnchorFile = writeAnchors(t, ca)
		})
		_ = f
		// A device attesting under the authority in the file enrols.
		d := f.acmeDevice(t, "ACME-ANCHOR-1", "Mac16,1")
		if err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("BadAnchorFile", func(t *testing.T) {
		_, err := app.Build(ctx, app.Config{
			Role: app.RoleAll, Storage: "inmem", Logger: quiet,
			Enroll: app.EnrollConfig{
				PublicURL: "https://mdm.example", Topic: "com.apple.mgmt.External.x",
				Identity: app.IdentityACME,
				ACME:     app.ACMEConfig{AnchorFile: filepath.Join(t.TempDir(), "nope.pem")},
			},
		})
		if !errors.Is(err, app.ErrConfig) {
			t.Fatalf("Build = %v", err)
		}
	})

	t.Run("SQLiteStoreAndGeneratedKey", func(t *testing.T) {
		// With no identifier key configured the server generates one and
		// says so, because a generated key works for one process and fails
		// the moment a second has to verify what the first minted.
		a := build(t, app.Config{
			Role: app.RoleAll, Storage: "sqlite", DSN: t.TempDir() + "/acme.db", Logger: quiet,
			Enroll: app.EnrollConfig{
				PublicURL: "https://mdm.example", Topic: "com.apple.mgmt.External.x",
				Identity: app.IdentityACME,
			},
		})
		store := a.ACMEStoreForTests()
		if store == nil {
			t.Fatal("no ACME store")
		}
		identifier, err := a.ACMEIdentifierForTests(acme.Binding{Serial: "SER"})
		if err != nil || identifier == "" {
			t.Fatalf("identifier = %q %v", identifier, err)
		}
	})

	t.Run("DEPPolicy", func(t *testing.T) {
		// Ownership according to Apple: the attested serial has to be
		// assigned to this organisation in the device enrollment service.
		f := newACMEAppFixture(t, func(cfg *app.Config) {
			cfg.Enroll.ACME.Policy = app.ACMEPolicyDEP
		})
		unknown := f.acmeDevice(t, "ACME-DEP-1", "Mac16,1")
		err := unknown.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{})
		if err == nil || !strings.Contains(err.Error(), "unauthorized") {
			t.Fatalf("an unassigned device enrolled: %v", err)
		}
		// Once the device is in the store it enrols.
		assigned := f.acmeDevice(t, "ACME-DEP-2", "Mac16,1")
		depStore := f.app.DEPStoreForTests()
		if err := depStore.PutAccount(ctx, &dep.Account{Name: "abm"}); err != nil {
			t.Fatal(err)
		}
		err = depStore.PutDevices(
			ctx, "abm", []dep.Device{{SerialNumber: assigned.SerialNumber}}, time.Now(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := assigned.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("SIPPolicy", func(t *testing.T) {
		f := newACMEAppFixture(t, func(cfg *app.Config) {
			cfg.Enroll.ACME.Policy = app.ACMEPolicySIP
		})
		d := f.acmeDevice(t, "ACME-SIP-1", "Mac16,1")
		depStore := f.app.DEPStoreForTests()
		if err := depStore.PutAccount(ctx, &dep.Account{Name: "abm"}); err != nil {
			t.Fatal(err)
		}
		if err := depStore.PutDevices(ctx, "abm", []dep.Device{{SerialNumber: d.SerialNumber}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		// The device is assigned but reports no System Integrity Protection
		// status, and the absence of the extension is not evidence that it
		// is on.
		err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{})
		if err == nil || !strings.Contains(err.Error(), "unauthorized") {
			t.Fatalf("a device with no SIP status enrolled: %v", err)
		}
	})

	t.Run("ConfiguredKeyType", func(t *testing.T) {
		f := newACMEAppFixture(t, func(cfg *app.Config) {
			cfg.Enroll.ACME.KeyType, cfg.Enroll.ACME.KeySize = enroll.KeyTypeRSA, 2048
		})
		d := f.acmeDevice(t, "ACME-KEY-1", "Mac16,1")
		p, err := enroll.Parse(adeProfile(t, f, d), profile.ParseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		// An RSA key cannot be hardware bound, so it cannot be attested
		// either, and the payload says so rather than asking for something
		// the device would refuse.
		if p.ACME.KeyType != enroll.KeyTypeRSA || p.ACME.HardwareBound || p.ACME.Attest {
			t.Fatalf("payload = %+v", p.ACME)
		}
	})

	t.Run("CredentialForAnUnknownCertificate", func(t *testing.T) {
		f := newACMEAppFixture(t, nil)
		// A certificate this server issued, held by a device that never
		// completed check-in, is not yet an enrollment we know.
		d := f.acmeDevice(t, "ACME-UNKNOWN", "Mac16,1")
		if err := applyACME(ctx, d, adeProfile(t, f, d), f.attestation); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(
			ctx, http.MethodGet, f.publicURL+app.PathACMECredential, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
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
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("an unenrolled certificate = %d", res.StatusCode)
		}
	})
}

// writeAnchors writes an attestation root to a PEM file.
func writeAnchors(t *testing.T, ca *attesttest.CA) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "anchors.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Root.Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestACMEAdminFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	failing := &acmetest.Failing{Store: acmeinmem.New()}
	f := newACMEAppFixture(t, func(cfg *app.Config) {
		cfg.AdminToken = "t"
		cfg.Enroll.ACME.Store = failing
	})
	d := f.acmeDevice(t, "ACME-ADMIN-1", "Mac16,1")
	if err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
		t.Fatal(err)
	}
	// The listing pages, so a limit is honoured.
	var listed struct {
		Items      []struct{ Serial string }
		NextCursor string
	}
	if err := getJSON(t, f, f.publicURL+"/admin/v1/acme/certificates?limit=1", &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 {
		t.Fatalf("listed %d certificates", len(listed.Items))
	}
	// Orders for the account that enrolled.
	var orders struct{ Items []struct{ ID string } }
	res, err := f.acmeStore(t).ListCertificates(ctx, acme.CertificateQuery{}, paging.Page{})
	if err != nil {
		t.Fatal(err)
	}
	account := res.Items[0].AccountID
	if err := getJSON(t, f, f.publicURL+"/admin/v1/acme/orders?account="+account, &orders); err != nil {
		t.Fatal(err)
	}
	if len(orders.Items) != 1 {
		t.Fatalf("listed %d orders", len(orders.Items))
	}
	// A store that will not answer is a server error, not an empty list.
	for _, c := range []struct{ method, path string }{
		{"ListCertificates", "/admin/v1/acme/certificates"},
		{"ListOrders", "/admin/v1/acme/orders?account=" + account},
	} {
		failing.SetFail(map[string]error{c.method: boom})
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.publicURL+c.path, nil)
		req.Header.Set("Authorization", "Bearer t")
		res, err := f.client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusInternalServerError {
			t.Fatalf("%s failing = %d", c.method, res.StatusCode)
		}
	}
}

// acmeStore reads the ACME store behind the app.
func (f *acmeAppFixture) acmeStore(t *testing.T) acme.Store {
	t.Helper()
	s := f.app.ACMEStoreForTests()
	if s == nil {
		t.Fatal("no ACME store")
	}
	return s
}

func TestACMEPolicyFaultIsNotARefusal(t *testing.T) {
	// A lookup that fails is a fault on our side, not a device that was
	// turned away, so the challenge stays pending and the device can try
	// again once the store is well. Settling it invalid would lock a
	// legitimate device out until someone reissued its profile.
	ctx := context.Background()
	failing := &deptest.Failing{Store: depinmem.New()}
	f := newACMEAppFixture(t, func(cfg *app.Config) {
		cfg.Enroll.ACME.Policy = app.ACMEPolicyDEP
		cfg.DEP.Store = failing
	})
	d := f.acmeDevice(t, "ACME-FAULT-1", "Mac16,1")
	if err := failing.PutAccount(ctx, &dep.Account{Name: "abm"}); err != nil {
		t.Fatal(err)
	}
	err := failing.PutDevices(ctx, "abm", []dep.Device{{SerialNumber: d.SerialNumber}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	failing.SetFail(map[string]error{"ListAccounts": errors.New("the directory is down")})
	// The client retries a server error, so the attempt is bounded here
	// rather than left to give up on its own.
	attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := d.ADEEnroll(attempt, f.publicURL+app.PathADE, simulator.ADEOptions{}); err == nil {
		t.Fatal("the device enrolled while the lookup was failing")
	}
	// Nothing was issued, and nothing was settled against the device.
	res, err := f.acmeStore(t).ListCertificates(ctx, acme.CertificateQuery{}, paging.Page{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 {
		t.Fatalf("issued %d certificates during an outage", len(res.Items))
	}
	// Once the store answers again the same device enrols, which is the
	// point: the refusal was never recorded.
	failing.SetFail(nil)
	if err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestACMEIdentifierKeyFallsBackToSCEP(t *testing.T) {
	// Both are the same kind of secret held by the same server, so a
	// deployment that configured one has said what it means to.
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", Logger: quiet,
		Enroll: app.EnrollConfig{
			PublicURL: "https://mdm.example", Topic: "com.apple.mgmt.External.x",
			Identity:    app.IdentityACME,
			SCEPHMACKey: []byte("a SCEP key of quite sufficient length"),
		},
	})
	identifier, err := a.ACMEIdentifierForTests(acme.Binding{Serial: "SER"})
	if err != nil || identifier == "" {
		t.Fatalf("identifier = %q %v", identifier, err)
	}
}

func TestACMEEnvKeys(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string {
			if k == app.EnvStorageKeys && m[k] == "" {
				return "test"
			}
			return m[k]
		}
	}
	base := map[string]string{
		app.EnvPublicURL:   "https://mdm.example",
		app.EnvPushTopic:   "com.apple.mgmt.External.x",
		app.EnvIdentity:    app.IdentityACME,
		app.EnvSCEPHMACKey: "a SCEP key of quite sufficient length",
	}
	for name, want := range map[string]struct {
		Type string
		Size int64
	}{
		"ec256":   {enroll.KeyTypeEC, 256},
		"ec384":   {enroll.KeyTypeEC, 384},
		"rsa2048": {enroll.KeyTypeRSA, 2048},
		"rsa4096": {enroll.KeyTypeRSA, 4096},
	} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		m[app.EnvACMEKey] = name
		cfg, err := app.ParseEnv(env(m))
		if err != nil {
			t.Fatalf("%s = %v", name, err)
		}
		if cfg.Enroll.ACME.KeyType != want.Type || cfg.Enroll.ACME.KeySize != want.Size {
			t.Fatalf("%s = %s/%d", name, cfg.Enroll.ACME.KeyType, cfg.Enroll.ACME.KeySize)
		}
		// The SCEP key is read from the same environment.
		if string(cfg.Enroll.SCEPHMACKey) != base[app.EnvSCEPHMACKey] {
			t.Fatalf("SCEP key = %q", cfg.Enroll.SCEPHMACKey)
		}
	}
	// A key file that is not there is a configuration error, not a silent
	// fall back to a generated key.
	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	m[app.EnvACMEHMACKey] = "@" + filepath.Join(t.TempDir(), "absent")
	if _, err := app.ParseEnv(env(m)); !errors.Is(err, app.ErrConfig) {
		t.Fatalf("missing key file = %v", err)
	}
}

func TestACMEDEPPolicyNeedsTheDEPStore(t *testing.T) {
	// The mdm role runs no device enrollment service, so a policy that asks
	// it about every device could never say yes. Saying so at startup is
	// better than answering every enrolment with a server error, which is
	// what the client would then retry until it gave up.
	_, err := app.Build(context.Background(), app.Config{
		Role: app.RoleMDM, Storage: "inmem", Logger: quiet,
		Enroll: app.EnrollConfig{
			PublicURL: "https://mdm.example", Topic: "com.apple.mgmt.External.x",
			Identity: app.IdentityACME,
			ACME:     app.ACMEConfig{Policy: app.ACMEPolicyDEP},
		},
	})
	if !errors.Is(err, app.ErrConfig) {
		t.Fatalf("Build = %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "device enrollment service") {
		t.Fatalf("error does not say why: %v", err)
	}
}
