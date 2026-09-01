package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
	"github.com/deploymenttheory/go-apple-mdm/schema/commands"
	"github.com/deploymenttheory/go-apple-mdm/service"
	"github.com/deploymenttheory/go-apple-mdm/storage"
	"github.com/deploymenttheory/go-apple-mdm/storage/inmem"
	"github.com/deploymenttheory/go-apple-mdm/storage/storagetest"
)

var errDB = errors.New("database on fire")

// TestStorageFailuresAreInternal drives every storage call the service makes
// through a failing store and checks each surfaces as CodeInternal.
func TestStorageFailuresAreInternal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ca, err := testpki.NewCA("t")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := ca.Issue("d", t0.Add(-1))
	failing := &storagetest.Failing{Store: inmem.New(), Fail: map[string]error{}}
	core, err := service.New(service.Config{Store: failing, Clock: clock.NewFake(t0)})
	if err != nil {
		t.Fatal(err)
	}
	// Healthy enrollment first.
	if _, err := core.Checkin(ctx, req(id.Cert), authenticate(t, "D1")); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Checkin(ctx, req(id.Cert), tokenUpdate(t, "D1", nil)); err != nil {
		t.Fatal(err)
	}
	lock, _ := mdm.NewCommand(&commands.DeviceLock{}, mdm.WithUUID("C1"))
	dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	if _, err := core.Enqueue(ctx, []mdm.EnrollmentID{dev}, lock, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method string
		run    func() error
	}{
		{"Get", func() error { _, err := core.Checkin(ctx, req(id.Cert), authenticate(t, "D1")); return err }},
		{"UpsertAuthenticate", func() error { _, err := core.Checkin(ctx, req(id.Cert), authenticate(t, "D1")); return err }},
		{"AssociateCert", func() error { _, err := core.Checkin(ctx, req(id.Cert), authenticate(t, "D1")); return err }},
		{"CertHash", func() error {
			_, err := core.Connect(ctx, req(id.Cert), response("D1", "", mdm.StatusIdle))
			return err
		}},
		{"StoreTokenUpdate", func() error { _, err := core.Checkin(ctx, req(id.Cert), tokenUpdate(t, "D1", nil)); return err }},
		{"Disable", func() error { _, err := core.Checkin(ctx, req(id.Cert), simple(t, "CheckOut", "D1", nil)); return err }},
		{"StoreBootstrapToken", func() error {
			_, err := core.Checkin(ctx, req(id.Cert), simple(t, "SetBootstrapToken", "D1", map[string]any{"BootstrapToken": []byte("x")}))
			return err
		}},
		{"BootstrapToken", func() error {
			_, err := core.Checkin(ctx, req(id.Cert), simple(t, "GetBootstrapToken", "D1", nil))
			return err
		}},
		{"StoreResult", func() error {
			_, err := core.Connect(ctx, req(id.Cert), response("D1", "C1", mdm.StatusAcknowledged))
			return err
		}},
		{"Next", func() error {
			_, err := core.Connect(ctx, req(id.Cert), response("D1", "", mdm.StatusIdle))
			return err
		}},
		{"TouchLastSeen", func() error {
			_, err := core.Connect(ctx, req(id.Cert), response("D1", "", mdm.StatusIdle))
			return err
		}},
		{"Enqueue", func() error {
			_, err := core.Enqueue(ctx, []mdm.EnrollmentID{dev}, lock, storage.EnqueueOptions{})
			return err
		}},
	}
	for _, c := range cases {
		failing.Fail = map[string]error{c.method: errDB}
		err := c.run()
		if service.CodeOf(err) != service.CodeInternal || !errors.Is(err, errDB) {
			t.Errorf("%s failure: code=%d err=%v", c.method, service.CodeOf(err), err)
		}
	}
	// Get failing on a user-channel TokenUpdate and AssociateCert failing on
	// retroactive pinning.
	failing.Fail = map[string]error{}
	if _, err := core.Checkin(ctx, req(id.Cert), tokenUpdate(t, "D1", map[string]any{"UserID": "U"})); err != nil {
		t.Fatal(err)
	}
	failing.Fail = map[string]error{"Get": errDB}
	if err := errors.Unwrap(func() error {
		_, err := core.Checkin(ctx, req(id.Cert), tokenUpdate(t, "D1", map[string]any{"UserID": "U2"}))
		return err
	}()); !errors.Is(err, errDB) {
		t.Errorf("user channel Get failure: %v", err)
	}
	retro := &storagetest.Failing{Store: inmem.New(), Fail: map[string]error{}}
	core2, _ := service.New(service.Config{Store: retro, Pinning: service.PinWarn, Clock: clock.NewFake(t0)})
	if _, err := core2.Checkin(ctx, req(nil), authenticate(t, "D1")); err != nil {
		t.Fatal(err)
	}
	retro.Fail = map[string]error{"AssociateCert": errDB}
	if _, err := core2.Checkin(ctx, req(id.Cert), tokenUpdate(t, "D1", nil)); service.CodeOf(err) != service.CodeInternal {
		t.Errorf("retroactive AssociateCert failure: %v", err)
	}
	// A storage.ErrInvalid from the store maps to CodeBadRequest.
	retro.Fail = map[string]error{"UpsertAuthenticate": storage.ErrInvalid}
	if _, err := core2.Checkin(ctx, req(nil), authenticate(t, "D2")); service.CodeOf(err) != service.CodeBadRequest {
		t.Errorf("ErrInvalid mapping: %v", err)
	}
}

func TestEventHandlerFailureIsLogged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bus := event.New()
	bus.Subscribe(event.All, func(context.Context, event.Event) error { return errors.New("subscriber broke") })
	core, err := service.New(service.Config{Store: inmem.New(), Bus: bus, Pinning: service.PinOff, Clock: clock.NewFake(t0)})
	if err != nil {
		t.Fatal(err)
	}
	// The operation still succeeds; the failure is logged, not propagated.
	if _, err := core.Checkin(ctx, req(nil), authenticate(t, "D1")); err != nil {
		t.Fatalf("subscriber failure leaked: %v", err)
	}
}

// TestAuthorizeGuardsEveryMessage sends every post-enrollment message with a
// certificate that is not the pinned one and expects a refusal, including
// on the command channel with a vetoing hook.
func TestAuthorizeGuardsEveryMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	getTokenErr := errors.New("token service down")
	hook := &recordingHook{veto: "connect"}
	h := newHarness(t, service.Config{
		Hooks: []service.Hook{hook},
		GetToken: func(context.Context, *mdm.Request, *checkin.GetToken) (*checkin.GetTokenResponse, error) {
			return nil, getTokenErr
		},
		DeclarativeManagement: func(context.Context, *mdm.Request, *checkin.DeclarativeManagement) (service.DMResponse, error) {
			return service.DMResponse{}, nil
		},
	})
	enroll(t, h, "D1")
	messages := map[string]*mdm.Checkin{
		"GetToken":              simple(t, "GetToken", "D1", map[string]any{"TokenServiceType": "com.apple.maid"}),
		"UserAuthenticate":      checkinPlist(t, map[string]any{"MessageType": "UserAuthenticate", "UDID": "D1", "UserID": "U1", "DigestResponse": ""}),
		"DeclarativeManagement": simple(t, "DeclarativeManagement", "D1", map[string]any{"Endpoint": "tokens"}),
		"GetBootstrapToken":     simple(t, "GetBootstrapToken", "D1", nil),
		"SetBootstrapToken":     simple(t, "SetBootstrapToken", "D1", map[string]any{"BootstrapToken": []byte("x")}),
		"CheckOut":              simple(t, "CheckOut", "D1", nil),
	}
	for name, ck := range messages {
		if _, err := h.core.Checkin(ctx, req(h.cert2), ck); !errors.Is(err, service.ErrCertMismatch) {
			t.Errorf("%s with wrong certificate: %v", name, err)
		}
	}
	// Handler failure surfaces with its own code (plain errors are internal).
	if _, err := h.core.Checkin(ctx, req(h.cert), messages["GetToken"]); !errors.Is(err, getTokenErr) || service.CodeOf(err) != service.CodeInternal {
		t.Errorf("GetToken handler error: %v", err)
	}
	// Hook veto on the command channel.
	if _, err := h.core.Connect(ctx, req(h.cert), response("D1", "", mdm.StatusIdle)); !errors.Is(err, service.ErrHookVeto) {
		t.Errorf("connect veto: %v", err)
	}
}
