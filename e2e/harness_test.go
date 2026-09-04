//go:build e2e

package e2e

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push/apns"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push/pushtest"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/v3/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/v3/server/pushnotify"
	"github.com/deploymenttheory/go-apple-dm/v3/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/testpki"
)

// otaChallenge is the Profile Service challenge the harness expects in
// phase 1.
const otaChallenge = "ota-challenge-e2e"

// pushTopic matches the simulator's default topic.
const pushTopic = "com.apple.mgmt.External.simulator"

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// harness is one running server plus the pieces tests inspect.
type harness struct {
	t      *testing.T
	ca     *testpki.CA
	store  storage.Store
	core   *service.Core
	clock  *clock.Fake
	server *httptest.Server

	// Enrollment identity issuance and push, added with the SCEP, OTA, and
	// APNs scenarios.
	scepCA     *x509.Certificate
	scepSigner *ca.Local
	challenges *scep.OneTimeChallenges
	apns       *pushtest.Server
	notifier   *pushnotify.Notifier

	mu     sync.Mutex
	events []event.Event
}

func newHarness(t *testing.T, cfg service.Config) *harness {
	t.Helper()
	return newHarnessWith(t, cfg, newStore(t), newBus())
}

// newBus is the event bus every harness records from.
func newBus() *event.Bus { return event.New() }

// newHarnessWith builds the server around a store and bus the caller made
// first, so components that need them before the core exists (the DDM
// engine) can be wired into cfg.
func newHarnessWith(t *testing.T, cfg service.Config, store storage.Store, bus *event.Bus) *harness {
	t.Helper()
	return newHarnessMounted(t, cfg, store, bus, nil)
}

// newHarnessMounted also lets a scenario mount extra routes; mount runs
// before the server starts, so handlers must read h.server.URL lazily.
func newHarnessMounted(t *testing.T, cfg service.Config, store storage.Store, bus *event.Bus, mount func(h *harness, mux *http.ServeMux)) *harness {
	t.Helper()
	testCA, err := testpki.NewCA("go-apple-dm test CA")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ca: testCA, store: store, clock: clock.NewFake(t0)}
	bus.Subscribe(event.All, func(_ context.Context, e event.Event) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.events = append(h.events, e)
		return nil
	})
	cfg.Store, cfg.Bus, cfg.Clock = h.store, bus, h.clock
	h.core, err = service.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	// SCEP CA: identities it issues are trusted for Mdm-Signature alongside
	// the pre-issued test CA.
	scepCert, scepKey, err := ca.NewSelfSigned(ca.SelfSignedOptions{Subject: pkix.Name{CommonName: "go-apple-dm SCEP CA"}})
	if err != nil {
		t.Fatal(err)
	}
	h.scepCA = scepCert
	h.scepSigner, err = ca.NewLocal(scepCert, scepKey, ca.WithDepot(ca.NewMemoryDepot()))
	if err != nil {
		t.Fatal(err)
	}
	h.challenges = scep.NewOneTimeChallenges(time.Hour, h.clock)
	scepServer, err := scep.NewServer(h.scepSigner, scepCert, scepKey, scep.WithChallenge(h.challenges), scep.WithLogger(quiet))
	if err != nil {
		t.Fatal(err)
	}
	roots := testCA.Pool()
	roots.AddCert(scepCert)

	api := httpapi.Handler(httpapi.Config{Checkin: h.core, Connect: h.core, Now: h.clock.Now})
	verify := cms.VerifyOptions{Roots: roots, ClockSkew: 5 * time.Minute, Now: time.Now}
	mux := http.NewServeMux()
	mux.Handle("/mdm", httpapi.CertFromMdmSignature(verify, 0)(api))
	mux.Handle("/scep", scepServer.Handler())
	mux.Handle("/ota", h.otaService(quiet).Handler())
	if mount != nil {
		mount(h, mux)
	}
	// Apple requires https in enrollment profiles, so the harness serves TLS.
	h.server = httptest.NewTLSServer(mux)
	t.Cleanup(h.server.Close)

	// Fake APNs behind the real client.
	h.apns = pushtest.NewServer()
	t.Cleanup(h.apns.Close)
	pushID, err := testCA.Issue(pushTopic, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	certs := push.StaticCertStore{pushTopic: tls.Certificate{Certificate: [][]byte{pushID.Cert.Raw}, PrivateKey: pushID.Key, Leaf: pushID.Cert}}
	client := apns.New(certs, apns.WithHost(h.apns.URL), apns.WithTransport(func(tls.Certificate) *http.Client { return h.apns.Client() }))
	h.notifier = &pushnotify.Notifier{Store: h.store, Pusher: client, Bus: bus, Clock: h.clock}
	return h
}

// enrollmentProfile builds the unsigned MDM enrollment profile a device
// would receive, with a one-time SCEP challenge.
func (h *harness) enrollmentProfile(udid string) []byte {
	h.t.Helper()
	challenge, err := h.challenges.Issue(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	data, err := enroll.Profile{
		Identifier: "com.example.e2e", DisplayName: "go-apple-dm e2e", Organization: "go-apple-dm",
		Topic: pushTopic, ServerURL: h.server.URL + "/mdm", CheckInURL: h.server.URL + "/mdm",
		SCEP:               &enroll.SCEP{URL: h.server.URL + "/scep", Challenge: challenge, Subject: pkix.Name{CommonName: udid, Organization: []string{"go-apple-dm"}}},
		Roots:              []*x509.Certificate{h.scepCA},
		ServerCapabilities: []string{enroll.CapabilityBootstrapToken, enroll.CapabilityToken},
	}.Marshal()
	if err != nil {
		h.t.Fatal(err)
	}
	return data
}

// otaService wires the Profile Service endpoint: the test CA plays the
// Apple iPhone Device CA for phase 1; phase 2 must be signed by the SCEP
// identity issued for the same UDID.
func (h *harness) otaService(logger *slog.Logger) *enroll.OTAService {
	identityRoots := x509.NewCertPool()
	identityRoots.AddCert(h.scepCA)
	return &enroll.OTAService{
		DeviceRoots: h.ca.Pool(), IdentityRoots: identityRoots, Logger: logger,
		Authorize: func(_ context.Context, r *enroll.OTARequest) error {
			switch r.Phase {
			case enroll.PhaseDevice:
				if r.Attributes.Challenge != otaChallenge {
					return errors.New("bad OTA challenge")
				}
			case enroll.PhaseIdentity:
				if r.Signer.Subject.CommonName != r.Attributes.UDID {
					return errors.New("phase 2 identity does not match the UDID")
				}
			}
			return nil
		},
		Profile: func(ctx context.Context, r *enroll.OTARequest) ([]byte, error) {
			if r.Phase == enroll.PhaseIdentity {
				return h.enrollmentProfile(r.Attributes.UDID), nil
			}
			challenge, err := h.challenges.Issue(ctx)
			if err != nil {
				return nil, err
			}
			p := &enroll.Profile{
				Identifier: "com.example.e2e.ota", Topic: pushTopic, ServerURL: h.server.URL + "/mdm",
				SCEP: &enroll.SCEP{URL: h.server.URL + "/scep", Challenge: challenge, Subject: pkix.Name{CommonName: r.Attributes.UDID}},
			}
			built, err := p.Build()
			if err != nil {
				return nil, err
			}
			// Phase 1 delivers only the identity payload.
			built.Payloads = built.Payloads[:1]
			return built.Marshal()
		},
	}
}

var _ = rsa.GenerateKey

// device creates a simulated device with a freshly issued identity.
func (h *harness) device(udid string) *simulator.Device {
	h.t.Helper()
	id, err := h.ca.Issue(udid, time.Now().Add(-time.Minute))
	if err != nil {
		h.t.Fatal(err)
	}
	return simulator.New(udid,
		simulator.WithURLs(h.server.URL+"/mdm", h.server.URL+"/mdm"),
		simulator.WithClient(h.server.Client()),
		simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}),
	)
}

func (h *harness) identity(cn string) *simulator.Identity {
	h.t.Helper()
	id, err := h.ca.Issue(cn, time.Now().Add(-time.Minute))
	if err != nil {
		h.t.Fatal(err)
	}
	return &simulator.Identity{Cert: id.Cert, Key: id.Key}
}

func (h *harness) eventTypes() []event.Type {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]event.Type, 0, len(h.events))
	for _, e := range h.events {
		out = append(out, e.Type)
	}
	return out
}

func deviceID(udid string) mdm.EnrollmentID {
	return mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: udid}
}
