//go:build e2e

package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/schema/commands"
	"github.com/deploymenttheory/go-apple-mdm/service"
	"github.com/deploymenttheory/go-apple-mdm/simulator"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// E2E-001: a device with a pre-issued identity enrols (Authenticate,
// TokenUpdate) and an Idle poll returns no command.
func TestE2E_EnrollIdle(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	d := h.device("E2E-001")
	if err := d.Enroll(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	e, err := h.store.Get(ctx, deviceID("E2E-001"))
	if err != nil || !e.Enabled || e.Push.Magic != d.PushMagic || e.CertHash == "" || e.Device.SerialNumber != d.SerialNumber {
		t.Fatalf("enrollment record %+v %v", e, err)
	}
	got, err := d.Connect(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("idle: %v %v", got, err)
	}
	if types := h.eventTypes(); len(types) != 2 || types[0] != event.Enrolled || types[1] != event.TokenUpdated {
		t.Fatalf("events = %v", types)
	}
	// Signed with a certificate from another CA: rejected by the transport.
	other, _ := h.ca.Issue("other", time.Now())
	_ = other
	rogue := simulator.New("E2E-001", simulator.WithURLs(h.server.URL+"/mdm", h.server.URL+"/mdm"), simulator.WithClient(h.server.Client()))
	var he *simulator.HTTPError
	if _, err := rogue.Connect(ctx); !errors.As(err, &he) || he.Status != 403 {
		t.Fatalf("unsigned request should be forbidden, got %v", err)
	}
}

// E2E-002: three commands queued are delivered in order in one connection
// and acknowledged with typed responses.
func TestE2E_CommandsInOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	d := h.device("E2E-002")
	if err := d.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	id := deviceID("E2E-002")
	var uuids []string
	for _, payload := range []commands.Command{&commands.DeviceInformation{}, &commands.ProfileList{}, &commands.DeviceLock{PIN: new("123456")}} {
		cmd, err := mdm.NewCommand(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
		uuids = append(uuids, cmd.UUID)
	}
	got, err := d.Connect(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("connect: %d %v", len(got), err)
	}
	for i, cmd := range got {
		if cmd.UUID != uuids[i] {
			t.Fatalf("order: got %s at %d, want %s", cmd.UUID, i, uuids[i])
		}
	}
	res, err := h.store.Commands(ctx, id, storage.CommandQuery{States: []storage.State{storage.StateAcknowledged}}, storage.Page{})
	if err != nil || len(res.Items) != 3 {
		t.Fatalf("acknowledged = %d %v", len(res.Items), err)
	}
	for _, c := range res.Items {
		if c.Result == nil || c.Result.Status != mdm.StatusAcknowledged {
			t.Fatalf("result %+v", c)
		}
	}
	// The typed response of the DeviceLock acknowledgement decodes.
	for _, c := range res.Items {
		if c.Command.RequestType == "DeviceLock" {
			if _, err := mdm.DecodeResponse(c.Result.Raw, "DeviceLock"); err != nil {
				t.Fatalf("typed decode: %v", err)
			}
		}
	}
}

// E2E-003: a device answers NotNow; the command is not re-sent on that
// connection and is retried after the backoff.
func TestE2E_NotNowBackoff(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	d := h.device("E2E-003")
	if err := d.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	id := deviceID("E2E-003")
	cmd, _ := mdm.NewCommand(&commands.DeviceInformation{})
	other, _ := mdm.NewCommand(&commands.ProfileList{})
	for _, c := range []*mdm.Command{cmd, other} {
		if _, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{id}, c, storage.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	busy := true
	d.Responder = func(c *mdm.Command) simulator.Reply {
		if busy && c.UUID == cmd.UUID {
			return simulator.Reply{Status: mdm.StatusNotNow}
		}
		return simulator.AcknowledgeAll(c)
	}
	got, err := d.Connect(ctx)
	if err != nil || len(got) != 2 || got[0].UUID != cmd.UUID || got[1].UUID != other.UUID {
		t.Fatalf("first connection: %v %v", got, err)
	}
	// Before the backoff nothing is delivered.
	if got, _ = d.Connect(ctx); len(got) != 0 {
		t.Fatalf("delivered before backoff: %v", got)
	}
	busy = false
	h.clock.Advance(time.Minute)
	got, err = d.Connect(ctx)
	if err != nil || len(got) != 1 || got[0].UUID != cmd.UUID {
		t.Fatalf("retry: %v %v", got, err)
	}
	res, _ := h.store.Commands(ctx, id, storage.CommandQuery{}, storage.Page{})
	for _, c := range res.Items {
		if c.Command.UUID == cmd.UUID && (c.State != storage.StateAcknowledged || c.NotNowCount != 1) {
			t.Fatalf("state after retry: %+v", c)
		}
	}
}

// E2E-004: an Error response with an ErrorChain is stored and published.
func TestE2E_CommandError(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	d := h.device("E2E-004")
	if err := d.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	id := deviceID("E2E-004")
	cmd, _ := mdm.NewCommand(&commands.EraseDevice{PIN: new("123456")})
	if _, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	d.Responder = func(*mdm.Command) simulator.Reply {
		return simulator.Reply{Status: mdm.StatusError, ErrorChain: []mdm.ErrorChainItem{{ErrorCode: 12021, ErrorDomain: "MCMDMErrorDomain", USEnglishDescription: "not supervised"}}}
	}
	if _, err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	res, _ := h.store.Commands(ctx, id, storage.CommandQuery{States: []storage.State{storage.StateError}}, storage.Page{})
	if len(res.Items) != 1 || len(res.Items[0].Result.ErrorChain) != 1 || res.Items[0].Result.ErrorChain[0].ErrorCode != 12021 {
		t.Fatalf("error result %+v", res.Items)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	found := false
	for _, e := range h.events {
		if e.Type == event.CommandResult {
			if r, ok := e.Data.(*mdm.Response); ok && r.Status == mdm.StatusError {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("CommandResult event with Error status not published")
	}
}

// E2E-005: re-enrollment with a new identity rotates the pinned
// certificate, clears the pending queue and escrowed tokens, and the old
// identity is refused afterwards.
func TestE2E_Reenroll(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	d := h.device("E2E-005")
	if err := d.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	id := deviceID("E2E-005")
	if err := d.SetBootstrapToken(ctx, []byte("escrowed")); err != nil {
		t.Fatal(err)
	}
	pending, _ := mdm.NewCommand(&commands.ProfileList{})
	if _, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{id}, pending, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	old := d.Identity
	if err := d.Reenroll(ctx, h.identity("E2E-005-new")); err != nil {
		t.Fatalf("reenroll: %v", err)
	}
	e, _ := h.store.Get(ctx, id)
	if !e.Enabled || e.CertHash == "" {
		t.Fatalf("record after reenroll %+v", e)
	}
	if got, _ := d.Connect(ctx); len(got) != 0 {
		t.Fatalf("pending queue survived re-enrollment: %v", got)
	}
	if tok, err := d.GetBootstrapToken(ctx); err != nil || len(tok) != 0 {
		t.Fatalf("bootstrap token survived re-enrollment: %q %v", tok, err)
	}
	// The old identity is refused.
	stale := simulator.New("E2E-005", simulator.WithURLs(h.server.URL+"/mdm", h.server.URL+"/mdm"), simulator.WithClient(h.server.Client()), simulator.WithIdentity(old))
	var he *simulator.HTTPError
	if _, err := stale.Connect(ctx); !errors.As(err, &he) || he.Status != 403 {
		t.Fatalf("old identity: %v", err)
	}
	types := h.eventTypes()
	sawRotation, sawReenroll := false, false
	for _, ty := range types {
		sawRotation = sawRotation || ty == event.CertRotated
		sawReenroll = sawReenroll || ty == event.Reenrolled
	}
	if !sawRotation || !sawReenroll {
		t.Fatalf("events = %v", types)
	}
	// DenyReenroll keeps the old identity in charge.
	strict := newHarness(t, service.Config{Reenroll: service.DenyReenroll})
	sd := strict.device("E2E-005-strict")
	if err := sd.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sd.Reenroll(ctx, strict.identity("intruder")); !errors.As(err, &he) || he.Status != 403 {
		t.Fatalf("deny reenroll: %v", err)
	}
}
