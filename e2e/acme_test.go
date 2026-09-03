//go:build e2e

package e2e

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/acme/attest/attesttest"
	acmeinmem "github.com/deploymenttheory/go-apple-dm/acme/inmem"
	"github.com/deploymenttheory/go-apple-dm/ca"
	"github.com/deploymenttheory/go-apple-dm/enroll"
	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/profile"
	"github.com/deploymenttheory/go-apple-dm/service"
	"github.com/deploymenttheory/go-apple-dm/simulator"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// acmeFixture is a harness with an ACME server mounted on the enrollment
// certificate authority, so an identity it issues is trusted for
// Mdm-Signature exactly as a SCEP one is.
type acmeFixture struct {
	*harness
	acme        acme.Store
	attestation *attesttest.CA
	identifiers *acme.HMACIdentifiers
	server      *acme.Server
	bus         *event.Bus
}

func newACMEFixture(t *testing.T, mutate ...func(*acme.Config)) *acmeFixture {
	t.Helper()
	attestCA, err := attesttest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	f := &acmeFixture{acme: acmeinmem.New(), attestation: attestCA}
	var once sync.Once
	var handler http.Handler
	// The server needs the base URL, which does not exist until the test
	// server is listening, so it is built on the first request.
	build := func() {
		// The ACME server runs on real time here, not the harness's fake
		// clock: an attestation certificate has a real validity window, and
		// a server verifying it a year in the simulated past would reject a
		// perfectly good chain.
		f.identifiers, err = acme.NewHMACIdentifiers(
			[]byte("an e2e identifier key of ample length"), time.Hour, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		cfg := acme.Config{
			BaseURL:     f.server1URL(),
			Store:       f.acme,
			Signer:      f.scepSigner,
			CAPolicy:    ca.Policy{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
			Identifiers: f.identifiers,
			Anchors:     attestCA.Anchors(),
			Bus:         f.bus,
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		for _, m := range mutate {
			m(&cfg)
		}
		srv, err := acme.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		f.server = srv
		handler = srv.Handler()
	}
	bus := newBus()
	f.harness = newHarnessMounted(
		t, service.Config{}, newStore(t), bus,
		func(h *harness, mux *http.ServeMux) {
			mux.Handle("/acme/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				once.Do(build)
				handler.ServeHTTP(w, r)
			}))
		},
	)
	f.bus = bus
	return f
}

func (f *acmeFixture) server1URL() string { return f.harness.server.URL }

// profile builds an enrollment profile whose identity comes from ACME,
// with a client identifier minted for one device.
func (f *acmeFixture) profile(t *testing.T, udid string, b acme.Binding) []byte {
	t.Helper()
	// Touch the server so the lazily built configuration exists.
	f.ensure(t)
	identifier, err := f.identifiers.Issue(b)
	if err != nil {
		t.Fatal(err)
	}
	data, err := enroll.Profile{
		Identifier: "com.example.e2e", DisplayName: "go-apple-dm e2e", Organization: "go-apple-dm",
		Topic: pushTopic, ServerURL: f.harness.server.URL + "/mdm",
		CheckInURL: f.harness.server.URL + "/mdm",
		ACME: &enroll.ACME{
			DirectoryURL:     f.harness.server.URL + "/acme/directory",
			ClientIdentifier: identifier,
			KeyType:          enroll.KeyTypeEC,
			KeySize:          384,
			HardwareBound:    true,
			Attest:           true,
			Subject:          pkix.Name{CommonName: udid},
		},
		Roots:              []*x509.Certificate{f.scepCA},
		ServerCapabilities: []string{enroll.CapabilityBootstrapToken, enroll.CapabilityToken},
	}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// ensure makes the lazily built server exist.
func (f *acmeFixture) ensure(t *testing.T) {
	t.Helper()
	if f.server != nil {
		return
	}
	res, err := f.harness.server.Client().Get(f.harness.server.URL + "/acme/directory")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if f.server == nil {
		t.Fatal("the ACME server was not built")
	}
}

// TestE2E_ACMEAttest is E2E-014: a device enrols with an identity issued
// through ACME against an attestation, and every way of getting the
// attestation wrong is refused.
func TestE2E_ACMEAttest(t *testing.T) {
	ctx := context.Background()
	t.Run("Enrols", func(t *testing.T) {
		f := newACMEFixture(t)
		const udid, serial = "ACME-UDID-1", "C02ACME0001"
		data := f.profile(t, udid, acme.Binding{Serial: serial, UDID: udid, CommonName: serial})
		d := simulator.New(udid,
			simulator.WithClient(f.harness.server.Client()),
			simulator.WithACME(simulator.ACMEOptions{Attestation: f.attestation}),
		)
		d.SerialNumber = serial
		if err := d.ApplyProfile(ctx, data, profile.ParseOptions{}); err != nil {
			t.Fatal(err)
		}
		// The identity the device now holds came from ACME and chains to
		// the enrollment authority, so it authenticates check-in.
		if d.Identity == nil {
			t.Fatal("the device has no identity")
		}
		id, ok, err := ca.ParsePermanentIdentifier(d.Identity.Cert)
		if err != nil || !ok {
			t.Fatalf("permanent identifier = %v %v", ok, err)
		}
		if id == "" {
			t.Fatal("the certificate carries an empty permanent identifier")
		}
		// The subject is the server's statement about the device, not one
		// the certificate request asked for.
		if d.Identity.Cert.Subject.CommonName != serial {
			t.Fatalf("subject = %q, want the bound serial %q", d.Identity.Cert.Subject.CommonName, serial)
		}
		if err := d.Authenticate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := d.TokenUpdate(ctx); err != nil {
			t.Fatal(err)
		}
		issued := 0
		for _, tp := range f.eventTypes() {
			if tp == event.ACMEIssued {
				issued++
			}
		}
		if issued != 1 {
			t.Fatalf("%d issuance events in %v", issued, f.eventTypes())
		}
		// What Apple attested is kept with the certificate, so an operator
		// can ask which hardware holds an identity.
		res, err := f.acme.ListCertificates(ctx, acme.CertificateQuery{}, storage.Page{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 1 || res.Items[0].Device.SerialNumber != serial {
			t.Fatalf("stored certificates = %+v", res.Items)
		}
	})

	// Each of these is a real attack or a real fault, and each must be
	// refused. The reference implementations accept some of them: nanoca
	// and Fleet do not compare the certificate request's key with the
	// attested key, and step-ca skips the freshness check when the leaf
	// carries no freshness extension.
	refusals := []struct {
		name    string
		options func(f *acmeFixture) simulator.ACMEOptions
		binding func(serial, udid string) acme.Binding
		want    string
	}{
		{
			name: "ForeignAttestationAuthority",
			options: func(f *acmeFixture) simulator.ACMEOptions {
				other, err := attesttest.NewCA()
				if err != nil {
					t.Fatal(err)
				}
				return simulator.ACMEOptions{
					Attestation: f.attestation,
					Faults:      simulator.ACMEFaults{ForeignCA: other},
				}
			},
			want: "badAttestationStatement",
		},
		{
			name: "AttestationFromAnotherChallenge",
			options: func(f *acmeFixture) simulator.ACMEOptions {
				return simulator.ACMEOptions{
					Attestation: f.attestation,
					Faults:      simulator.ACMEFaults{StaleFreshness: true},
				}
			},
			want: "badAttestationStatement",
		},
		{
			name: "AttestsOneKeyAndAsksForAnother",
			options: func(f *acmeFixture) simulator.ACMEOptions {
				return simulator.ACMEOptions{
					Attestation: f.attestation,
					Faults:      simulator.ACMEFaults{WrongKey: true},
				}
			},
			want: "badCSR",
		},
		{
			name: "NoAttestation",
			options: func(f *acmeFixture) simulator.ACMEOptions {
				return simulator.ACMEOptions{}
			},
			want: "badAttestationStatement",
		},
		{
			name: "AttestationNamesAnotherDevice",
			options: func(f *acmeFixture) simulator.ACMEOptions {
				return simulator.ACMEOptions{
					Attestation: f.attestation,
					Properties:  attest.Properties{SerialNumber: "SOMEONE-ELSE"},
				}
			},
			binding: func(serial, udid string) acme.Binding {
				return acme.Binding{Serial: serial, CommonName: serial}
			},
			want: "badAttestationStatement",
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			f := newACMEFixture(t)
			const udid, serial = "ACME-UDID-2", "C02ACME0002"
			b := acme.Binding{Serial: serial, UDID: udid, CommonName: serial}
			if tc.binding != nil {
				b = tc.binding(serial, udid)
			}
			data := f.profile(t, udid, b)
			d := simulator.New(udid,
				simulator.WithClient(f.harness.server.Client()),
				simulator.WithACME(tc.options(f)),
			)
			d.SerialNumber = serial
			err := d.ApplyProfile(ctx, data, profile.ParseOptions{})
			if err == nil {
				t.Fatal("the device enrolled with a bad attestation")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want something about %s", err, tc.want)
			}
			if d.Identity != nil {
				t.Fatal("the device kept an identity it should not have")
			}
		})
	}

	t.Run("ClientIdentifierBuysOneCertificate", func(t *testing.T) {
		// Apple calls the ClientIdentifier an anti-replay code. Neither
		// nanoca nor step-ca consumes it, so on those servers one
		// identifier buys any number of certificates.
		f := newACMEFixture(t)
		const udid, serial = "ACME-UDID-3", "C02ACME0003"
		data := f.profile(t, udid, acme.Binding{Serial: serial, UDID: udid, CommonName: serial})
		first := simulator.New(udid,
			simulator.WithClient(f.harness.server.Client()),
			simulator.WithACME(simulator.ACMEOptions{Attestation: f.attestation}),
		)
		first.SerialNumber = serial
		if err := first.ApplyProfile(ctx, data, profile.ParseOptions{}); err != nil {
			t.Fatal(err)
		}
		second := simulator.New(udid,
			simulator.WithClient(f.harness.server.Client()),
			simulator.WithACME(simulator.ACMEOptions{Attestation: f.attestation}),
		)
		second.SerialNumber = serial
		err := second.ApplyProfile(ctx, data, profile.ParseOptions{})
		if err == nil {
			t.Fatal("the same client identifier was used twice")
		}
		if !strings.Contains(err.Error(), "rejectedIdentifier") {
			t.Fatalf("error = %v, want rejectedIdentifier", err)
		}
	})

	t.Run("PolicyRefusesADeviceThatIsNotOurs", func(t *testing.T) {
		f := newACMEFixture(t, func(cfg *acme.Config) {
			cfg.Authorize = acme.AllowSerials("SOME-OTHER-MAC")
		})
		const udid, serial = "ACME-UDID-4", "C02ACME0004"
		data := f.profile(t, udid, acme.Binding{Serial: serial, UDID: udid, CommonName: serial})
		d := simulator.New(udid,
			simulator.WithClient(f.harness.server.Client()),
			simulator.WithACME(simulator.ACMEOptions{Attestation: f.attestation}),
		)
		d.SerialNumber = serial
		err := d.ApplyProfile(ctx, data, profile.ParseOptions{})
		if err == nil || !strings.Contains(err.Error(), "unauthorized") {
			t.Fatalf("error = %v, want unauthorized", err)
		}
	})

	t.Run("UnattestedDeviceWhenAllowed", func(t *testing.T) {
		// Hardware that cannot attest still enrols where a deployment has
		// said so, on the strength of the client identifier alone.
		f := newACMEFixture(t, func(cfg *acme.Config) { cfg.AllowUnattested = true })
		const udid, serial = "ACME-UDID-5", "C02ACME0005"
		data := f.profile(t, udid, acme.Binding{CommonName: serial, AllowUnidentified: true})
		d := simulator.New(udid, simulator.WithClient(f.harness.server.Client()))
		d.SerialNumber = serial
		if err := d.ApplyProfile(ctx, data, profile.ParseOptions{}); err != nil {
			t.Fatal(err)
		}
		if d.Identity == nil {
			t.Fatal("the device has no identity")
		}
	})
}

// TestE2E_DeviceAttestation is E2E-023: the same verifier reads the
// attestation a device returns to a DeviceInformation query.
func TestE2E_DeviceAttestation(t *testing.T) {
	ctx := context.Background()
	f := newACMEFixture(t)
	const udid, serial = "ATTEST-UDID-1", "C02ATTEST01"
	data := f.profile(t, udid, acme.Binding{Serial: serial, UDID: udid, CommonName: serial})
	d := simulator.New(udid,
		simulator.WithClient(f.harness.server.Client()),
		simulator.WithACME(simulator.ACMEOptions{Attestation: f.attestation}),
	)
	d.SerialNumber = serial
	if err := d.ApplyProfile(ctx, data, profile.ParseOptions{}); err != nil {
		t.Fatal(err)
	}
	nonce := []byte("a freshness code of our choosing")
	chain, err := d.DevicePropertiesAttestation(nonce)
	if err != nil {
		t.Fatal(err)
	}
	a, err := attest.ParseChain(chain)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Verify(attest.VerifyOptions{
		Anchors:   f.attestation.Anchors(),
		Freshness: nonce,
	}); err != nil {
		t.Fatal(err)
	}
	if a.Properties.SerialNumber != serial || a.Properties.UDID != udid {
		t.Fatalf("properties = %+v", a.Properties)
	}

	// Apple's device caches its attestation for seven days and returns the
	// cached one whatever freshness code was asked for, so a server on this
	// path cannot read a mismatch as a replay.
	other := []byte("a different freshness code....")
	cached, err := d.DevicePropertiesAttestation(other)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := attest.ParseChain(cached)
	if err != nil {
		t.Fatal(err)
	}
	if string(stale.Properties.Freshness) != string(nonce) {
		t.Fatal("the device minted a fresh attestation inside the cache window")
	}
	if err := stale.Verify(attest.VerifyOptions{
		Anchors:   f.attestation.Anchors(),
		Freshness: other,
	}); !errors.Is(err, attest.ErrFreshness) {
		t.Fatalf("Verify = %v, want ErrFreshness", err)
	}

	// Once the window has passed the device mints a new one.
	d.ExpireAttestationCache()
	fresh, err := d.DevicePropertiesAttestation(other)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := attest.ParseChain(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if err := renewed.Verify(attest.VerifyOptions{
		Anchors:   f.attestation.Anchors(),
		Freshness: other,
	}); err != nil {
		t.Fatal(err)
	}

	// A tampered chain still parses, because a signature is not checked
	// until the path is built, and then it fails.
	tampered := [][]byte{append([]byte(nil), fresh[0]...), fresh[1]}
	tampered[0][len(tampered[0])-1] ^= 0xff
	bad, err := attest.ParseChain(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Verify(attest.VerifyOptions{
		Anchors:   f.attestation.Anchors(),
		Freshness: other,
	}); !errors.Is(err, attest.ErrChain) {
		t.Fatalf("Verify = %v, want ErrChain", err)
	}
}
