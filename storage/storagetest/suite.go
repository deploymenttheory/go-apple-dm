package storagetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// Factory returns a fresh, empty store for one test.
type Factory func(t *testing.T) storage.Store

// RunAll runs every suite.
func RunAll(t *testing.T, newStore Factory) {
	t.Helper()
	t.Run("Enrollment", func(t *testing.T) { RunEnrollmentSuite(t, newStore) })
	t.Run("CommandQueue", func(t *testing.T) { RunCommandQueueSuite(t, newStore) })
	t.Run("Push", func(t *testing.T) { RunPushSuite(t, newStore) })
	t.Run("CertAuth", func(t *testing.T) { RunCertAuthSuite(t, newStore) })
	t.Run("BootstrapToken", func(t *testing.T) { RunBootstrapTokenSuite(t, newStore) })
	t.Run("PushCert", func(t *testing.T) { RunPushCertSuite(t, newStore) })
	t.Run("UserAuth", func(t *testing.T) { RunUserAuthSuite(t, newStore) })
	t.Run("Migration", func(t *testing.T) { RunMigrationSuite(t, newStore) })
	t.Run("Concurrency", func(t *testing.T) { RunConcurrencySuite(t, newStore) })
}

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func device(n int) mdm.EnrollmentID {
	return mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: fmt.Sprintf("DEVICE-%02d", n)}
}

func user(n int, u string) mdm.EnrollmentID {
	d := device(n)
	return mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: d.ID + ":" + u, ParentID: d.ID}
}

func auth(serial string) *checkin.Authenticate {
	s := serial
	return &checkin.Authenticate{Topic: "com.apple.mgmt.test", Model: "Mac", ModelName: "MacBook", DeviceName: "dev", SerialNumber: &s}
}

func push(n int) mdm.Push {
	return mdm.Push{Topic: "com.apple.mgmt.test", Token: []byte{byte(n % 256), 2, 3}, Magic: fmt.Sprintf("magic-%d", n)} // #nosec G115 -- bounded by the modulo
}

// enroll performs Authenticate and TokenUpdate for id.
func enroll(t *testing.T, s storage.Store, id mdm.EnrollmentID, n int) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertAuthenticate(ctx, id, auth(fmt.Sprintf("S%d", n)), []byte("<plist/>"), t0); err != nil {
		t.Fatalf("UpsertAuthenticate: %v", err)
	}
	short := "alice"
	if err := s.StoreTokenUpdate(ctx, id, push(n), &checkin.TokenUpdate{Topic: "com.apple.mgmt.test", UnlockToken: []byte{9}, UserShortName: &short, UserLongName: "Alice"}, nil, t0.Add(time.Second)); err != nil {
		t.Fatalf("StoreTokenUpdate: %v", err)
	}
}

func cmd(t *testing.T, id string) *mdm.Command {
	t.Helper()
	c, err := mdm.NewCommand(&commands.ProfileList{}, mdm.WithUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func result(uuid string, status mdm.Status) *mdm.Response {
	return &mdm.Response{Enrollment: mdm.Enrollment{UDID: "DEVICE-01"}, CommandUUID: uuid, Status: status}
}

// RunEnrollmentSuite covers EnrollmentStore.
func RunEnrollmentSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("Lifecycle", func(t *testing.T) {
		s := newStore(t)
		id := device(1)
		if _, err := s.Get(ctx, id); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("Get unknown: %v", err)
		}
		if err := s.StoreTokenUpdate(ctx, id, push(1), nil, nil, t0); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("TokenUpdate before Authenticate: %v", err)
		}
		if err := s.Disable(ctx, id, t0); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("Disable unknown: %v", err)
		}
		if err := s.TouchLastSeen(ctx, id, t0); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("TouchLastSeen unknown: %v", err)
		}
		if err := s.UpsertAuthenticate(ctx, id, auth("S1"), []byte("<a/>"), t0); err != nil {
			t.Fatal(err)
		}
		e, err := s.Get(ctx, id)
		if err != nil || e.Enabled || e.Device.SerialNumber != "S1" || string(e.AuthenticateRaw) != "<a/>" || !e.EnrolledAt.Equal(t0) {
			t.Fatalf("after Authenticate: %+v %v", e, err)
		}
		if err := s.StoreTokenUpdate(ctx, id, mdm.Push{}, nil, nil, t0); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("incomplete push: %v", err)
		}
		short := "bob"
		if err := s.StoreTokenUpdate(ctx, id, push(1), &checkin.TokenUpdate{UnlockToken: []byte{7}, UserShortName: &short, UserLongName: "Bob"}, nil, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		e, _ = s.Get(ctx, id)
		if !e.Enabled || !e.Push.Valid() || e.Push.Magic != "magic-1" || string(e.UnlockToken) != "\x07" || e.UserShortName != "bob" || e.UserLongName != "Bob" || !e.TokenUpdatedAt.Equal(t0.Add(time.Minute)) {
			t.Fatalf("after TokenUpdate: %+v", e)
		}
		// Returned copies do not alias store state.
		e.Push.Token[0] = 0xff
		e2, _ := s.Get(ctx, id)
		if e2.Push.Token[0] == 0xff {
			t.Fatal("Get returned aliased push token")
		}
		// Idempotent TokenUpdate.
		if err := s.StoreTokenUpdate(ctx, id, push(1), nil, nil, t0.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.TouchLastSeen(ctx, id, t0.Add(3*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.TouchLastSeen(ctx, id, t0); err != nil {
			t.Fatal(err)
		}
		e, _ = s.Get(ctx, id)
		if !e.LastSeenAt.Equal(t0.Add(3 * time.Minute)) {
			t.Fatalf("LastSeenAt = %v (must not go backwards)", e.LastSeenAt)
		}
		if err := s.Disable(ctx, id, t0.Add(4*time.Minute)); err != nil {
			t.Fatal(err)
		}
		e, _ = s.Get(ctx, id)
		if e.Enabled || !e.DisabledAt.Equal(t0.Add(4*time.Minute)) {
			t.Fatalf("after Disable: %+v", e)
		}
		// Re-enable by TokenUpdate.
		if err := s.StoreTokenUpdate(ctx, id, push(1), nil, nil, t0.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if e, _ = s.Get(ctx, id); !e.Enabled || !e.DisabledAt.IsZero() {
			t.Fatalf("after re-enable: %+v", e)
		}
		// Invalid ids are rejected.
		bad := mdm.EnrollmentID{}
		if err := s.UpsertAuthenticate(ctx, bad, nil, nil, t0); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("invalid id: %v", err)
		}
		if _, err := s.Get(ctx, bad); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("Get invalid id: %v", err)
		}
	})

	t.Run("ReenrollClearsState", func(t *testing.T) {
		s := newStore(t)
		id := device(1)
		enroll(t, s, id, 1)
		uid := user(1, "alice")
		enroll(t, s, uid, 2)
		if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd(t, "C1"), storage.EnqueueOptions{Now: t0}); err != nil {
			t.Fatal(err)
		}
		if err := s.AssociateCert(ctx, id, "hash-1", t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreBootstrapToken(ctx, id, []byte("bst"), t0); err != nil {
			t.Fatal(err)
		}
		// Re-enrollment with a new identity.
		if err := s.UpsertAuthenticate(ctx, id, auth("S1"), []byte("<b/>"), t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		e, _ := s.Get(ctx, id)
		if e.Enabled || e.Push.Valid() || e.CertHash != "" || len(e.UnlockToken) != 0 || string(e.AuthenticateRaw) != "<b/>" ||
			!e.CertHashAt.IsZero() || !e.BootstrapTokenAt.IsZero() {
			t.Fatalf("re-enrolled record keeps state: %+v", e)
		}
		if _, err := s.EnrollmentByCertHash(ctx, "hash-1"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("cert association survived re-enrollment: %v", err)
		}
		if _, err := s.BootstrapToken(ctx, id); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("bootstrap token survived re-enrollment: %v", err)
		}
		if err := s.StoreTokenUpdate(ctx, id, push(1), nil, nil, t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if next, err := s.Next(ctx, id, false, t0.Add(time.Hour)); err != nil || next != nil {
			t.Fatalf("queue survived re-enrollment: %v %v", next, err)
		}
		if u, _ := s.Get(ctx, uid); u.Enabled {
			t.Fatal("user channel stayed enabled after device re-enrollment")
		}
		cleared, _ := s.Commands(ctx, id, storage.CommandQuery{States: []storage.State{storage.StateCleared}}, storage.Page{})
		if len(cleared.Items) != 1 {
			t.Fatalf("expected the old command to be cleared, got %d", len(cleared.Items))
		}
	})

	t.Run("ManyUsersPerDevice", func(t *testing.T) {
		s := newStore(t)
		d := device(7)
		if err := s.UpsertAuthenticate(ctx, d, auth("S7"), nil, t0); err != nil {
			t.Fatal(err)
		}
		for i, u := range []string{"alice", "bob", "carol"} {
			id := user(7, u)
			if err := s.UpsertAuthenticate(ctx, id, nil, nil, t0); err != nil {
				t.Fatal(err)
			}
			short := u
			if err := s.StoreTokenUpdate(ctx, id, push(10+i), &checkin.TokenUpdate{Topic: "t", UserShortName: &short, UserLongName: u}, nil, t0); err != nil {
				t.Fatal(err)
			}
		}
		res, err := s.List(ctx, storage.EnrollmentQuery{ParentID: d.ID}, storage.Page{})
		if err != nil || len(res.Items) != 3 {
			t.Fatalf("users = %d, %v (a new UserID must never remove another)", len(res.Items), err)
		}
		for _, e := range res.Items {
			if !e.Enabled {
				t.Fatalf("user %s disabled", e.ID.ID)
			}
		}
	})
	t.Run("ReusedUserIDOnOtherDevice", func(t *testing.T) {
		s := newStore(t)
		for _, n := range []int{8, 9} {
			if err := s.UpsertAuthenticate(ctx, device(n), auth(fmt.Sprintf("S%d", n)), nil, t0); err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertAuthenticate(ctx, user(n, "shared-uid"), nil, nil, t0); err != nil {
				t.Fatal(err)
			}
		}
		a, err := s.Get(ctx, user(8, "shared-uid"))
		if err != nil || a.ID.ParentID != device(8).ID {
			t.Fatalf("user on device 8: %+v %v", a, err)
		}
		b, err := s.Get(ctx, user(9, "shared-uid"))
		if err != nil || b.ID.ParentID != device(9).ID {
			t.Fatalf("user on device 9: %+v %v (a reused UserID must be a distinct row)", b, err)
		}
	})
	t.Run("UserCheckOutDisablesOnlyUser", func(t *testing.T) {
		s := newStore(t)
		d := device(10)
		if err := s.UpsertAuthenticate(ctx, d, auth("S10"), nil, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreTokenUpdate(ctx, d, push(10), nil, nil, t0); err != nil {
			t.Fatal(err)
		}
		for i, u := range []string{"u1", "u2"} {
			if err := s.UpsertAuthenticate(ctx, user(10, u), nil, nil, t0); err != nil {
				t.Fatal(err)
			}
			if err := s.StoreTokenUpdate(ctx, user(10, u), push(20+i), nil, nil, t0); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Disable(ctx, user(10, "u1"), t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		for id, want := range map[mdm.EnrollmentID]bool{d: true, user(10, "u1"): false, user(10, "u2"): true} {
			e, err := s.Get(ctx, id)
			if err != nil || e.Enabled != want {
				t.Fatalf("%s enabled = %v, want %v (%v)", id.ID, e.Enabled, want, err)
			}
		}
	})
	t.Run("UserFieldsRoundTrip", func(t *testing.T) {
		s := newStore(t)
		if err := s.UpsertAuthenticate(ctx, device(11), auth("S11"), nil, t0); err != nil {
			t.Fatal(err)
		}
		id := user(11, "dana")
		if err := s.UpsertAuthenticate(ctx, id, nil, nil, t0); err != nil {
			t.Fatal(err)
		}
		short := "dana"
		msg := &checkin.TokenUpdate{Topic: "t", UserShortName: &short, UserLongName: "Dana D", NotOnConsole: true, EnrollmentUserID: "EU-1"}
		if err := s.StoreTokenUpdate(ctx, id, push(30), msg, nil, t0); err != nil {
			t.Fatal(err)
		}
		e, err := s.Get(ctx, id)
		if err != nil || e.UserShortName != "dana" || e.UserLongName != "Dana D" || !e.NotOnConsole || e.EnrollmentUserID != "EU-1" {
			t.Fatalf("user fields = %+v %v", e, err)
		}
		// A later TokenUpdate that omits the names keeps them and updates the console flag.
		if err := s.StoreTokenUpdate(ctx, id, push(30), &checkin.TokenUpdate{Topic: "t"}, nil, t0.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		e, err = s.Get(ctx, id)
		if err != nil || e.UserShortName != "dana" || e.NotOnConsole || e.EnrollmentUserID != "EU-1" {
			t.Fatalf("after second TokenUpdate = %+v %v", e, err)
		}
	})
	t.Run("DisableCascadesToUserChannels", func(t *testing.T) {
		s := newStore(t)
		enroll(t, s, device(1), 1)
		enroll(t, s, user(1, "alice"), 2)
		enroll(t, s, user(1, "bob"), 3)
		enroll(t, s, device(2), 4)
		enroll(t, s, user(2, "carol"), 5)
		at := t0.Add(time.Minute)
		// Disabling a user channel touches only that channel.
		if err := s.Disable(ctx, user(1, "bob"), at); err != nil {
			t.Fatal(err)
		}
		if d, _ := s.Get(ctx, device(1)); !d.Enabled {
			t.Fatal("device disabled by a user channel checkout")
		}
		if a, _ := s.Get(ctx, user(1, "alice")); !a.Enabled {
			t.Fatal("sibling user channel disabled")
		}
		// Disabling the device disables its user channels and nobody else's.
		if err := s.Disable(ctx, device(1), at); err != nil {
			t.Fatal(err)
		}
		if a, _ := s.Get(ctx, user(1, "alice")); a.Enabled || !a.DisabledAt.Equal(at) {
			t.Fatalf("user channel after device checkout: %+v", a)
		}
		if d, _ := s.Get(ctx, device(2)); !d.Enabled {
			t.Fatal("other device disabled")
		}
		if c, _ := s.Get(ctx, user(2, "carol")); !c.Enabled {
			t.Fatal("other device's user channel disabled")
		}
		// TokenUpdate on the user channel re-enables it independently.
		if err := s.StoreTokenUpdate(ctx, user(1, "alice"), push(2), nil, nil, at.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if a, _ := s.Get(ctx, user(1, "alice")); !a.Enabled {
			t.Fatal("user channel did not re-enable")
		}
		if d, _ := s.Get(ctx, device(1)); d.Enabled {
			t.Fatal("device re-enabled by its user channel")
		}
	})

	t.Run("IdempotentTokenUpdateSameInstant", func(t *testing.T) {
		s := newStore(t)
		id := device(1)
		enroll(t, s, id, 1)
		short := "alice"
		msg := &checkin.TokenUpdate{Topic: "com.apple.mgmt.test", UnlockToken: []byte{9}, UserShortName: &short, UserLongName: "Alice"}
		// A byte-identical re-send at the same instant changes no column;
		// backends that count changed rows instead of matched rows would
		// report the enrollment as missing.
		if err := s.StoreTokenUpdate(ctx, id, push(1), msg, nil, t0.Add(time.Second)); err != nil {
			t.Fatalf("identical TokenUpdate: %v", err)
		}
		if err := s.TouchLastSeen(ctx, id, t0.Add(time.Second)); err != nil {
			t.Fatalf("no-op TouchLastSeen: %v", err)
		}
		if err := s.AssociateCert(ctx, id, "h", t0); err != nil {
			t.Fatal(err)
		}
		if err := s.AssociateCert(ctx, id, "h", t0); err != nil {
			t.Fatalf("same pin at the same instant: %v", err)
		}
		if err := s.StoreBootstrapToken(ctx, id, []byte("tok"), t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreBootstrapToken(ctx, id, []byte("tok"), t0); err != nil {
			t.Fatalf("same bootstrap token at the same instant: %v", err)
		}
		if err := s.Disable(ctx, id, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.Disable(ctx, id, t0.Add(time.Minute)); err != nil {
			t.Fatalf("repeated Disable: %v", err)
		}
		if e, _ := s.Get(ctx, id); e.Enabled || !e.LastSeenAt.Equal(t0.Add(time.Second)) {
			t.Fatalf("state after idempotent writes: %+v", e)
		}
	})

	t.Run("ListPagination", func(t *testing.T) {
		s := newStore(t)
		for i := 1; i <= 5; i++ {
			enroll(t, s, device(i), i)
		}
		enroll(t, s, user(1, "u"), 9)
		if err := s.Disable(ctx, device(5), t0); err != nil {
			t.Fatal(err)
		}
		var all []string
		cursor := ""
		pages := 0
		for {
			res, err := s.List(ctx, storage.EnrollmentQuery{Channel: mdm.ChannelDevice}, storage.Page{Cursor: cursor, Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			pages++
			for _, e := range res.Items {
				all = append(all, e.ID.ID)
			}
			if res.NextCursor == "" {
				break
			}
			cursor = res.NextCursor
		}
		if pages != 3 || len(all) != 5 || all[0] != "DEVICE-01" || all[4] != "DEVICE-05" {
			t.Fatalf("pages=%d ids=%v", pages, all)
		}
		enabled := true
		res, _ := s.List(ctx, storage.EnrollmentQuery{Channel: mdm.ChannelDevice, Enabled: &enabled}, storage.Page{})
		if len(res.Items) != 4 {
			t.Fatalf("enabled devices = %d", len(res.Items))
		}
		res, _ = s.List(ctx, storage.EnrollmentQuery{ParentID: "DEVICE-01"}, storage.Page{})
		if len(res.Items) != 1 || res.Items[0].ID.Channel != mdm.ChannelUser {
			t.Fatalf("children = %+v", res.Items)
		}
		res, _ = s.List(ctx, storage.EnrollmentQuery{}, storage.Page{})
		if len(res.Items) != 6 || res.NextCursor != "" {
			t.Fatalf("all = %d next=%q", len(res.Items), res.NextCursor)
		}
	})

	// FilterBySerial backs the admin API's enrollment lookup (record 0034).
	// enroll gives device(n) the serial "S<n>".
	t.Run("FilterBySerial", func(t *testing.T) {
		s := newStore(t)
		for i := 1; i <= 3; i++ {
			enroll(t, s, device(i), i)
		}
		res, err := s.List(ctx, storage.EnrollmentQuery{Serial: "S2"}, storage.Page{})
		if err != nil {
			t.Fatalf("List by serial: %v", err)
		}
		if len(res.Items) != 1 || res.Items[0].ID != device(2) {
			t.Fatalf("serial S2 = %+v", res.Items)
		}
		if res.Items[0].Device.SerialNumber != "S2" {
			t.Fatalf("serial round trip = %q", res.Items[0].Device.SerialNumber)
		}
		res, err = s.List(ctx, storage.EnrollmentQuery{Serial: "nope"}, storage.Page{})
		if err != nil || len(res.Items) != 0 {
			t.Fatalf("unknown serial = %+v err=%v", res.Items, err)
		}
		// Serial composes with the other filters rather than replacing them.
		res, _ = s.List(ctx, storage.EnrollmentQuery{Serial: "S2", Channel: mdm.ChannelUser}, storage.Page{})
		if len(res.Items) != 0 {
			t.Fatalf("serial S2 on the user channel = %+v", res.Items)
		}
	})
}

// RunCommandQueueSuite covers CommandQueue.
func RunCommandQueueSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("OrderAndResults", func(t *testing.T) {
		s := newStore(t)
		id := device(1)
		enroll(t, s, id, 1)
		if next, err := s.Next(ctx, id, false, t0); err != nil || next != nil {
			t.Fatalf("empty queue: %v %v", next, err)
		}
		for i := 1; i <= 3; i++ {
			res, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd(t, fmt.Sprintf("C%d", i)), storage.EnqueueOptions{Now: t0.Add(time.Duration(i) * time.Second)})
			if err != nil || len(res.Queued) != 1 {
				t.Fatalf("Enqueue %d: %+v %v", i, res, err)
			}
		}
		n1, _ := s.Next(ctx, id, false, t0)
		if n1 == nil || n1.UUID != "C1" || n1.RequestType != "ProfileList" || len(n1.Raw) == 0 {
			t.Fatalf("Next = %+v", n1)
		}
		// Unacknowledged commands are re-delivered.
		if again, _ := s.Next(ctx, id, false, t0); again == nil || again.UUID != "C1" {
			t.Fatalf("re-delivery = %+v", again)
		}
		if err := s.StoreResult(ctx, id, result("C1", mdm.StatusAcknowledged), t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreResult(ctx, id, result("C1", mdm.StatusAcknowledged), t0); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("second result for a closed command: %v", err)
		}
		if err := s.StoreResult(ctx, id, result("NOPE", mdm.StatusAcknowledged), t0); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("unknown command result: %v", err)
		}
		if err := s.StoreResult(ctx, id, &mdm.Response{Status: mdm.StatusIdle}, t0); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("idle as result: %v", err)
		}
		n2, _ := s.Next(ctx, id, false, t0)
		if n2 == nil || n2.UUID != "C2" {
			t.Fatalf("after ack = %+v", n2)
		}
		// NotNow: skipped when the device just said so, retried after backoff.
		if err := s.StoreResult(ctx, id, result("C2", mdm.StatusNotNow), t0); err != nil {
			t.Fatal(err)
		}
		if n3, _ := s.Next(ctx, id, true, t0); n3 == nil || n3.UUID != "C3" {
			t.Fatalf("skipNotNow = %+v", n3)
		}
		if n, _ := s.Next(ctx, id, false, t0.Add(10*time.Second)); n == nil || n.UUID != "C3" {
			t.Fatalf("before backoff = %+v", n)
		}
		if n, _ := s.Next(ctx, id, false, t0.Add(31*time.Second)); n == nil || n.UUID != "C2" {
			t.Fatalf("after backoff = %+v", n)
		}
		// Second NotNow doubles the backoff.
		if err := s.StoreResult(ctx, id, result("C2", mdm.StatusNotNow), t0.Add(31*time.Second)); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.Next(ctx, id, false, t0.Add(80*time.Second)); n == nil || n.UUID != "C3" {
			t.Fatalf("within doubled backoff = %+v", n)
		}
		if n, _ := s.Next(ctx, id, false, t0.Add(100*time.Second)); n == nil || n.UUID != "C2" {
			t.Fatalf("after doubled backoff = %+v", n)
		}
		// Error is terminal and keeps the result.
		errResp := result("C2", mdm.StatusError)
		errResp.ErrorChain = []mdm.ErrorChainItem{{ErrorCode: 1, ErrorDomain: "d"}}
		if err := s.StoreResult(ctx, id, errResp, t0.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreResult(ctx, id, result("C3", mdm.StatusCommandFormatError), t0.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.Next(ctx, id, false, t0.Add(time.Hour)); n != nil {
			t.Fatalf("queue should be drained, got %+v", n)
		}
		res, err := s.Commands(ctx, id, storage.CommandQuery{}, storage.Page{})
		if err != nil || len(res.Items) != 3 || res.Items[0].Command.UUID != "C3" {
			t.Fatalf("Commands = %+v %v", res, err)
		}
		var c2 storage.QueuedCommand
		for _, c := range res.Items {
			if c.Command.UUID == "C2" {
				c2 = c
			}
		}
		if c2.State != storage.StateError || c2.NotNowCount != 2 || c2.Attempts != 3 || c2.Result == nil || len(c2.Result.ErrorChain) != 1 || c2.CompletedAt.IsZero() {
			t.Fatalf("C2 = %+v", c2)
		}
		res, _ = s.Commands(ctx, id, storage.CommandQuery{States: []storage.State{storage.StateAcknowledged}}, storage.Page{})
		if len(res.Items) != 1 || res.Items[0].Command.UUID != "C1" {
			t.Fatalf("filtered = %+v", res.Items)
		}
		res, _ = s.Commands(ctx, id, storage.CommandQuery{RequestType: "Nope"}, storage.Page{})
		if len(res.Items) != 0 {
			t.Fatal("RequestType filter")
		}
		// Pagination of results.
		first, _ := s.Commands(ctx, id, storage.CommandQuery{}, storage.Page{Limit: 2})
		if len(first.Items) != 2 || first.NextCursor == "" {
			t.Fatalf("page 1 = %+v", first)
		}
		second, _ := s.Commands(ctx, id, storage.CommandQuery{}, storage.Page{Limit: 2, Cursor: first.NextCursor})
		if len(second.Items) != 1 || second.NextCursor != "" {
			t.Fatalf("page 2 = %+v", second)
		}
		if _, err := s.Commands(ctx, id, storage.CommandQuery{}, storage.Page{Cursor: "bogus"}); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("bad cursor: %v", err)
		}
		if _, err := s.Commands(ctx, device(9), storage.CommandQuery{}, storage.Page{}); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("unknown enrollment: %v", err)
		}
	})

	t.Run("EnqueueTargets", func(t *testing.T) {
		s := newStore(t)
		enroll(t, s, device(1), 1)
		if err := s.UpsertAuthenticate(ctx, device(2), auth("S2"), nil, t0); err != nil {
			t.Fatal(err)
		}
		res, err := s.Enqueue(ctx, []mdm.EnrollmentID{device(1), device(2), device(3)}, cmd(t, "C1"), storage.EnqueueOptions{DedupeKey: "k", Now: t0})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Queued) != 1 || !errors.Is(res.Skipped[device(2)], storage.ErrDisabled) || !errors.Is(res.Skipped[device(3)], storage.ErrNotFound) {
			t.Fatalf("Enqueue = %+v", res)
		}
		res, _ = s.Enqueue(ctx, []mdm.EnrollmentID{device(1)}, cmd(t, "C2"), storage.EnqueueOptions{DedupeKey: "k", Now: t0})
		if len(res.Queued) != 0 || !errors.Is(res.Skipped[device(1)], storage.ErrConflict) {
			t.Fatalf("dedupe = %+v", res)
		}
		if _, err := s.Next(ctx, device(1), false, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreResult(ctx, device(1), result("C1", mdm.StatusAcknowledged), t0); err != nil {
			t.Fatal(err)
		}
		res, _ = s.Enqueue(ctx, []mdm.EnrollmentID{device(1)}, cmd(t, "C2"), storage.EnqueueOptions{DedupeKey: "k"})
		if len(res.Queued) != 1 {
			t.Fatalf("dedupe after completion = %+v", res)
		}
		if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{device(1)}, &mdm.Command{}, storage.EnqueueOptions{}); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("invalid command: %v", err)
		}
		if _, err := s.Next(ctx, device(9), false, t0); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("Next unknown: %v", err)
		}
		if err := s.StoreResult(ctx, device(9), result("C1", mdm.StatusAcknowledged), t0); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("StoreResult unknown: %v", err)
		}
	})

	t.Run("ClearFilter", func(t *testing.T) {
		s := newStore(t)
		id := device(1)
		enroll(t, s, id, 1)
		lock, _ := mdm.NewCommand(&commands.DeviceLock{}, mdm.WithUUID("L1"))
		for i, c := range []*mdm.Command{cmd(t, "C1"), cmd(t, "C2"), lock, cmd(t, "C4")} {
			if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, c, storage.EnqueueOptions{Now: t0.Add(time.Duration(i) * time.Minute)}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Next(ctx, id, false, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreResult(ctx, id, result("C1", mdm.StatusNotNow), t0); err != nil {
			t.Fatal(err)
		}
		n, err := s.Clear(ctx, id, storage.ClearFilter{RequestType: "DeviceLock"})
		if err != nil || n != 1 {
			t.Fatalf("Clear by type = %d %v", n, err)
		}
		n, _ = s.Clear(ctx, id, storage.ClearFilter{States: []storage.State{storage.StateNotNow}})
		if n != 1 {
			t.Fatalf("Clear by state = %d", n)
		}
		n, _ = s.Clear(ctx, id, storage.ClearFilter{Before: t0.Add(90 * time.Second)})
		if n != 1 {
			t.Fatalf("Clear before = %d", n)
		}
		n, _ = s.Clear(ctx, id, storage.ClearFilter{})
		if n != 1 {
			t.Fatalf("Clear rest = %d", n)
		}
		if next, _ := s.Next(ctx, id, false, t0.Add(time.Hour)); next != nil {
			t.Fatalf("cleared command delivered: %+v", next)
		}
		if _, err := s.Clear(ctx, device(9), storage.ClearFilter{}); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("Clear unknown: %v", err)
		}
	})
}

// RunPushSuite covers PushStore.
func RunPushSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	enroll(t, s, device(1), 1)
	enroll(t, s, device(2), 2)
	if err := s.Disable(ctx, device(2), t0); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAuthenticate(ctx, device(3), auth("S3"), nil, t0); err != nil {
		t.Fatal(err)
	}
	info, err := s.PushInfo(ctx, []mdm.EnrollmentID{device(1), device(2), device(3), device(4)})
	if err != nil {
		t.Fatal(err)
	}
	if len(info) != 1 || info[device(1)].Magic != "magic-1" || info[device(1)].Token[0] != 1 {
		t.Fatalf("PushInfo = %+v", info)
	}
	info[device(1)].Token[0] = 0xff
	again, _ := s.PushInfo(ctx, []mdm.EnrollmentID{device(1)})
	if again[device(1)].Token[0] == 0xff {
		t.Fatal("PushInfo returned aliased token")
	}
}

// RunCertAuthSuite covers CertAuthStore.
func RunCertAuthSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	enroll(t, s, device(1), 1)
	enroll(t, s, device(2), 2)
	if err := s.AssociateCert(ctx, device(9), "h", t0); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if err := s.AssociateCert(ctx, device(1), "", t0); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("empty hash: %v", err)
	}
	if h, err := s.CertHash(ctx, device(1)); err != nil || h != "" {
		t.Fatalf("CertHash before = %q %v", h, err)
	}
	if _, err := s.CertHash(ctx, device(9)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("CertHash unknown: %v", err)
	}
	if err := s.AssociateCert(ctx, device(1), "h1", t0); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Get(ctx, device(1)); !e.CertHashAt.Equal(t0) {
		t.Fatalf("CertHashAt = %v, want %v", e.CertHashAt, t0)
	}
	if err := s.AssociateCert(ctx, device(1), "h1", t0.Add(time.Minute)); err != nil {
		t.Fatalf("re-associating the same hash: %v", err)
	}
	if e, _ := s.Get(ctx, device(1)); !e.CertHashAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("CertHashAt after re-pin = %v", e.CertHashAt)
	}
	if err := s.AssociateCert(ctx, device(2), "h1", t0); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if id, err := s.EnrollmentByCertHash(ctx, "h1"); err != nil || id != device(1) {
		t.Fatalf("EnrollmentByCertHash = %+v %v", id, err)
	}
	// User channel resolves through its device.
	if err := s.AssociateCert(ctx, user(1, "u"), "h2", t0); err != nil {
		t.Fatalf("user channel associate: %v", err)
	}
	if h, _ := s.CertHash(ctx, user(1, "u")); h != "h2" {
		t.Fatalf("user channel hash = %q", h)
	}
	if _, err := s.EnrollmentByCertHash(ctx, "h1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("old hash still resolves: %v", err)
	}
	if e, _ := s.Get(ctx, device(1)); e.CertHash != "h2" || !e.CertHashAt.Equal(t0) {
		t.Fatalf("record hash = %q at %v", e.CertHash, e.CertHashAt)
	}
	if _, err := s.EnrollmentByCertHash(ctx, "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("unknown hash: %v", err)
	}

	t.Run("HistoryAppendOnly", func(t *testing.T) {
		s := newStore(t)
		enroll(t, s, device(1), 1)
		if err := s.AssociateCert(ctx, device(1), "h1", t0); err != nil {
			t.Fatal(err)
		}
		if err := s.AssociateCert(ctx, device(1), "h2", t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		// Re-pinning a hash keeps its first-seen time.
		if err := s.AssociateCert(ctx, device(1), "h1", t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		h, err := s.CertHistory(ctx, device(1))
		if err != nil || len(h) != 2 || h[0].Hash != "h1" || !h[0].At.Equal(t0) || h[1].Hash != "h2" || !h[1].At.Equal(t0.Add(time.Minute)) || h[0].ID != device(1) {
			t.Fatalf("history = %+v %v", h, err)
		}
		// Re-enrollment clears the pin but keeps the history.
		if err := s.UpsertAuthenticate(ctx, device(1), auth("S1"), nil, t0.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if hash, _ := s.CertHash(ctx, device(1)); hash != "" {
			t.Fatalf("pin survived re-enrollment: %q", hash)
		}
		if again, _ := s.CertHistory(ctx, device(1)); len(again) != 2 {
			t.Fatalf("history after re-enrollment = %+v", again)
		}
	})

	t.Run("HashHistoryAcrossEnrollments", func(t *testing.T) {
		s := newStore(t)
		enroll(t, s, device(1), 1)
		enroll(t, s, device(2), 2)
		if err := s.AssociateCert(ctx, device(1), "h1", t0); err != nil {
			t.Fatal(err)
		}
		if err := s.AssociateCert(ctx, device(1), "h2", t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		// h1 is no longer pinned, so the store lets another enrollment take
		// it; the service decides whether that reuse is allowed.
		if err := s.AssociateCert(ctx, device(2), "h1", t0.Add(2*time.Minute)); err != nil {
			t.Fatalf("reuse of an unpinned hash: %v", err)
		}
		h, err := s.CertHashHistory(ctx, "h1")
		if err != nil || len(h) != 2 || h[0].ID != device(1) || h[1].ID != device(2) || !h[1].At.Equal(t0.Add(2*time.Minute)) {
			t.Fatalf("hash history = %+v %v", h, err)
		}
	})

	t.Run("HistoryUnknownAndEmpty", func(t *testing.T) {
		s := newStore(t)
		enroll(t, s, device(1), 1)
		if _, err := s.CertHistory(ctx, device(9)); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("unknown enrollment: %v", err)
		}
		if h, err := s.CertHistory(ctx, device(1)); err != nil || len(h) != 0 {
			t.Fatalf("never pinned = %+v %v", h, err)
		}
		if h, err := s.CertHashHistory(ctx, "nope"); err != nil || len(h) != 0 {
			t.Fatalf("unseen hash = %+v %v", h, err)
		}
		if _, err := s.CertHashHistory(ctx, ""); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("empty hash: %v", err)
		}
		if _, err := s.CertHistory(ctx, mdm.EnrollmentID{}); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("invalid id: %v", err)
		}
	})

	t.Run("HistoryViaUserChannel", func(t *testing.T) {
		s := newStore(t)
		enroll(t, s, device(1), 1)
		if err := s.AssociateCert(ctx, user(1, "u"), "h1", t0); err != nil {
			t.Fatal(err)
		}
		h, err := s.CertHistory(ctx, user(1, "u"))
		if err != nil || len(h) != 1 || h[0].ID != device(1) {
			t.Fatalf("history through the user channel = %+v %v", h, err)
		}
	})
}

// RunBootstrapTokenSuite covers BootstrapTokenStore.
func RunBootstrapTokenSuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	enroll(t, s, device(1), 1)
	if _, err := s.BootstrapToken(ctx, device(1)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("before store: %v", err)
	}
	if err := s.StoreBootstrapToken(ctx, device(9), []byte("x"), t0); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.BootstrapToken(ctx, device(9)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("unknown get: %v", err)
	}
	if err := s.StoreBootstrapToken(ctx, device(1), nil, t0); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("empty token: %v", err)
	}
	if err := s.StoreBootstrapToken(ctx, user(1, "u"), []byte("tok"), t0); err != nil {
		t.Fatal(err)
	}
	tok, err := s.BootstrapToken(ctx, device(1))
	if err != nil || string(tok) != "tok" {
		t.Fatalf("BootstrapToken = %q %v", tok, err)
	}
	tok[0] = 'X'
	if again, _ := s.BootstrapToken(ctx, device(1)); string(again) != "tok" {
		t.Fatal("aliased bootstrap token")
	}
	if e, _ := s.Get(ctx, device(1)); !e.BootstrapTokenAt.Equal(t0) {
		t.Fatalf("BootstrapTokenAt = %v, want %v", e.BootstrapTokenAt, t0)
	}
	if err := s.StoreBootstrapToken(ctx, device(1), []byte("tok2"), t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Get(ctx, device(1)); !e.BootstrapTokenAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("BootstrapTokenAt after overwrite = %v", e.BootstrapTokenAt)
	}
	if tok, _ := s.BootstrapToken(ctx, device(1)); string(tok) != "tok2" {
		t.Fatalf("overwritten token = %q", tok)
	}
}

// RunConcurrencySuite hammers the store from many goroutines; run with -race.
func RunConcurrencySuite(t *testing.T, newStore Factory) {
	t.Helper()
	ctx := context.Background()

	t.Run("QueueHammer", func(t *testing.T) {
		s := newStore(t)
		id := device(1)
		enroll(t, s, id, 1)
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Go(func() {
				c := cmd(t, fmt.Sprintf("P%02d", i))
				if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, c, storage.EnqueueOptions{Now: t0}); err != nil {
					t.Error(err)
				}
				if _, err := s.Next(ctx, id, false, t0); err != nil {
					t.Error(err)
				}
				if err := s.TouchLastSeen(ctx, id, t0); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		acked := 0
		for {
			n, err := s.Next(ctx, id, false, t0)
			if err != nil {
				t.Fatal(err)
			}
			if n == nil {
				break
			}
			if err := s.StoreResult(ctx, id, result(n.UUID, mdm.StatusAcknowledged), t0); err != nil {
				t.Fatal(err)
			}
			acked++
		}
		if acked != 20 {
			t.Fatalf("acknowledged %d of 20", acked)
		}
	})

	// CertPinRace has several enrollments race to pin one certificate hash:
	// exactly one wins and every loser sees ErrConflict, never a raw driver
	// error from the unique index.
	t.Run("CertPinRace", func(t *testing.T) {
		s := newStore(t)
		const n = 8
		for i := 1; i <= n; i++ {
			enroll(t, s, device(i), i)
		}
		errs := make([]error, n+1)
		var wg sync.WaitGroup
		for i := 1; i <= n; i++ {
			wg.Go(func() {
				errs[i] = s.AssociateCert(ctx, device(i), "shared-hash", t0)
			})
		}
		wg.Wait()
		winners := 0
		for i := 1; i <= n; i++ {
			switch {
			case errs[i] == nil:
				winners++
			case errors.Is(errs[i], storage.ErrConflict):
			default:
				t.Fatalf("device %d: raw error %v", i, errs[i])
			}
		}
		if winners != 1 {
			t.Fatalf("winners = %d, want 1 (%v)", winners, errs[1:])
		}
		owner, err := s.EnrollmentByCertHash(ctx, "shared-hash")
		if err != nil {
			t.Fatal(err)
		}
		if h, _ := s.CertHash(ctx, owner); h != "shared-hash" {
			t.Fatalf("owner %s has hash %q", owner.ID, h)
		}
	})
}
