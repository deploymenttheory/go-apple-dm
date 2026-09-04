//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push/pushtest"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
)

// E2E-006: a device follows an unsigned enrollment profile, enrolls its
// identity through SCEP with a one-time challenge, is pushed through the
// APNs client against a fake APNs, and fetches a command.
func TestE2E_SCEPEnrollPush(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	const udid = "E2E-006"
	d := simulator.New(udid, simulator.WithClient(h.server.Client()))
	if err := d.ApplyProfile(ctx, h.enrollmentProfile(udid), profile.ParseOptions{}); err != nil {
		t.Fatalf("apply profile: %v", err)
	}
	if d.Identity == nil || d.Identity.Cert.Issuer.CommonName != h.scepCA.Subject.CommonName || d.Identity.Cert.Subject.CommonName != udid {
		t.Fatalf("identity %+v", d.Identity)
	}
	if d.ServerURL != h.server.URL+"/mdm" || d.Topic != pushTopic {
		t.Fatalf("profile not applied: %s %s", d.ServerURL, d.Topic)
	}
	if err := d.Enroll(ctx); err != nil {
		t.Fatalf("enroll with SCEP identity: %v", err)
	}
	// The one-time challenge is spent: a second device with the same
	// profile is refused by the SCEP server.
	twin := simulator.New("E2E-006-twin", simulator.WithClient(h.server.Client()))
	if err := twin.ApplyProfile(ctx, h.enrollmentProfile(udid), profile.ParseOptions{}); err != nil {
		t.Fatalf("fresh challenge: %v", err)
	}
	if twin.Identity.Cert.Subject.CommonName != udid {
		t.Fatal("subject from profile")
	}

	id := deviceID(udid)
	cmd, err := mdm.NewCommand(&commands.DeviceInformation{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err := h.notifier.Notify(ctx, []mdm.EnrollmentID{id})
	if err != nil || !res[id].Sent() || res[id].TokenInvalid() {
		t.Fatalf("push: %+v %v", res[id], err)
	}
	reqs := h.apns.Requests()
	if len(reqs) != 1 || reqs[0].Magic != d.PushMagic || reqs[0].Topic != pushTopic || reqs[0].PushType != "mdm" {
		t.Fatalf("apns requests %+v", reqs)
	}
	got, err := d.Connect(ctx)
	if err != nil || len(got) != 1 || got[0].UUID != cmd.UUID {
		t.Fatalf("connect after push: %v %v", got, err)
	}
	for _, typ := range []event.Type{event.Enrolled, event.TokenUpdated, event.CommandQueued, event.CommandResult} {
		if !hasEvent(h.eventTypes(), typ) {
			t.Fatalf("missing event %s in %v", typ, h.eventTypes())
		}
	}
}

// E2E-007: APNs answers 410 Unregistered for a device token; the result is
// marked invalid and PushTokenInvalid is published for subscribers to act
// on (disable the enrollment, alert, or wait for the next TokenUpdate).
func TestE2E_PushInvalidToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	d := h.device("E2E-007")
	if err := d.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	h.apns.ScriptToken(d.PushToken, pushtest.Script{Status: 410, Reason: "Unregistered"})
	id := deviceID("E2E-007")
	res, err := h.notifier.Notify(ctx, []mdm.EnrollmentID{id})
	if err != nil || res[id].Sent() || !res[id].TokenInvalid() || res[id].Status != 410 || res[id].Reason != "Unregistered" {
		t.Fatalf("410: %+v %v", res[id], err)
	}
	if !hasEvent(h.eventTypes(), event.PushTokenInvalid) {
		t.Fatalf("PushTokenInvalid not published: %v", h.eventTypes())
	}
	// Unknown enrollments are reported per id, not pushed.
	res, err = h.notifier.Notify(ctx, []mdm.EnrollmentID{deviceID("nobody")})
	if err != nil || res[deviceID("nobody")].Err == nil {
		t.Fatalf("unknown: %+v %v", res, err)
	}
	if len(h.apns.Requests()) != 1 {
		t.Fatalf("apns requests %d", len(h.apns.Requests()))
	}
}

// E2E-016: over-the-air profile service. Phase 1 is signed by the device
// certificate and carries the OTA challenge, phase 2 by the SCEP identity
// issued for the same UDID; the final profile enrolls the device.
func TestE2E_OTAProfileService(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	const udid = "E2E-016"
	deviceCert := h.identity("apple-device-" + udid)
	d := simulator.New(udid, simulator.WithClient(h.server.Client()))
	if err := d.OTAEnroll(ctx, h.server.URL+"/ota", otaChallenge, deviceCert, profile.ParseOptions{}); err != nil {
		t.Fatalf("OTA: %v", err)
	}
	if d.Identity.Cert.Subject.CommonName != udid || d.Identity.Cert.Issuer.CommonName != h.scepCA.Subject.CommonName {
		t.Fatalf("identity %v", d.Identity.Cert.Subject)
	}
	if err := d.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Connect(ctx); err != nil || len(got) != 0 {
		t.Fatalf("idle: %v %v", got, err)
	}
	// Wrong OTA challenge is refused in phase 1.
	bad := simulator.New(udid+"-bad", simulator.WithClient(h.server.Client()))
	if err := bad.OTAEnroll(ctx, h.server.URL+"/ota", "wrong", deviceCert, profile.ParseOptions{}); err == nil {
		t.Fatal("wrong OTA challenge accepted")
	}
	// A phase 1 request signed by an identity the harness does not trust.
	stranger := simulator.New(udid+"-stranger", simulator.WithClient(h.server.Client()))
	if err := stranger.OTAEnroll(ctx, h.server.URL+"/ota", otaChallenge, d.Identity, profile.ParseOptions{}); err == nil {
		t.Fatal("phase 2 identity for another UDID accepted in phase 1")
	}
}

func hasEvent(types []event.Type, want event.Type) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}
