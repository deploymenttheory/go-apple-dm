package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
)

func deviceID(udid string) mdm.EnrollmentID {
	return mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: udid}
}

func certHashOf(t *testing.T, h *harness, udid string) string {
	t.Helper()
	e, err := h.store.Get(context.Background(), deviceID(udid))
	if err != nil {
		t.Fatalf("Get %s: %v", udid, err)
	}
	return e.CertHash
}

// TestCertReuseDeniedAcrossEnrollments proves decision record 0014 claim 2:
// the default policy refuses an Authenticate whose certificate another
// enrollment pinned before and publishes CertReuseDenied with the history.
func TestCertReuseDeniedAcrossEnrollments(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	enroll(t, h, "D1")
	hashA := certHashOf(t, h, "D1")
	h.events = nil
	_, err := h.core.Checkin(ctx, req(h.cert), authenticate(t, "D2"))
	if service.CodeOf(err) != service.CodeForbidden || !errors.Is(err, service.ErrCertReused) {
		t.Fatalf("D2 with D1's certificate: code=%d err=%v", service.CodeOf(err), err)
	}
	if got := h.eventTypes(); strings.Join(got, ",") != "cert-reuse-denied" {
		t.Fatalf("events = %v", got)
	}
	ev := h.events[0]
	previous, ok := ev.Data.([]storage.CertAssociation)
	if !ok || len(previous) != 1 || previous[0].ID != deviceID("D1") || previous[0].Hash != hashA || ev.Enrollment != deviceID("D2") || ev.Actor != "device" {
		t.Fatalf("event = %+v", ev)
	}
	// The refusal wrote nothing for D2.
	if _, err := h.store.Get(ctx, deviceID("D2")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("D2 record after refusal: %v", err)
	}
	// A fresh certificate enrols D2 normally.
	if _, err := h.core.Checkin(ctx, req(h.cert2), authenticate(t, "D2")); err != nil {
		t.Fatalf("D2 with its own certificate: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert2), tokenUpdate(t, "D2", nil)); err != nil {
		t.Fatalf("D2 TokenUpdate: %v", err)
	}
	if certHashOf(t, h, "D2") == hashA {
		t.Fatal("D2 pinned D1's certificate")
	}
	// A custom policy's error is wrapped so callers still match ErrCertReused.
	errPolicy := errors.New("quarantined")
	custom := newHarness(t, service.Config{CertReuse: func(_ context.Context, r *mdm.Request, previous []storage.CertAssociation) error {
		if r.ID.ID != "D2" || len(previous) != 1 {
			t.Errorf("policy called with %s %+v", r.ID.ID, previous)
		}
		return errPolicy
	}})
	enroll(t, custom, "D1")
	if _, err := custom.core.Checkin(ctx, req(custom.cert), authenticate(t, "D2")); !errors.Is(err, service.ErrCertReused) || !errors.Is(err, errPolicy) || service.CodeOf(err) != service.CodeForbidden {
		t.Fatalf("custom policy: %v", err)
	}
}

// TestCertReuseAllowedByPolicy proves decision record 0014 claims 2 and 4:
// AllowCertReuse permits a certificate that is only in history, and never
// overrides a live pin.
func TestCertReuseAllowedByPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// (a) D1 rotated from A to C, so A is history only.
	h := newHarness(t, service.Config{CertReuse: service.AllowCertReuse})
	enroll(t, h, "D1")
	hashA := certHashOf(t, h, "D1")
	if _, err := h.core.Checkin(ctx, req(h.cert2), authenticate(t, "D1")); err != nil {
		t.Fatalf("rotate D1: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert2), tokenUpdate(t, "D1", nil)); err != nil {
		t.Fatalf("rotate D1 TokenUpdate: %v", err)
	}
	h.events = nil
	if _, err := h.core.Checkin(ctx, req(h.cert), authenticate(t, "D2")); err != nil {
		t.Fatalf("D2 with historical certificate: %v", err)
	}
	if _, err := h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, "D2", nil)); err != nil {
		t.Fatalf("D2 TokenUpdate: %v", err)
	}
	if certHashOf(t, h, "D2") != hashA {
		t.Fatal("D2 did not pin the reused certificate")
	}
	if got := h.eventTypes(); strings.Join(got, ",") != "enrolled,token-updated" {
		t.Fatalf("events = %v", got)
	}
	// (b) A is still pinned by D1: the live pin wins regardless of policy.
	live := newHarness(t, service.Config{CertReuse: service.AllowCertReuse})
	enroll(t, live, "D1")
	liveHash := certHashOf(t, live, "D1")
	live.events = nil
	_, err := live.core.Checkin(ctx, req(live.cert), authenticate(t, "D2"))
	if service.CodeOf(err) != service.CodeForbidden || !errors.Is(err, service.ErrCertMismatch) || !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("D2 with a live pin: code=%d err=%v", service.CodeOf(err), err)
	}
	if errors.Is(err, service.ErrCertReused) {
		t.Fatalf("live pin reported as history reuse: %v", err)
	}
	for _, e := range live.events {
		if e.Type == "cert-reuse-denied" {
			t.Fatalf("CertReuseDenied published for an allowed policy: %v", live.eventTypes())
		}
	}
	if certHashOf(t, live, "D1") != liveHash {
		t.Fatal("D1 lost its pin")
	}
}

// seedWithoutPin writes an enabled enrollment straight into the store, the
// way a migration that carried no identity would, so no certificate is
// pinned.
func seedWithoutPin(t *testing.T, h *harness, udid string) {
	t.Helper()
	ctx := context.Background()
	auth := authenticate(t, udid)
	if err := h.store.UpsertAuthenticate(ctx, deviceID(udid), auth.Message.(*checkin.Authenticate), auth.Raw, t0); err != nil {
		t.Fatal(err)
	}
	tu := tokenUpdate(t, udid, nil)
	msg := tu.Message.(*checkin.TokenUpdate)
	push, err := mdm.PushFromTokenUpdate(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.StoreTokenUpdate(ctx, deviceID(udid), push, msg, tu.Raw, t0); err != nil {
		t.Fatal(err)
	}
	if certHashOf(t, h, udid) != "" {
		t.Fatalf("%s seeded with a pin", udid)
	}
}

// TestRetroactivePinOnlyIfUnseen proves decision record 0014 claim 3: the
// retroactive pin in authorize only takes a hash no other enrollment has
// presented; otherwise PinEnforce refuses and PinWarn allows without
// writing.
func TestRetroactivePinOnlyIfUnseen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newHarness(t, service.Config{})
	seedWithoutPin(t, h, "D1")
	if _, err := h.core.Connect(ctx, req(h.cert), response("D1", "", mdm.StatusIdle)); err != nil {
		t.Fatalf("D1 first connect: %v", err)
	}
	hashA := certHashOf(t, h, "D1")
	if hashA == "" {
		t.Fatal("D1 not pinned retroactively")
	}
	seedWithoutPin(t, h, "D2")
	_, err := h.core.Connect(ctx, req(h.cert), response("D2", "", mdm.StatusIdle))
	if service.CodeOf(err) != service.CodeForbidden || !errors.Is(err, service.ErrCertReused) {
		t.Fatalf("D2 retroactive with D1's certificate: code=%d err=%v", service.CodeOf(err), err)
	}
	if certHashOf(t, h, "D2") != "" {
		t.Fatal("D2 pinned a certificate seen on D1")
	}
	// A never-seen certificate is pinned.
	if _, err := h.core.Connect(ctx, req(h.cert2), response("D2", "", mdm.StatusIdle)); err != nil {
		t.Fatalf("D2 retroactive with a fresh certificate: %v", err)
	}
	if certHashOf(t, h, "D2") == "" || certHashOf(t, h, "D2") == hashA {
		t.Fatal("D2 not pinned to its own certificate")
	}
	// PinWarn allows the request and writes nothing.
	warn := newHarness(t, service.Config{Pinning: service.PinWarn})
	seedWithoutPin(t, warn, "D1")
	if _, err := warn.core.Connect(ctx, req(warn.cert), response("D1", "", mdm.StatusIdle)); err != nil {
		t.Fatal(err)
	}
	seedWithoutPin(t, warn, "D2")
	if _, err := warn.core.Connect(ctx, req(warn.cert), response("D2", "", mdm.StatusIdle)); err != nil {
		t.Fatalf("PinWarn retroactive reuse: %v", err)
	}
	if certHashOf(t, warn, "D2") != "" {
		t.Fatal("PinWarn wrote a pin for a seen certificate")
	}
}
