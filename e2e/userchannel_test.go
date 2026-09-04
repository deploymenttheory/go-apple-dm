//go:build e2e

package e2e

import (
	"context"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/v3/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
)

const userPassword = "correct horse battery staple"

// digestHarness enrols users through the RFC 2617 digest flow (record 0016)
// and requires it before a user channel exists (record 0029).
func digestHarness(t *testing.T) *harness {
	t.Helper()
	var h *harness
	digest := &service.DigestUserAuth{Verifier: service.HA1Verifier(func(_ context.Context, username, realm string) (string, error) {
		return simulator.HA1(username, realm, userPassword), nil
	})}
	h = newHarness(t, service.Config{UserAuthenticate: digest.Handle, RequireUserAuth: true})
	digest.Store, digest.Clock = h.store, h.clock
	return h
}

// enrolUser runs UserAuthenticate (challenge, response) then TokenUpdate.
func enrolUser(t *testing.T, u *simulator.User) {
	t.Helper()
	ctx := context.Background()
	body, err := u.Authenticate(ctx, "")
	if err != nil {
		t.Fatalf("UserAuthenticate: %v", err)
	}
	var challenge struct {
		DigestChallenge string `plist:"DigestChallenge"`
	}
	if err := plist.Unmarshal(body, &challenge); err != nil || challenge.DigestChallenge == "" {
		t.Fatalf("challenge body %s: %v", body, err)
	}
	resp, err := simulator.DigestResponse(challenge.DigestChallenge, u.UserID, userPassword, "/mdm", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Authenticate(ctx, resp); err != nil {
		t.Fatalf("digest response: %v", err)
	}
	if err := u.TokenUpdate(ctx); err != nil {
		t.Fatalf("user TokenUpdate: %v", err)
	}
}

// TestE2E_UserChannel is E2E-013: a macOS user channel through
// UserAuthenticate and TokenUpdate, a user-scoped command delivered to that
// user only, a device-only command refused for the user at enqueue, and two
// users coexisting on one device.
func TestE2E_UserChannel(t *testing.T) {
	ctx := context.Background()
	h := digestHarness(t)
	d := h.device("UDID-USER-1")
	if err := d.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	alice, bob := d.User("alice", "alice", "Alice"), d.User("bob", "bob", "Bob")
	// A TokenUpdate before UserAuthenticate is refused.
	if err := alice.TokenUpdate(ctx); err == nil {
		t.Fatal("user TokenUpdate without UserAuthenticate succeeded")
	}
	enrolUser(t, alice)
	enrolUser(t, bob)
	aliceID := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "UDID-USER-1:alice", ParentID: "UDID-USER-1"}
	bobID := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "UDID-USER-1:bob", ParentID: "UDID-USER-1"}
	for _, id := range []mdm.EnrollmentID{aliceID, bobID} {
		if e, err := h.store.Get(ctx, id); err != nil || !e.Enabled {
			t.Fatalf("%s: %+v %v", id.ID, e, err)
		}
	}

	// A user-scoped command to alice only.
	cmd, err := mdm.NewCommand(&commands.ProfileList{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{aliceID}, cmd, storage.EnqueueOptions{})
	if err != nil || len(res.Queued) != 1 {
		t.Fatalf("enqueue: %+v %v", res, err)
	}
	if _, err := h.notifier.Notify(ctx, []mdm.EnrollmentID{aliceID}); err != nil {
		t.Fatal(err)
	}
	if reqs := h.apns.Requests(); len(reqs) != 1 {
		t.Fatalf("apns requests = %d, want the user channel push only", len(reqs))
	}
	got, err := alice.Connect(ctx)
	if err != nil || len(got) != 1 || got[0].RequestType != "ProfileList" {
		t.Fatalf("alice connect: %+v %v", got, err)
	}
	if got, _ := bob.Connect(ctx); len(got) != 0 {
		t.Fatalf("bob received alice's command: %+v", got)
	}
	if got, _ := d.Connect(ctx); len(got) != 0 {
		t.Fatalf("device received the user command: %+v", got)
	}

	// A device-only command addressed to a user is refused at enqueue.
	dc, _ := mdm.NewCommand(&commands.DeviceConfigured{})
	res, err = h.core.Enqueue(ctx, []mdm.EnrollmentID{aliceID, deviceID("UDID-USER-1")}, dc, storage.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Queued) != 1 || res.Queued[0] != deviceID("UDID-USER-1") || !errors.Is(res.Skipped[aliceID], service.ErrUnsupportedTarget) {
		t.Fatalf("device-only to user: %+v", res)
	}
	if got, _ := alice.Connect(ctx); len(got) != 0 {
		t.Fatalf("device-only command reached alice: %+v", got)
	}

	// Alice checks out; bob and the device stay enrolled.
	if err := alice.CheckOut(ctx); err != nil {
		t.Fatal(err)
	}
	if e, _ := h.store.Get(ctx, aliceID); e.Enabled {
		t.Fatal("alice still enabled after CheckOut")
	}
	if e, _ := h.store.Get(ctx, bobID); !e.Enabled {
		t.Fatal("bob disabled by alice's CheckOut")
	}
	if e, _ := h.store.Get(ctx, deviceID("UDID-USER-1")); !e.Enabled {
		t.Fatal("device disabled by a user CheckOut")
	}
}

// TestE2E_SharedIPad is E2E-020: the logged-in user channel of a Shared
// iPad with device-scoped and user-scoped commands routed by the schema
// metadata.
func TestE2E_SharedIPad(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, service.Config{})
	d := h.device("UDID-SHARED-1")
	d.Model, d.ModelName, d.ProductName, d.OSVersion = "iPad", "iPad Pro", "iPad14,1", "18.4"
	if err := d.Enroll(ctx); err != nil {
		t.Fatal(err)
	}
	dev := deviceID("UDID-SHARED-1")
	logout, _ := mdm.NewCommand(&commands.LogOutUser{})
	// Before anyone logs in it is a plain iPad: LogOutUser is refused.
	res, err := h.core.Enqueue(ctx, []mdm.EnrollmentID{dev}, logout, storage.EnqueueOptions{})
	if err != nil || len(res.Queued) != 0 || !errors.Is(res.Skipped[dev], service.ErrUnsupportedTarget) {
		t.Fatalf("LogOutUser on a plain iPad: %+v %v", res, err)
	}
	student := d.SharedIPadUser("student1", "Student One")
	if err := student.TokenUpdate(ctx); err != nil {
		t.Fatalf("shared iPad user TokenUpdate: %v", err)
	}
	userID := mdm.EnrollmentID{Channel: mdm.ChannelSharedIPadUser, ID: "UDID-SHARED-1:student1", ParentID: "UDID-SHARED-1"}
	if e, err := h.store.Get(ctx, userID); err != nil || !e.Enabled || e.UserShortName != "student1" {
		t.Fatalf("shared iPad user = %+v %v", e, err)
	}
	// Device-scoped: LogOutUser and UserList go to the device, never the user.
	userList, _ := mdm.NewCommand(&commands.UserList{})
	res, err = h.core.Enqueue(ctx, []mdm.EnrollmentID{dev, userID}, userList, storage.EnqueueOptions{})
	if err != nil || len(res.Queued) != 1 || res.Queued[0] != dev || !errors.Is(res.Skipped[userID], service.ErrUnsupportedTarget) {
		t.Fatalf("UserList: %+v %v", res, err)
	}
	res, err = h.core.Enqueue(ctx, []mdm.EnrollmentID{dev}, logout, storage.EnqueueOptions{})
	if err != nil || len(res.Queued) != 1 {
		t.Fatalf("LogOutUser on a Shared iPad: %+v %v", res, err)
	}
	// User-scoped: a profile list on the user channel.
	pl, _ := mdm.NewCommand(&commands.ProfileList{})
	res, err = h.core.Enqueue(ctx, []mdm.EnrollmentID{userID}, pl, storage.EnqueueOptions{})
	if err != nil || len(res.Queued) != 1 {
		t.Fatalf("ProfileList to the shared iPad user: %+v %v", res, err)
	}
	got, err := d.Connect(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("device connect: %+v %v", got, err)
	}
	got, err = student.Connect(ctx)
	if err != nil || len(got) != 1 || got[0].RequestType != "ProfileList" {
		t.Fatalf("user connect: %+v %v", got, err)
	}
}
