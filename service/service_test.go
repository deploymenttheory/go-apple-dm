package service_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/plist"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
	"github.com/deploymenttheory/go-apple-mdm/schema/commands"
	"github.com/deploymenttheory/go-apple-mdm/service"
	"github.com/deploymenttheory/go-apple-mdm/storage"
	"github.com/deploymenttheory/go-apple-mdm/storage/inmem"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	core   *service.Core
	store  *inmem.Store
	clock  *clock.Fake
	events []event.Event
	ca     *testpki.CA
	cert   *x509.Certificate
	cert2  *x509.Certificate
}

func newHarness(t *testing.T, cfg service.Config) *harness {
	t.Helper()
	h := &harness{store: inmem.New(), clock: clock.NewFake(t0)}
	bus := event.New()
	bus.Subscribe(event.All, func(_ context.Context, e event.Event) error { h.events = append(h.events, e); return nil })
	ca, err := testpki.NewCA("test")
	if err != nil {
		t.Fatal(err)
	}
	h.ca = ca
	id1, _ := ca.Issue("dev", time.Now().Add(-time.Minute))
	id2, _ := ca.Issue("dev-new", time.Now().Add(-time.Minute))
	h.cert, h.cert2 = id1.Cert, id2.Cert
	cfg.Store, cfg.Bus, cfg.Clock = h.store, bus, h.clock
	h.core, err = service.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) eventTypes() []string {
	out := make([]string, 0, len(h.events))
	for _, e := range h.events {
		out = append(out, string(e.Type))
	}
	return out
}

func req(cert *x509.Certificate) *mdm.Request {
	return &mdm.Request{Certificate: cert, ReceivedAt: t0}
}

func checkinPlist(t *testing.T, fields map[string]any) *mdm.Checkin {
	t.Helper()
	raw, err := plist.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	ck, err := mdm.DecodeCheckin(raw)
	if err != nil {
		t.Fatalf("decode %v: %v", fields, err)
	}
	return ck
}

func authenticate(t *testing.T, udid string) *mdm.Checkin {
	t.Helper()
	return checkinPlist(t, map[string]any{"MessageType": "Authenticate", "Topic": "com.apple.mgmt.t", "UDID": udid, "Model": "Mac", "ModelName": "MacBook", "DeviceName": "d", "SerialNumber": "S1"})
}

func tokenUpdate(t *testing.T, udid string, extra map[string]any) *mdm.Checkin {
	t.Helper()
	f := map[string]any{"MessageType": "TokenUpdate", "Topic": "com.apple.mgmt.t", "UDID": udid, "PushMagic": "magic", "Token": []byte{1, 2, 3}, "UserLongName": ""}
	maps.Copy(f, extra)
	return checkinPlist(t, f)
}

func simple(t *testing.T, msgType, udid string, extra map[string]any) *mdm.Checkin {
	t.Helper()
	f := map[string]any{"MessageType": msgType, "UDID": udid}
	maps.Copy(f, extra)
	return checkinPlist(t, f)
}

func response(udid, cmdUUID string, status mdm.Status) *mdm.Response {
	return &mdm.Response{Enrollment: mdm.Enrollment{UDID: udid}, ID: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: udid}, CommandUUID: cmdUUID, Status: status}
}

func enroll(t *testing.T, h *harness, udid string) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.core.Checkin(ctx, req(h.cert), authenticate(t, udid)); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, udid, nil)); err != nil {
		t.Fatalf("TokenUpdate: %v", err)
	}
}

func TestEnrollAndCommandFlow(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}

	// Authenticate without a certificate is refused under PinEnforce.
	if _, err := h.core.Checkin(ctx, req(nil), authenticate(t, "D1")); service.CodeOf(err) != service.CodeForbidden || !errors.Is(err, service.ErrCertRequired) {
		t.Fatalf("no cert: %v", err)
	}
	enroll(t, h, "D1")
	e, err := h.store.Get(ctx, dev)
	if err != nil || !e.Enabled || e.CertHash == "" || e.Device.SerialNumber != "S1" {
		t.Fatalf("enrollment %+v %v", e, err)
	}
	// Idle with nothing queued.
	cmd, err := h.core.Connect(ctx, req(h.cert), response("D1", "", mdm.StatusIdle))
	if err != nil || cmd != nil {
		t.Fatalf("idle: %v %v", cmd, err)
	}
	// Enqueue and deliver.
	lock, _ := mdm.NewCommand(&commands.DeviceLock{PIN: new("123456")}, mdm.WithUUID("C1"))
	res, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{dev, {Channel: mdm.ChannelDevice, ID: "nope"}}, lock, storage.EnqueueOptions{})
	if err != nil || len(res.Queued) != 1 || len(res.Skipped) != 1 {
		t.Fatalf("enqueue: %+v %v", res, err)
	}
	cmd, err = h.core.Connect(ctx, req(h.cert), response("D1", "", mdm.StatusIdle))
	if err != nil || cmd == nil || cmd.UUID != "C1" {
		t.Fatalf("deliver: %+v %v", cmd, err)
	}
	// NotNow: command retried after backoff, not on the same connection.
	h.clock.Advance(time.Second)
	cmd, err = h.core.Connect(ctx, req(h.cert), response("D1", "C1", mdm.StatusNotNow))
	if err != nil || cmd != nil {
		t.Fatalf("after NotNow: %+v %v", cmd, err)
	}
	h.clock.Advance(time.Minute)
	cmd, err = h.core.Connect(ctx, req(h.cert), response("D1", "", mdm.StatusIdle))
	if err != nil || cmd == nil || cmd.UUID != "C1" {
		t.Fatalf("retry after backoff: %+v %v", cmd, err)
	}
	ack := response("D1", "C1", mdm.StatusAcknowledged)
	cmd, err = h.core.Connect(ctx, req(h.cert), ack)
	if err != nil || cmd != nil {
		t.Fatalf("ack: %+v %v", cmd, err)
	}
	// A late duplicate result is tolerated.
	if _, err := h.core.Connect(ctx, req(h.cert), ack); err != nil {
		t.Fatalf("duplicate ack: %v", err)
	}
	// Bootstrap token round trip.
	if _, err := h.core.Checkin(ctx, req(h.cert), simple(t, "SetBootstrapToken", "D1", map[string]any{"BootstrapToken": []byte("bst")})); err != nil {
		t.Fatal(err)
	}
	got, err := h.core.Checkin(ctx, req(h.cert), simple(t, "GetBootstrapToken", "D1", nil))
	if err != nil || !strings.Contains(string(got.Body), "YnN0") || got.ContentType != service.ContentTypePlist {
		t.Fatalf("GetBootstrapToken: %+v %v", got, err)
	}
	// Check out.
	if _, err := h.core.Checkin(ctx, req(h.cert), simple(t, "CheckOut", "D1", nil)); err != nil {
		t.Fatal(err)
	}
	if e, _ := h.store.Get(ctx, dev); e.Enabled {
		t.Fatal("still enabled after CheckOut")
	}
	want := []string{"enrolled", "token-updated", "command-queued", "command-sent", "command-result", "command-sent", "command-result", "command-result", "bootstrap-token-set", "checked-out"}
	if got := h.eventTypes(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v\nwant %v", got, want)
	}
	if h.events[0].Enrollment != dev || h.events[0].Actor != "device" || !h.events[0].At.Equal(t0) {
		t.Fatalf("event detail = %+v", h.events[0])
	}
}

func TestPinningAndReenroll(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	enroll(t, h, "D1")
	// A different certificate is rejected on the command channel and check-in.
	if _, err := h.core.Connect(ctx, req(h.cert2), response("D1", "", mdm.StatusIdle)); service.CodeOf(err) != service.CodeForbidden || !errors.Is(err, service.ErrCertMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert2), tokenUpdate(t, "D1", nil)); !errors.Is(err, service.ErrCertMismatch) {
		t.Fatalf("mismatch token update: %v", err)
	}
	if _, err := h.core.Connect(ctx, req(nil), response("D1", "", mdm.StatusIdle)); !errors.Is(err, service.ErrCertRequired) {
		t.Fatalf("no cert on connect: %v", err)
	}
	// Unknown enrollment.
	if _, err := h.core.Connect(ctx, req(h.cert), response("D9", "", mdm.StatusIdle)); service.CodeOf(err) != service.CodeUnknownEnrollment {
		t.Fatalf("unknown: %v", err)
	}
	// Re-enrollment with a new identity rotates the pin and resets state.
	h.events = nil
	if _, err := h.core.Checkin(ctx, req(h.cert2), authenticate(t, "D1")); err != nil {
		t.Fatalf("reenroll: %v", err)
	}
	if got := h.eventTypes(); strings.Join(got, ",") != "cert-rotated,reenrolled" {
		t.Fatalf("events = %v", got)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "D1", nil)); !errors.Is(err, service.ErrCertMismatch) {
		t.Fatalf("old cert after rotation: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert2), tokenUpdate(t, "D1", nil)); err != nil {
		t.Fatalf("new cert token update: %v", err)
	}
	// Re-enrollment with the same identity is a plain re-enroll.
	h.events = nil
	if _, err := h.core.Checkin(ctx, req(h.cert2), authenticate(t, "D1")); err != nil {
		t.Fatal(err)
	}
	if got := h.eventTypes(); strings.Join(got, ",") != "reenrolled" {
		t.Fatalf("events = %v", got)
	}
	// Authenticate on a user channel is invalid.
	if _, err := h.core.Checkin(ctx, req(h.cert2), checkinPlist(t, map[string]any{"MessageType": "Authenticate", "Topic": "t", "UDID": "D1", "UserID": "U", "Model": "m", "ModelName": "mn", "DeviceName": "d"})); service.CodeOf(err) != service.CodeBadRequest {
		t.Fatalf("user channel authenticate: %v", err)
	}
}

func TestDenyReenrollAndPinModes(t *testing.T) {
	t.Parallel()
	deny := newHarness(t, service.Config{Reenroll: service.DenyReenroll})
	ctx := context.Background()
	enroll(t, deny, "D1")
	if _, err := deny.core.Checkin(ctx, req(deny.cert2), authenticate(t, "D1")); !errors.Is(err, service.ErrReenrollDenied) || service.CodeOf(err) != service.CodeForbidden {
		t.Fatalf("deny: %v", err)
	}
	warn := newHarness(t, service.Config{Pinning: service.PinWarn})
	enroll(t, warn, "D1")
	if _, err := warn.core.Connect(ctx, req(warn.cert2), response("D1", "", mdm.StatusIdle)); err != nil {
		t.Fatalf("warn mode should allow: %v", err)
	}
	if _, err := warn.core.Connect(ctx, req(nil), response("D1", "", mdm.StatusIdle)); err != nil {
		t.Fatalf("warn mode without cert: %v", err)
	}
	off := newHarness(t, service.Config{Pinning: service.PinOff})
	if _, err := off.core.Checkin(ctx, req(nil), authenticate(t, "D1")); err != nil {
		t.Fatalf("off mode authenticate without cert: %v", err)
	}
	if _, err := off.core.Checkin(ctx, req(nil), tokenUpdate(t, "D1", nil)); err != nil {
		t.Fatalf("off mode token update: %v", err)
	}
	// Retroactive pinning: enrollment created without a certificate under
	// PinWarn gets pinned by the first request that carries one.
	retro := newHarness(t, service.Config{Pinning: service.PinWarn})
	if _, err := retro.core.Checkin(ctx, req(nil), authenticate(t, "D1")); err != nil {
		t.Fatal(err)
	}
	if _, err := retro.core.Checkin(ctx, req(retro.cert), tokenUpdate(t, "D1", nil)); err != nil {
		t.Fatal(err)
	}
	if e, _ := retro.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}); e.CertHash == "" {
		t.Fatal("retroactive pin not recorded")
	}
	// A certificate another device already presented cannot enrol a second
	// one under the default reuse policy.
	if _, err := retro.core.Checkin(ctx, req(retro.cert), authenticate(t, "D2")); !errors.Is(err, service.ErrCertReused) {
		t.Fatalf("cert reuse across devices: %v", err)
	}
	// Presented retroactively by another enrollment under PinWarn, the
	// request is allowed but nothing is pinned.
	if _, err := retro.core.Checkin(ctx, req(nil), authenticate(t, "D3")); err != nil {
		t.Fatal(err)
	}
	if _, err := retro.core.Checkin(ctx, req(retro.cert), tokenUpdate(t, "D3", nil)); err != nil {
		t.Fatalf("retroactive reuse under PinWarn: %v", err)
	}
	if e, _ := retro.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D3"}); e.CertHash != "" {
		t.Fatalf("retroactive pin written for a seen certificate: %q", e.CertHash)
	}
}

func TestUserChannel(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	// User channel before the device exists is unknown.
	if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "D1", map[string]any{"UserID": "U1"})); service.CodeOf(err) != service.CodeUnknownEnrollment {
		t.Fatalf("user before device: %v", err)
	}
	enroll(t, h, "D1")
	if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "D1", map[string]any{"UserID": "U1", "UserShortName": "alice", "UserLongName": "Alice"})); err != nil {
		t.Fatalf("user token update: %v", err)
	}
	uid := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "D1:U1", ParentID: "D1"}
	u, err := h.store.Get(ctx, uid)
	if err != nil || !u.Enabled || u.UserShortName != "alice" {
		t.Fatalf("user record %+v %v", u, err)
	}
	// Commands flow on the user channel too.
	pl, _ := mdm.NewCommand(&commands.ProfileList{}, mdm.WithUUID("U-C1"))
	if _, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{uid}, pl, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	r := &mdm.Response{Enrollment: mdm.Enrollment{UDID: "D1", UserID: "U1"}, ID: uid, Status: mdm.StatusIdle}
	cmd, err := h.core.Connect(ctx, req(h.cert), r)
	if err != nil || cmd == nil || cmd.UUID != "U-C1" {
		t.Fatalf("user channel command: %+v %v", cmd, err)
	}
	// UserAuthenticate default accepts everyone with an empty challenge.
	got, err := h.core.Checkin(ctx, req(h.cert), checkinPlist(t, map[string]any{"MessageType": "UserAuthenticate", "UDID": "D1", "UserID": "U1", "DigestResponse": ""}))
	if err != nil || !strings.Contains(string(got.Body), "<key>DigestChallenge</key><string></string>") {
		t.Fatalf("UserAuthenticate: %s %v", got.Body, err)
	}
}

func TestHandlersAndHooks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// No handlers configured.
	h := newHarness(t, service.Config{})
	enroll(t, h, "D1")
	if _, err := h.core.Checkin(ctx, req(h.cert), simple(t, "GetToken", "D1", map[string]any{"TokenServiceType": "com.apple.maid"})); service.CodeOf(err) != service.CodeNotImplemented || !errors.Is(err, service.ErrNoHandler) {
		t.Fatalf("GetToken without handler: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), simple(t, "DeclarativeManagement", "D1", map[string]any{"Endpoint": "tokens"})); service.CodeOf(err) != service.CodeNotImplemented {
		t.Fatalf("DM without handler: %v", err)
	}
	// Handlers configured, plus a hook that vetoes CheckOut and records calls.
	hook := &recordingHook{veto: "checkin:CheckOut"}
	h2 := newHarness(t, service.Config{
		Hooks: []service.Hook{hook},
		GetToken: func(_ context.Context, _ *mdm.Request, m *checkin.GetToken) (*checkin.GetTokenResponse, error) {
			return &checkin.GetTokenResponse{TokenData: []byte("tok-" + m.TokenServiceType)}, nil
		},
		DeclarativeManagement: func(_ context.Context, _ *mdm.Request, m *checkin.DeclarativeManagement) (service.DMResponse, error) {
			if m.Endpoint == "boom" {
				return service.DMResponse{}, &service.Error{Code: service.CodeBadRequest, Err: errors.New("bad endpoint")}
			}
			return service.DMResponse{Body: []byte(`{"ok":true}`), ContentType: "application/json", Status: 200}, nil
		},
		UserAuthenticate: func(context.Context, *mdm.Request, *checkin.UserAuthenticate) (*mdm.UserAuthenticateResponse, error) {
			return nil, &service.Error{Code: service.CodeGone, Err: errors.New("not managing users")}
		},
	})
	enroll(t, h2, "D1")
	got, err := h2.core.Checkin(ctx, req(h2.cert), simple(t, "GetToken", "D1", map[string]any{"TokenServiceType": "com.apple.maid"}))
	if err != nil || !strings.Contains(string(got.Body), "<key>TokenData</key>") {
		t.Fatalf("GetToken: %s %v", got.Body, err)
	}
	got, err = h2.core.Checkin(ctx, req(h2.cert), simple(t, "DeclarativeManagement", "D1", map[string]any{"Endpoint": "tokens"}))
	if err != nil || string(got.Body) != `{"ok":true}` || got.ContentType != "application/json" || got.Status != 200 {
		t.Fatalf("DM: %+v %v", got, err)
	}
	if _, err := h2.core.Checkin(ctx, req(h2.cert), simple(t, "DeclarativeManagement", "D1", map[string]any{"Endpoint": "boom"})); service.CodeOf(err) != service.CodeBadRequest {
		t.Fatalf("DM error code: %v", err)
	}
	if _, err := h2.core.Checkin(ctx, req(h2.cert), checkinPlist(t, map[string]any{"MessageType": "UserAuthenticate", "UDID": "D1", "UserID": "U1", "DigestResponse": ""})); service.CodeOf(err) != service.CodeGone {
		t.Fatalf("UserAuthenticate gone: %v", err)
	}
	if _, err := h2.core.Checkin(ctx, req(h2.cert), simple(t, "CheckOut", "D1", nil)); !errors.Is(err, service.ErrHookVeto) || service.CodeOf(err) != service.CodeForbidden {
		t.Fatalf("hook veto: %v", err)
	}
	if hook.before == 0 || hook.after != hook.before-1 {
		t.Fatalf("hook calls before=%d after=%d", hook.before, hook.after)
	}
	if _, err := h2.core.Enqueue(ctx, nil, nil, storage.EnqueueOptions{}); service.CodeOf(err) != service.CodeBadRequest {
		t.Fatalf("enqueue nil: %v", err)
	}
}

func TestArgumentErrors(t *testing.T) {
	t.Parallel()
	if _, err := service.New(service.Config{}); err == nil {
		t.Fatal("New without store")
	}
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	if _, err := h.core.Checkin(ctx, nil, nil); service.CodeOf(err) != service.CodeBadRequest {
		t.Fatalf("nil checkin: %v", err)
	}
	if _, err := h.core.Connect(ctx, nil, nil); service.CodeOf(err) != service.CodeBadRequest {
		t.Fatalf("nil connect: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "D1", nil)); service.CodeOf(err) != service.CodeUnknownEnrollment {
		t.Fatalf("token update before authenticate: %v", err)
	}
	bad := checkinPlist(t, map[string]any{"MessageType": "TokenUpdate", "Topic": "t", "UDID": "D1", "UserLongName": ""})
	if _, err := h.core.Checkin(ctx, req(h.cert), bad); service.CodeOf(err) != service.CodeBadRequest {
		t.Fatalf("incomplete token update: %v", err)
	}
	if service.CodeOf(errors.New("plain")) != service.CodeInternal {
		t.Fatal("CodeOf plain error")
	}
	se := &service.Error{Code: service.CodeGone, Err: fmt.Errorf("wrapped: %w", service.ErrNoHandler)}
	if se.Error() == "" || !errors.Is(se, service.ErrNoHandler) {
		t.Fatal("Error methods")
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), simple(t, "CheckOut", "D9", nil)); service.CodeOf(err) != service.CodeUnknownEnrollment {
		t.Fatalf("checkout unknown: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), simple(t, "SetBootstrapToken", "D9", map[string]any{"BootstrapToken": []byte("x")})); service.CodeOf(err) != service.CodeUnknownEnrollment {
		t.Fatalf("bootstrap unknown: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), simple(t, "GetBootstrapToken", "D9", nil)); service.CodeOf(err) != service.CodeUnknownEnrollment {
		t.Fatalf("get bootstrap unknown: %v", err)
	}
}

type recordingHook struct {
	veto   string
	before int
	after  int
}

func (r *recordingHook) Before(ctx context.Context, c *service.Call) (context.Context, error) {
	r.before++
	if c.Op == r.veto {
		return ctx, errors.New("vetoed")
	}
	return context.WithValue(ctx, ctxKey{}, c.Op), nil
}

func (r *recordingHook) After(ctx context.Context, c *service.Call, _ error) {
	r.after++
	if ctx.Value(ctxKey{}) != c.Op {
		panic("After did not receive the Before context")
	}
}

type ctxKey struct{}
