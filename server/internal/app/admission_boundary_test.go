package app

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

type admissionInventory struct {
	dep.Store
	account               *dep.Account
	device                *dep.StoredDevice
	accountErr, deviceErr error
}

func (s admissionInventory) GetAccount(context.Context, string) (*dep.Account, error) {
	return s.account, s.accountErr
}

func (s admissionInventory) GetDevice(context.Context, string, string) (*dep.StoredDevice, error) {
	return s.device, s.deviceErr
}

func TestAdmissionInventoryFailuresDenyIssuance(t *testing.T) {
	a := &App{cfg: Config{Clock: clock.Real{}}}
	file := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(
		file,
		[]byte(`{"devices":[{}],"depAccounts":["fleet"]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	e := &enrollment{cfg: EnrollConfig{AdmissionFile: file}}
	policy, err := a.enrollmentAdmission(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = policy(
		t.Context(),
		AdmissionRequest{Serial: "SER"},
	); !errors.Is(
		err,
		errDEPAdmissionUnavailable,
	) {
		t.Fatal(err)
	}
	fault := errors.New("inventory database unavailable")
	valid := admissionInventory{
		account: &dep.Account{ProfileUUID: "assigned"},
		device: &dep.StoredDevice{
			Device: dep.Device{ProfileUUID: "assigned", ProfileStatus: dep.ProfileStatusAssigned},
		},
	}
	for _, stage := range []string{"account absent", "account failure", "device absent", "device failure", "unassigned", "allowed"} {
		t.Run(stage, func(t *testing.T) {
			inventory := valid
			switch stage {
			case "account absent":
				inventory.accountErr = dep.ErrNotFound
			case "account failure":
				inventory.accountErr = fault
			case "device absent":
				inventory.deviceErr = dep.ErrNotFound
			case "device failure":
				inventory.deviceErr = fault
			case "unassigned":
				inventory.device = &dep.StoredDevice{}
			}
			a.dep = &depService{store: inventory}
			_, err := policy(t.Context(), AdmissionRequest{Serial: "SER"})
			if stage == "allowed" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("inventory failure admitted")
			}
			if strings.HasSuffix(stage, "failure") && !errors.Is(err, fault) {
				t.Fatal("failure classified as ownership denial", err)
			}
		})
	}
	e.cfg.AdmissionFile = filepath.Join(t.TempDir(), "absent")
	if _, err = a.enrollmentAdmission(e); err == nil {
		t.Fatal("missing policy ignored")
	}
}

func TestAccountAdmissionRequiresExplicitStableMapping(t *testing.T) {
	request := AdmissionRequest{
		Issuer:        "https://idp",
		Subject:       "subject",
		Email:         "email@example.com",
		EmailVerified: true,
	}
	for _, rule := range []AccountAdmissionRule{
		{Issuer: "https://idp", ManagedAppleAccount: "managed"},
		{Issuer: "https://idp", Subject: "different", ManagedAppleAccount: "managed"},
		{Issuer: "https://other", Subject: "subject", ManagedAppleAccount: "managed"},
		{Issuer: "https://idp", Subject: "subject"},
	} {
		if rule.matches(request) {
			t.Fatal("incomplete/wrong account mapping matched")
		}
	}
	rule := AccountAdmissionRule{
		Issuer:              "https://idp",
		Subject:             "subject",
		ManagedAppleAccount: "managed",
	}
	if !rule.matches(request) {
		t.Fatal("stable mapping rejected")
	}
	mapped := accountAdmission(
		accountdriven.Identity{
			Issuer:  request.Issuer,
			Subject: request.Subject,
			Claims:  map[string]any{"groups": []string{"enrollers"}},
		},
	)
	if len(mapped.Groups) != 1 {
		t.Fatal(mapped)
	}
	e := &enrollment{now: time.Now}
	if _, err := e.admit(
		t.Context(),
		acme.Binding{UDID: "device"},
	); !errors.Is(
		err,
		webauth.ErrDenied,
	) {
		t.Fatal(err)
	}
	if _, err := e.admit(
		t.Context(),
		acme.Binding{CommonName: accountdriven.CertificateSubjectPrefix + "missing"},
	); !errors.Is(
		err,
		webauth.ErrDenied,
	) {
		t.Fatal(err)
	}
	for _, expires := range []time.Time{{}, time.Now().Add(-time.Minute)} {
		e.admission = func(context.Context, AdmissionRequest) (AdmissionGrant, error) {
			return AdmissionGrant{ExpiresAt: expires}, nil
		}
		if _, err := e.admit(
			t.Context(),
			acme.Binding{UDID: "device"},
		); !errors.Is(
			err,
			webauth.ErrDenied,
		) {
			t.Fatal("expired admission grant accepted", err)
		}
	}
}

type issuanceStateFault struct {
	state.Store
	readErr, txReadErr, writeErr error
	txValue                      []byte
}

func (s issuanceStateFault) Get(ctx context.Context, k string) (state.Record, error) {
	if s.readErr != nil {
		return state.Record{}, s.readErr
	}
	return s.Store.Get(ctx, k)
}

func (s issuanceStateFault) Update(
	ctx context.Context,
	keys []string,
	fn func(state.Tx) error,
) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error {
		return fn(
			issuanceTxFault{Tx: tx, readErr: s.txReadErr, writeErr: s.writeErr, value: s.txValue},
		)
	})
}

type issuanceTxFault struct {
	state.Tx
	readErr, writeErr error
	value             []byte
}

func (t issuanceTxFault) Get(ctx context.Context, k string) (state.Record, error) {
	if t.readErr != nil {
		return state.Record{}, t.readErr
	}
	r, err := t.Tx.Get(ctx, k)
	if t.value != nil {
		r.Value = t.value
	}
	return r, err
}

func (t issuanceTxFault) Put(ctx context.Context, r state.Record) error {
	if t.writeErr != nil {
		return t.writeErr
	}
	return t.Tx.Put(ctx, r)
}

func TestIssuanceFailsClosedOnStateFaults(t *testing.T) {
	backend := state.NewMemory()
	cert, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: Config{Clock: clock.Real{}}, protocol: backend}
	e := &enrollment{
		app:    a,
		state:  backend,
		now:    time.Now,
		caCert: cert,
		caKey:  key,
		admission: func(context.Context, AdmissionRequest) (AdmissionGrant, error) {
			return AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
	}
	a.enroll = e
	e.depot = &enrollmentDepot{app: a, Depot: ca.NewMemoryDepot()}
	csr := hardeningCSR(t, "device")
	password, err := e.issueSCEPGrant(
		t.Context(),
		acme.Binding{CommonName: "device", UDID: "device"},
		AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.verifySCEPGrant(t.Context(), password, csr); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("issuance persistence unavailable")
	for _, st := range []state.Store{issuanceStateFault{Store: backend, readErr: fault}, issuanceStateFault{Store: backend, txReadErr: fault}, issuanceStateFault{Store: backend, writeErr: fault}} {
		e.state = st
		if err = e.verifySCEPGrant(t.Context(), password, csr); !errors.Is(err, fault) {
			t.Fatal(err)
		}
	}
	e.state = issuanceStateFault{Store: backend, writeErr: fault}
	if _, err = e.issueSCEPGrant(
		t.Context(),
		acme.Binding{},
		AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)},
	); !errors.Is(
		err,
		fault,
	) {
		t.Fatal(err)
	}
	e.state = backend
	for _, input := range []struct {
		password string
		csr      *x509.CertificateRequest
	}{{"", csr}, {password, nil}, {"absent", csr}, {password, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "another"}, Raw: csr.Raw}}} {
		if err = e.verifySCEPGrant(t.Context(), input.password, input.csr); err == nil {
			t.Fatal("invalid challenge accepted")
		}
	}
	grantKey := scepGrantKey(password)
	if err = backend.Update(t.Context(), []string{grantKey}, func(tx state.Tx) error {
		return tx.Put(t.Context(), state.Record{Key: grantKey, Value: []byte("corrupt grant")})
	}); err != nil {
		t.Fatal(err)
	}
	if err = e.verifySCEPGrant(t.Context(), password, csr); err == nil {
		t.Fatal("corrupt grant accepted")
	}
	if _, err = e.issueSCEP(t.Context(), csr, ca.Policy{}, password, false); err == nil {
		t.Fatal("corrupt grant issued certificate")
	}
	if _, err = e.issueSCEP(t.Context(), csr, ca.Policy{}, "absent", false); err == nil {
		t.Fatal("missing grant issued certificate")
	}
	if _, err = e.issueSCEP(t.Context(), csr, ca.Policy{}, "", true); err == nil {
		t.Fatal("renewal without verified signer accepted")
	}
}

func TestAuthenticateRequiresMatchingIssuanceEvidence(t *testing.T) {
	backend := state.NewMemory()
	a := &App{protocol: backend}
	hook := issuanceAdmission{app: a}
	cert := &x509.Certificate{
		Subject: pkix.Name{CommonName: "identity"},
		Raw:     []byte("certificate"),
	}
	key := "issued-identity:" + cms.Fingerprint(cert)
	call := &service.Call{
		Op: "checkin:Authenticate",
		Request: &mdm.Request{
			Certificate: cert,
			ID:          mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"},
		},
		Checkin: &mdm.Checkin{Message: &checkin.Authenticate{SerialNumber: new("serial")}},
	}
	for _, evidence := range []identityEvidence{{}, {UDID: "another"}, {Serial: "different"}, {EnrollmentID: "other"}, {UDID: "device", Serial: "serial"}} {
		raw, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if err = backend.Update(
			t.Context(),
			[]string{key},
			func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: key, Value: raw}) },
		); err != nil {
			t.Fatal(err)
		}
		_, err = hook.Before(t.Context(), call)
		if evidence.UDID == "device" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("unbound/mismatched identity authenticated", evidence)
		}
	}
	if err := backend.Update(
		t.Context(),
		[]string{key},
		func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: key, Value: []byte("corrupt")}) },
	); err != nil {
		t.Fatal(err)
	}
	if _, err := hook.Before(t.Context(), call); err == nil {
		t.Fatal("corrupt evidence authenticated")
	}
}

func TestAdminHandlersHandlePostAuthorizationStorageFailure(t *testing.T) {
	fault := errors.New("database unavailable with secret detail")
	store := &storagetest.Failing{
		Store: inmem.New(),
		Fail: map[string]error{
			"Get":      fault,
			"Disable":  fault,
			"Commands": fault,
			"Clear":    fault,
			"PushInfo": fault,
		},
	}
	core, err := service.New(service.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		cfg:   Config{Clock: clock.Real{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		Store: store,
		Core:  core,
	}
	for name, h := range map[string]http.HandlerFunc{"get": a.getEnrollment, "disable": a.disableEnrollment, "commands": a.listCommands, "clear": a.clearCommands, "evidence": a.enrollmentEvidence} {
		t.Run(name, func(t *testing.T) {
			for _, valid := range []bool{false, true} {
				r := httptest.NewRequest("GET", "https://admin.example/resource", nil)
				if valid {
					r.SetPathValue("id", "device")
					r.SetPathValue("channel", "device")
				}
				w := httptest.NewRecorder()
				h(w, r)
				want := http.StatusBadRequest
				if valid {
					want = http.StatusInternalServerError
				}
				if w.Code != want || strings.Contains(w.Body.String(), "secret detail") {
					t.Fatal(w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestSecurityStartupRejectsMissingTrustAndPersistentIssuer(t *testing.T) {
	for _, cfg := range []Config{
		{Role: RoleAll, Storage: "sqlite", Enroll: EnrollConfig{PublicURL: "https://mdm.example", Topic: "com.apple.mgmt.test"}},
		{Role: RoleAll, Storage: "inmem", CertHeader: "Client-Cert"},
		{Role: RoleAll, Storage: "inmem", CertHeader: "Client-Cert", TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}},
		{Role: RoleAll, Storage: "inmem", Enroll: EnrollConfig{PublicURL: "https://mdm.example", Topic: "com.apple.mgmt.test", AdmissionFile: "absent-policy"}},
	} {
		a, err := Build(t.Context(), cfg)
		if a != nil {
			_ = a.Close()
		}
		if err == nil {
			t.Fatal("incomplete security configuration started")
		}
	}
}

func TestAdmissionEventsExcludeCredentialsAndSurviveSinkFailure(t *testing.T) {
	bus := event.New()
	var events []event.Event
	bus.Subscribe(event.All, func(_ context.Context, e event.Event) error {
		events = append(events, e)
		return errors.New("sink failed")
	})
	a := &App{
		cfg: Config{
			Clock:  clock.Real{},
			Bus:    bus,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	}
	e := &enrollment{
		app: a,
		now: time.Now,
		admission: func(context.Context, AdmissionRequest) (AdmissionGrant, error) {
			return AdmissionGrant{}, webauth.ErrDenied
		},
	}
	if _, err := e.admit(
		t.Context(),
		acme.Binding{UDID: "private-device", Serial: "private-serial"},
	); !errors.Is(
		err,
		webauth.ErrDenied,
	) {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != event.EnrollmentDenied || events[0].Data != nil ||
		events[0].Enrollment.ID != "" {
		t.Fatal("security event exposed identity data", events)
	}
}
