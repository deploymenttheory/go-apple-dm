package storagetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// RunSecuritySuite verifies identity isolation and atomic lifecycle transitions.
func RunSecuritySuite(t *testing.T, factory Factory) {
	t.Helper()
	ctx := context.Background()
	t.Run("UserHandshakeReservesCanonicalIdentity", func(t *testing.T) {
		s := factory(t)
		parent := device(1)
		enroll(t, s, parent, 1)
		child := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "contested", ParentID: parent.ID}
		device := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: child.ID}
		results := make(chan error, 2)
		start := make(chan struct{})
		go func() { <-start; results <- s.StoreUserAuthChallenge(ctx, child, "challenge", nil, t0) }()
		go func() { <-start; results <- s.UpsertAuthenticate(ctx, device, &checkin.Authenticate{}, nil, t0) }()
		close(start)
		first, second := <-results, <-results
		if (first == nil) == (second == nil) {
			t.Fatalf("one identity must win: %v, %v", first, second)
		}
		canonical, err := s.EnrollmentByID(ctx, child.ID)
		if err != nil {
			t.Fatal(err)
		}
		if canonical.ID == child {
			if err = s.UpsertAuthenticate(
				ctx,
				device,
				&checkin.Authenticate{},
				nil,
				t0,
			); err == nil {
				t.Fatal("device overwrote reserved user identity")
			}
		} else if err = s.StoreUserAuthChallenge(ctx, child, "changed", nil, t0); err == nil {
			t.Fatal("user overwrote device identity")
		}
	})

	t.Run("CapabilitiesRequireTrackedDeviceResponse", func(t *testing.T) {
		s := factory(t)
		id := device(1)
		enroll(t, s, id, 1)
		cmd := &mdm.Command{UUID: "security", RequestType: "SecurityInfo"}
		if _, err := s.Enqueue(
			ctx,
			[]mdm.EnrollmentID{id},
			cmd,
			storage.EnqueueOptions{Now: t0},
		); err != nil {
			t.Fatal(err)
		}
		raw, _ := plist.Marshal(
			map[string]any{
				"UDID":        id.ID,
				"CommandUUID": cmd.UUID,
				"Status":      "Acknowledged",
				"SecurityInfo": map[string]any{
					"ManagementStatus": map[string]any{
						"EnrolledViaDEP":         true,
						"UserApprovedEnrollment": false,
					},
				},
			},
		)
		resp, err := mdm.DecodeResponse(raw, "")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.StoreResult(ctx, id, resp, t0); err != nil {
			t.Fatal(err)
		}
		e, _ := s.Get(ctx, id)
		if e.Capabilities.DEP != storage.CapabilityTrue ||
			e.Capabilities.UserApproved != storage.CapabilityFalse ||
			e.Capabilities.Supervised != storage.CapabilityUnknown ||
			e.Capabilities.Source != "device:SecurityInfo" {
			t.Fatal(e.Capabilities)
		}
		resp.CommandUUID = "unknown"
		if err = s.StoreResult(ctx, id, resp, t0); !errors.Is(err, storage.ErrNotFound) {
			t.Fatal(err)
		}
		if err = s.Disable(ctx, id, t0); err != nil {
			t.Fatal(err)
		}
		if _, err = s.BootstrapToken(ctx, id); !errors.Is(err, storage.ErrDisabled) {
			t.Fatal("disabled secret read", err)
		}
		if err = s.StoreBootstrapToken(
			ctx,
			id,
			[]byte("secret"),
			t0,
		); !errors.Is(
			err,
			storage.ErrDisabled,
		) {
			t.Fatal("disabled secret write", err)
		}
	})
	t.Run("CanonicalIdentity", func(t *testing.T) {
		s := factory(t)
		enroll(t, s, device(1), 1)
		enroll(t, s, device(2), 2)
		enroll(t, s, user(1, "alice"), 1)
		wrongDevice := mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: device(1).ID}
		wrongParent := user(1, "alice")
		wrongParent.ParentID = device(2).ID
		wrongUser := user(1, "alice")
		wrongUser.Channel = mdm.ChannelSharedIPadUser
		for _, id := range []mdm.EnrollmentID{wrongDevice, wrongParent, wrongUser} {
			ops := []func() error{
				func() error { _, err := s.Get(ctx, id); return err },
				func() error { return s.TouchLastSeen(ctx, id, t0) },
				func() error { return s.Disable(ctx, id, t0) },
				func() error { return s.StoreTokenUpdate(ctx, id, push(1), nil, nil, t0) },
				func() error { _, err := s.Next(ctx, id, false, t0); return err },
				func() error { _, err := s.Commands(ctx, id, storage.CommandQuery{}, paging.Page{}); return err },
				func() error { _, err := s.Clear(ctx, id, storage.ClearFilter{}); return err },
				func() error { _, err := s.CertHash(ctx, id); return err },
				func() error { return s.StoreBootstrapToken(ctx, id, []byte("secret"), t0) },
				func() error { _, err := s.BootstrapToken(ctx, id); return err },
			}
			for i, op := range ops {
				if err := op(); !errors.Is(err, storage.ErrNotFound) {
					t.Fatalf("%v operation %d: %v", id, i, err)
				}
			}
			if err := s.UpsertAuthenticate(
				ctx,
				id,
				nil,
				nil,
				t0,
			); !errors.Is(
				err,
				storage.ErrConflict,
			) {
				t.Fatalf("colliding upsert: %v", err)
			}
			if err := s.Import(
				ctx,
				storage.EnrollmentExport{
					Enrollment: storage.Enrollment{ID: id, EnrolledAt: t0, LastSeenAt: t0},
				},
			); !errors.Is(
				err,
				storage.ErrConflict,
			) {
				t.Fatalf("colliding import: %v", err)
			}
			info, err := s.PushInfo(ctx, []mdm.EnrollmentID{id})
			if err != nil || len(info) != 0 {
				t.Fatalf("push leaked: %v %v", info, err)
			}
		}
		for _, id := range []mdm.EnrollmentID{device(1), device(2), user(1, "alice")} {
			e, err := s.Get(ctx, id)
			if err != nil || !e.Enabled {
				t.Fatalf("rejected request mutated %v: %+v %v", id, e, err)
			}
		}
	})
	t.Run("ConcurrentPinAndRollback", func(t *testing.T) {
		s := factory(t)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, hash := range []string{"identity-a", "identity-b"} {
			wg.Go(func() {
				<-start
				results <- s.AuthenticateEnrollment(ctx, device(1), storage.AuthenticateChange{Hash: hash, At: t0})
			})
		}
		close(start)
		wg.Wait()
		close(results)
		wins := 0
		for err := range results {
			if err == nil {
				wins++
			} else if !errors.Is(err, storage.ErrConflict) {
				t.Fatalf("authentication: %v", err)
			}
		}
		if wins != 1 {
			t.Fatalf("successful competing identities: %d", wins)
		}
		e, err := s.Get(ctx, device(1))
		if err != nil || e.CertHash == "" {
			t.Fatalf("unpinned result: %+v %v", e, err)
		}
		if err := s.AuthenticateEnrollment(
			ctx,
			device(2),
			storage.AuthenticateChange{Hash: e.CertHash, AllowReuse: true, At: t0},
		); !errors.Is(
			err,
			storage.ErrConflict,
		) {
			t.Fatalf("live certificate reuse: %v", err)
		}
		if _, err := s.Get(ctx, device(2)); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("failed issuance left enrollment: %v", err)
		}
	})
	t.Run("RetryAndDisable", func(t *testing.T) {
		s := factory(t)
		id := device(1)
		c := storage.AuthenticateChange{Hash: "identity", Raw: []byte("original"), At: t0}
		if err := s.AuthenticateEnrollment(ctx, id, c); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreTokenUpdate(ctx, id, push(1), nil, nil, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreBootstrapToken(ctx, id, []byte("escrow"), t0); err != nil {
			t.Fatal(err)
		}
		cmd := &mdm.Command{
			UUID:        "command",
			RequestType: "DeviceInformation",
			Raw:         []byte("payload"),
		}
		if _, err := s.Enqueue(
			ctx,
			[]mdm.EnrollmentID{id},
			cmd,
			storage.EnqueueOptions{Now: t0},
		); err != nil {
			t.Fatal(err)
		}
		c.At = t0.Add(time.Minute)
		if err := s.AuthenticateEnrollment(ctx, id, c); err != nil {
			t.Fatal(err)
		}
		e, _ := s.Get(ctx, id)
		if !e.Enabled || !e.EnrolledAt.Equal(t0) {
			t.Fatalf("retry reset state: %+v", e)
		}
		if b, err := s.BootstrapToken(ctx, id); err != nil || string(b) != "escrow" {
			t.Fatalf("retry lost escrow: %q %v", b, err)
		}
		if err := s.Disable(ctx, id, c.At); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Next(ctx, id, false, c.At); !errors.Is(err, storage.ErrDisabled) {
			t.Fatalf("disabled delivery: %v", err)
		}
		if err := s.StoreTokenUpdate(
			ctx,
			id,
			push(1),
			nil,
			nil,
			c.At,
		); !errors.Is(
			err,
			storage.ErrDisabled,
		) {
			t.Fatalf("reactivation: %v", err)
		}
		if err := s.AuthenticateEnrollment(ctx, id, c); !errors.Is(err, storage.ErrDisabled) {
			t.Fatalf("disabled authentication: %v", err)
		}
		q, err := s.Commands(ctx, id, storage.CommandQuery{}, paging.Page{})
		if err != nil || len(q.Items) != 1 || q.Items[0].State != storage.StateCleared {
			t.Fatalf("disable queue: %+v %v", q, err)
		}
	})
}
