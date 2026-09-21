package ddmsync_test

import (
	"context"
	"crypto/x509"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/dmhook"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	ddminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
	mdminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	ddmsql "github.com/deploymenttheory/go-apple-dm/server/ddmstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/ddmsync"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// lifecycleCertificate creates an identity for check-in pinning tests.
func lifecycleCertificate(t *testing.T) *x509.Certificate {
	t.Helper()
	ca, err := testpki.NewCA("lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ca.Issue("device", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return id.Cert
}

// lifecycleMessage builds a real decoded device or user check-in.
func lifecycleMessage(t *testing.T, id mdm.EnrollmentID, kind string) *mdm.Checkin {
	t.Helper()
	fields := map[string]any{"MessageType": kind, "UDID": id.Device().ID, "Topic": "com.apple.mgmt.test"}
	if id.Channel.IsUser() {
		fields["UserID"] = strings.TrimPrefix(id.ID, id.ParentID+":")
	}
	raw, err := plist.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	ck, err := mdm.DecodeCheckin(raw)
	if err != nil {
		t.Fatal(err)
	}
	return ck
}

// lifecycleCore composes the real service, DDM engine and required cleanup hook.
func lifecycleCore(t *testing.T, ms storage.Store, ds ddm.Store, bus event.Publisher, hooks ...service.Hook) (*service.Core, *ddm.Engine) {
	t.Helper()
	engine, err := ddm.New(ddm.Config{Store: ds, Clock: clock.NewFake(t0)})
	if err != nil {
		t.Fatal(err)
	}
	core, err := service.New(service.Config{
		Store: ms, Bus: bus, Clock: clock.NewFake(t0.Add(time.Minute)),
		Hooks:    append([]service.Hook{ddmsync.NewServiceHook(engine, ms, nil)}, hooks...),
		Reenroll: func(context.Context, *mdm.Request, *storage.Enrollment) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.PutSet(t.Context(), "lab"); err != nil {
		t.Fatal(err)
	}
	return core, engine
}

// requireMembership checks whether the lifecycle retained or cleared an assignment.
func requireMembership(t *testing.T, engine *ddm.Engine, id mdm.EnrollmentID, present bool) {
	t.Helper()
	sets, err := engine.EnrollmentSets(t.Context(), id)
	if err != nil || (len(sets) == 1 && sets[0] == "lab") != present || len(sets) > 1 {
		t.Fatalf("%s membership: %v %v; present=%v", id.ID, sets, err, present)
	}
}

type delayedAuthenticate struct {
	storage.Store
	delayed atomic.Bool
	entered chan struct{}
	release chan struct{}
}

// AuthenticateEnrollment pauses the first invocation after the core's policy read.
func (s *delayedAuthenticate) AuthenticateEnrollment(ctx context.Context, id mdm.EnrollmentID, c storage.AuthenticateChange) error {
	if s.delayed.CompareAndSwap(false, true) {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.AuthenticateEnrollment(ctx, id, c)
}

// TestConcurrentAuthenticatePreservesLaterAssignments forces a stale policy read:
// the delayed request becomes a storage retry after another request completes.
func TestConcurrentAuthenticatePreservesLaterAssignments(t *testing.T) {
	for _, mode := range []string{"initial", "rotation"} {
		t.Run(mode, func(t *testing.T) {
			ms := &delayedAuthenticate{Store: mdminmem.New(), entered: make(chan struct{}), release: make(chan struct{})}
			bus := event.New()
			var enrolled, reenrolled, rotated atomic.Int32
			bus.Subscribe(event.All, func(_ context.Context, e event.Event) error {
				switch e.Type {
				case event.Enrolled:
					enrolled.Add(1)
				case event.Reenrolled:
					reenrolled.Add(1)
				case event.CertRotated:
					rotated.Add(1)
				}
				return nil
			})
			core, engine := lifecycleCore(t, ms, ddminmem.New(), bus)
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
			cert := lifecycleCertificate(t)
			if mode == "rotation" {
				old := lifecycleCertificate(t)
				if err := ms.Store.AuthenticateEnrollment(t.Context(), id, storage.AuthenticateChange{Hash: cms.Fingerprint(old), At: t0}); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			message := lifecycleMessage(t, id, "Authenticate")
			finished := make(chan error, 1)
			go func() {
				_, err := core.Checkin(ctx, &mdm.Request{Certificate: cert}, message)
				finished <- err
			}()
			<-ms.entered
			if _, err := core.Checkin(ctx, &mdm.Request{Certificate: cert}, lifecycleMessage(t, id, "Authenticate")); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.AssignSet(ctx, id, "lab"); err != nil {
				t.Fatal(err)
			}
			close(ms.release)
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			requireMembership(t, engine, id, true)
			if mode == "initial" && (enrolled.Load() != 1 || reenrolled.Load() != 1 || rotated.Load() != 0) ||
				mode == "rotation" && (enrolled.Load() != 0 || reenrolled.Load() != 2 || rotated.Load() != 1) {
				t.Fatalf("duplicate lifecycle events: enrolled=%d reenrolled=%d rotated=%d", enrolled.Load(), reenrolled.Load(), rotated.Load())
			}
		})
	}
}

type firstLookupFailure struct {
	storage.Store
	failed atomic.Bool
	err    error
}

// Get fails only the first lookup, exposing hooks that incorrectly ignore it.
func (s *firstLookupFailure) Get(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	if s.failed.CompareAndSwap(false, true) {
		return nil, s.err
	}
	return s.Store.Get(ctx, id)
}

// TestAuthenticateLookupFailurePreservesDDM requires a storage error, rather than
// a policy veto or a successful retry that accidentally deletes assignments.
func TestAuthenticateLookupFailurePreservesDDM(t *testing.T) {
	ms := &firstLookupFailure{Store: mdminmem.New(), err: errors.New("lookup unavailable")}
	core, engine := lifecycleCore(t, ms, ddminmem.New(), nil)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	cert := lifecycleCertificate(t)
	if err := ms.AuthenticateEnrollment(t.Context(), id, storage.AuthenticateChange{Hash: cms.Fingerprint(cert), At: t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AssignSet(t.Context(), id, "lab"); err != nil {
		t.Fatal(err)
	}
	result, err := core.Checkin(t.Context(), &mdm.Request{Certificate: cert}, lifecycleMessage(t, id, "Authenticate"))
	if !errors.Is(err, ms.err) || service.CodeOf(err) != service.CodeInternal || result != nil {
		t.Fatalf("lookup failure: %+v %v", result, err)
	}
	requireMembership(t, engine, id, true)
	if _, err := core.Checkin(t.Context(), &mdm.Request{Certificate: cert}, lifecycleMessage(t, id, "Authenticate")); err != nil {
		t.Fatal(err)
	}
	requireMembership(t, engine, id, true)
}

type legacyAuthenticate struct{ storage.Store }

// AuthenticateEnrollment models a custom store that does not report outcomes.
func (s legacyAuthenticate) AuthenticateEnrollment(ctx context.Context, id mdm.EnrollmentID, c storage.AuthenticateChange) error {
	c.Result = nil
	return s.Store.AuthenticateEnrollment(ctx, id, c)
}

type authenticationObserver struct{ result *dmhook.AuthenticateResult }

// Before leaves authorization and storage behavior to the service.
func (h *authenticationObserver) Before(ctx context.Context, _ *service.Call) (context.Context, error) {
	return ctx, nil
}

// After records no state because the test observes successful completion only.
func (h *authenticationObserver) After(context.Context, *service.Call, error) {}

// Complete captures the classification supplied to lifecycle hooks.
func (h *authenticationObserver) Complete(_ context.Context, c *service.Call) error {
	h.result = c.Authenticate
	return nil
}

// TestLegacyAuthenticateFallback preserves sequential compatibility while
// identifying its pre-read classification as non-authoritative.
func TestLegacyAuthenticateFallback(t *testing.T) {
	ms := legacyAuthenticate{Store: mdminmem.New()}
	observer := &authenticationObserver{}
	core, engine := lifecycleCore(t, ms, ddminmem.New(), nil, observer)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	old, fresh := lifecycleCertificate(t), lifecycleCertificate(t)
	for _, step := range []struct {
		cert  *x509.Certificate
		reset bool
	}{{old, true}, {old, false}, {fresh, true}} {
		if _, err := engine.AssignSet(t.Context(), id, "lab"); err != nil {
			t.Fatal(err)
		}
		if _, err := core.Checkin(t.Context(), &mdm.Request{Certificate: step.cert}, lifecycleMessage(t, id, "Authenticate")); err != nil {
			t.Fatal(err)
		}
		if observer.result == nil || observer.result.Known || observer.result.Reset != step.reset {
			t.Fatalf("legacy classification claimed certainty: %+v", observer.result)
		}
		requireMembership(t, engine, id, !step.reset)
	}
}

// TestSQLLifecycleCleanupRollback checks failures after earlier child clears and
// after earlier DELETEs within one clear, together with the event transaction.
func TestSQLLifecycleCleanupRollback(t *testing.T) {
	for _, tc := range []struct{ name, kind, target, failed string }{
		{"AuthenticateSecondChild", "Authenticate", "device", "device:b"},
		{"AuthenticateLateDeviceClear", "Authenticate", "device", "device"},
		{"CheckOutSecondChild", "CheckOut", "device", "device:b"},
		{"UserCheckOut", "CheckOut", "device:a", "device:a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			ms, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "lifecycle.sqlite"), sqlite.Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ms.Close() })
			ds, err := ddmsql.Open(ctx, ms.DB(), sqlite.Dialect, ddmsql.Options{})
			if err != nil {
				t.Fatal(err)
			}
			es, err := eventstore.Open(ctx, ms.DB(), sqlite.Dialect)
			if err != nil {
				t.Fatal(err)
			}
			core, engine := lifecycleCore(t, ms, ds, &eventstore.Publisher{Store: es})
			device := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
			ids := []mdm.EnrollmentID{device, {Channel: mdm.ChannelUser, ID: "device:a", ParentID: "device"}, {Channel: mdm.ChannelUser, ID: "device:b", ParentID: "device"}}
			old, fresh := lifecycleCertificate(t), lifecycleCertificate(t)
			if err := ms.AuthenticateEnrollment(ctx, device, storage.AuthenticateChange{Hash: cms.Fingerprint(old), At: t0}); err != nil {
				t.Fatal(err)
			}
			var target mdm.EnrollmentID
			for _, id := range ids {
				if id.Channel.IsUser() {
					if err := ms.UpsertAuthenticate(ctx, id, nil, nil, t0); err != nil {
						t.Fatal(err)
					}
				}
				if err := ms.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0); err != nil {
					t.Fatal(err)
				}
				if _, err := engine.AssignSet(ctx, id, "lab"); err != nil {
					t.Fatal(err)
				}
				if _, err := engine.Manifest(ctx, id); err != nil {
					t.Fatal(err)
				}
				if id.ID == tc.target {
					target = id
				}
			}
			// Constants above select the affected fixture row; this late-table
			// failure follows assignment and snapshot deletions within the savepoint.
			trigger := "CREATE TRIGGER reject_cleanup BEFORE DELETE ON ddm_changes WHEN OLD.enrollment_id = '" + tc.failed + "' BEGIN SELECT RAISE(FAIL, 'cleanup unavailable'); END"
			if _, err := ms.DB().ExecContext(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			cert := old
			if tc.kind == "Authenticate" {
				cert = fresh
			}
			result, err := core.Checkin(ctx, &mdm.Request{Certificate: cert}, lifecycleMessage(t, target, tc.kind))
			if err == nil || !strings.Contains(err.Error(), "cleanup unavailable") || service.CodeOf(err) != service.CodeInternal || result != nil {
				t.Fatalf("cleanup failure reported success: %+v %v", result, err)
			}
			for _, id := range ids {
				stored, err := ms.Get(ctx, id)
				if err != nil || !stored.Enabled || !stored.DisabledAt.IsZero() {
					t.Fatalf("MDM rollback %s: %+v %v", id.ID, stored, err)
				}
				if id == device && stored.CertHash != cms.Fingerprint(old) {
					t.Fatal("new pin survived cleanup failure")
				}
				requireMembership(t, engine, id, true)
				if _, err := ds.Snapshot(ctx, id); err != nil {
					t.Fatalf("snapshot rollback %s: %v", id.ID, err)
				}
			}
			var events int
			if err := ms.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM event_records").Scan(&events); err != nil || events != 0 {
				t.Fatalf("failed lifecycle event committed: %d %v", events, err)
			}
			if _, err := ms.DB().ExecContext(ctx, "DROP TRIGGER reject_cleanup"); err != nil {
				t.Fatal(err)
			}
			if _, err := core.Checkin(ctx, &mdm.Request{Certificate: cert}, lifecycleMessage(t, target, tc.kind)); err != nil {
				t.Fatal(err)
			}
			for _, id := range ids {
				requireMembership(t, engine, id, target.Channel.IsUser() && id != target)
			}
		})
	}
}
