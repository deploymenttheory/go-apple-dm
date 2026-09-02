package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
	"github.com/deploymenttheory/go-apple-mdm/storage"
	"github.com/deploymenttheory/go-apple-mdm/storage/sqlcommon"
	"github.com/deploymenttheory/go-apple-mdm/storage/sqlcommon/sqltest"
	"github.com/deploymenttheory/go-apple-mdm/storage/sqlite"
	"github.com/deploymenttheory/go-apple-mdm/storage/storagetest"
)

func open(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "mdm.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestContract(t *testing.T) {
	t.Parallel()
	storagetest.RunAll(t, func(t *testing.T) storage.Store { return open(t) })
}

func TestOpenAndMigrations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := sqlite.Open(ctx, "", sqlite.Options{}); !errors.Is(err, sqlite.ErrPathRequired) {
		t.Fatalf("empty path: %v", err)
	}
	if _, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "missing", "dir", "x.db"), sqlite.Options{}); err == nil {
		t.Fatal("unwritable path")
	}
	dsn := sqlite.DSN("x.db", sqlite.Options{BusyTimeout: time.Second})
	if !strings.Contains(dsn, "busy_timeout%281000%29") || !strings.Contains(dsn, "_txlock=immediate") {
		t.Fatalf("dsn %s", dsn)
	}
	path := filepath.Join(t.TempDir(), "m.db")
	s, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := sqlcommon.Version(ctx, s.DB(), sqlite.Dialect)
	if err != nil || v != 1 {
		t.Fatalf("version %d %v", v, err)
	}
	// Idempotent.
	if applied, err := sqlcommon.Migrate(ctx, s.DB(), sqlite.Dialect); err != nil || len(applied) != 0 {
		t.Fatalf("second migrate: %v %v", applied, err)
	}
	// Down and back up.
	if reverted, err := sqlcommon.Rollback(ctx, s.DB(), sqlite.Dialect, 0); err != nil || len(reverted) != 1 {
		t.Fatalf("rollback: %v %v", reverted, err)
	}
	if v, _ := sqlcommon.Version(ctx, s.DB(), sqlite.Dialect); v != 0 {
		t.Fatalf("version after rollback %d", v)
	}
	if _, err := s.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "x"}); err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("query without schema must be a real error: %v", err)
	}
	if applied, err := sqlcommon.Migrate(ctx, s.DB(), sqlite.Dialect); err != nil || len(applied) != 1 {
		t.Fatalf("re-migrate: %v %v", applied, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(ctx); err == nil {
		t.Fatal("ping after close")
	}
	// Reopen with SkipMigrate keeps the schema.
	s2, err := sqlite.Open(ctx, path, sqlite.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.UpsertAuthenticate(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "d"}, &checkin.Authenticate{Topic: "t"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestClearBatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "big"}
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0); err != nil {
		t.Fatal(err)
	}
	n := sqlcommon.ClearBatchSize*2 + 7
	for i := range n {
		cmd := &mdm.Command{UUID: fmt.Sprintf("U%05d", i), RequestType: "ProfileList", Raw: []byte("<plist/>")}
		if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{Now: t0.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	// Only the first half by time.
	half := t0.Add(time.Duration(n/2) * time.Second)
	cleared, err := s.Clear(ctx, id, storage.ClearFilter{Before: half})
	if err != nil || cleared != int64(n/2) {
		t.Fatalf("cleared %d %v, want %d", cleared, err, n/2)
	}
	cleared, err = s.Clear(ctx, id, storage.ClearFilter{RequestType: "DeviceLock"})
	if err != nil || cleared != 0 {
		t.Fatalf("wrong type cleared %d %v", cleared, err)
	}
	cleared, err = s.Clear(ctx, id, storage.ClearFilter{States: []storage.State{storage.StateAcknowledged}})
	if err != nil || cleared != 0 {
		t.Fatalf("terminal filter cleared %d %v", cleared, err)
	}
	cleared, err = s.Clear(ctx, id, storage.ClearFilter{})
	if err != nil || cleared != int64(n-n/2) {
		t.Fatalf("rest cleared %d %v", cleared, err)
	}
	if next, err := s.Next(ctx, id, false, t0); err != nil || next != nil {
		t.Fatalf("queue not empty: %v %v", next, err)
	}
	// Newest-first paging over the cleared commands with the keyset cursor.
	seen := 0
	cursor := ""
	for {
		res, err := s.Commands(ctx, id, storage.CommandQuery{States: []storage.State{storage.StateCleared}}, storage.Page{Cursor: cursor, Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		seen += len(res.Items)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if seen != n {
		t.Fatalf("paged %d of %d", seen, n)
	}
	if _, err := s.Commands(ctx, id, storage.CommandQuery{}, storage.Page{Cursor: "x"}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatal("bad cursor")
	}
}

func TestResultRoundTripAndInvalidIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "r"}
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0); err != nil {
		t.Fatal(err)
	}
	cmd := &mdm.Command{UUID: "E1", RequestType: "DeviceLock", Raw: []byte("<plist/>")}
	if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{Now: t0}); err != nil {
		t.Fatal(err)
	}
	// Same UUID twice is a per-enrollment conflict, not an error.
	res, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{})
	if err != nil || len(res.Queued) != 0 || !errors.Is(res.Skipped[id], storage.ErrConflict) {
		t.Fatalf("duplicate: %+v %v", res, err)
	}
	if _, err := s.Next(ctx, id, false, t0); err != nil {
		t.Fatal(err)
	}
	resp := &mdm.Response{CommandUUID: "E1", Status: mdm.StatusError, Raw: []byte("<r/>"),
		ErrorChain: []mdm.ErrorChainItem{{ErrorCode: 12021, ErrorDomain: "MCMDMErrorDomain", LocalizedDescription: "nope"}}}
	if err := s.StoreResult(ctx, id, resp, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Commands(ctx, id, storage.CommandQuery{RequestType: "DeviceLock"}, storage.Page{})
	if err != nil || len(got.Items) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	c := got.Items[0]
	if c.State != storage.StateError || c.Result == nil || c.Result.Status != mdm.StatusError || string(c.Result.Raw) != "<r/>" ||
		len(c.Result.ErrorChain) != 1 || c.Result.ErrorChain[0].ErrorCode != 12021 || c.Result.ID != id || c.Attempts != 1 ||
		!c.CompletedAt.Equal(t0.Add(time.Minute)) || !c.LastSentAt.Equal(t0) {
		t.Fatalf("round trip: %+v result=%+v", c, c.Result)
	}
	bad := mdm.EnrollmentID{}
	if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{bad}, cmd, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"Next":           func() error { _, err := s.Next(ctx, bad, false, t0); return err },
		"StoreResult":    func() error { return s.StoreResult(ctx, bad, resp, t0) },
		"Commands":       func() error { _, err := s.Commands(ctx, bad, storage.CommandQuery{}, storage.Page{}); return err },
		"Clear":          func() error { _, err := s.Clear(ctx, bad, storage.ClearFilter{}); return err },
		"AssociateCert":  func() error { return s.AssociateCert(ctx, bad, "h", t0) },
		"CertHash":       func() error { _, err := s.CertHash(ctx, bad); return err },
		"StoreBootstrap": func() error { return s.StoreBootstrapToken(ctx, bad, []byte{1}, t0) },
		"BootstrapToken": func() error { _, err := s.BootstrapToken(ctx, bad); return err },
		"StoreTokenUpdate": func() error {
			return s.StoreTokenUpdate(ctx, bad, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0)
		},
		"Disable":            func() error { return s.Disable(ctx, bad, t0) },
		"TouchLastSeen":      func() error { return s.TouchLastSeen(ctx, bad, t0) },
		"UpsertAuthenticate": func() error { return s.UpsertAuthenticate(ctx, bad, nil, nil, t0) },
	} {
		if err := call(); !errors.Is(err, storage.ErrInvalid) {
			t.Errorf("%s with invalid id: %v", name, err)
		}
	}
	if info, err := s.PushInfo(ctx, nil); err != nil || len(info) != 0 {
		t.Fatal("empty push info")
	}
	if _, err := s.Clear(ctx, id, storage.ClearFilter{States: []storage.State{storage.StateCleared}}); err != nil {
		t.Fatal(err)
	}
}

// TestQueriesFailWithoutSchema drives every method's error path through a
// database whose tables were dropped.
func TestQueriesFailWithoutSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "d"}
	t0 := time.Now()
	if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE commands"); err != nil {
		t.Fatal(err)
	}
	cmd := &mdm.Command{UUID: "X", RequestType: "ProfileList"}
	resp := &mdm.Response{CommandUUID: "X", Status: mdm.StatusAcknowledged}
	calls := map[string]func() error{
		"Enqueue": func() error {
			_, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{DedupeKey: "k"})
			return err
		},
		"Next":        func() error { _, err := s.Next(ctx, id, false, t0); return err },
		"StoreResult": func() error { return s.StoreResult(ctx, id, resp, t0) },
		"Commands":    func() error { _, err := s.Commands(ctx, id, storage.CommandQuery{}, storage.Page{}); return err },
		"Clear":       func() error { _, err := s.Clear(ctx, id, storage.ClearFilter{}); return err },
		"Upsert":      func() error { return s.UpsertAuthenticate(ctx, id, nil, nil, t0) },
	}
	for name, call := range calls {
		if err := call(); err == nil || errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	uid := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "d:u", ParentID: "d"}
	pushCertPEM, pushKeyPEM := pushPair(t, t0)
	// user_auth carries a foreign key to enrollments, so it goes first.
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE user_auth"); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"UserAuthChallenge": func() error { return s.StoreUserAuthChallenge(ctx, uid, "c", nil, t0) },
		"UserAuth":          func() error { _, err := s.UserAuth(ctx, uid); return err },
		"ClearUserAuth":     func() error { return s.ClearUserAuth(ctx, uid) },
	} {
		if err := call(); err == nil || errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE cert_associations"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE enrollments"); err != nil {
		t.Fatal(err)
	}
	calls = map[string]func() error{
		"Get":  func() error { _, err := s.Get(ctx, id); return err },
		"List": func() error { _, err := s.List(ctx, storage.EnrollmentQuery{}, storage.Page{}); return err },
		"StoreTokenUpdate": func() error {
			return s.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0)
		},
		"Disable":         func() error { return s.Disable(ctx, id, t0) },
		"TouchLastSeen":   func() error { return s.TouchLastSeen(ctx, id, t0) },
		"PushInfo":        func() error { _, err := s.PushInfo(ctx, []mdm.EnrollmentID{id}); return err },
		"AssociateCert":   func() error { return s.AssociateCert(ctx, id, "h", t0) },
		"CertHash":        func() error { _, err := s.CertHash(ctx, id); return err },
		"ByCertHash":      func() error { _, err := s.EnrollmentByCertHash(ctx, "h"); return err },
		"CertHistory":     func() error { _, err := s.CertHistory(ctx, id); return err },
		"CertHashHistory": func() error { _, err := s.CertHashHistory(ctx, "h"); return err },
		"StoreBootstrap":  func() error { return s.StoreBootstrapToken(ctx, id, []byte{1}, t0) },
		"BootstrapToken":  func() error { _, err := s.BootstrapToken(ctx, id); return err },
		"Export":          func() error { _, err := s.Export(ctx, storage.Page{}); return err },
		"Import": func() error {
			return s.Import(ctx, storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id}})
		},
		"UserAuthChallenge": func() error { return s.StoreUserAuthChallenge(ctx, uid, "c", nil, t0) },
		"UserAuthToken":     func() error { return s.StoreUserAuthToken(ctx, uid, "t", nil, t0) },
		"UserAuth":          func() error { _, err := s.UserAuth(ctx, uid); return err },
		"ClearUserAuth":     func() error { return s.ClearUserAuth(ctx, uid) },
	}
	for name, call := range calls {
		if err := call(); err == nil || errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE push_certs"); err != nil {
		t.Fatal(err)
	}
	calls = map[string]func() error{
		"StorePushCert":   func() error { _, err := s.StorePushCert(ctx, "", pushCertPEM, pushKeyPEM, t0); return err },
		"PushCert":        func() error { _, err := s.PushCert(ctx, "com.apple.mgmt.x"); return err },
		"PushCerts":       func() error { _, err := s.PushCerts(ctx); return err },
		"PushCertVersion": func() error { _, err := s.PushCertVersion(ctx, "com.apple.mgmt.x"); return err },
	}
	for name, call := range calls {
		if err := call(); err == nil || errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE schema_migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, "CREATE TABLE schema_migrations (version TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES ('abc')"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlcommon.Version(ctx, s.DB(), sqlite.Dialect); err == nil {
		t.Fatal("unscannable version")
	}
}

// TestWriteFailuresSurface uses RAISE triggers so every UPDATE and INSERT
// that follows a successful existence check fails, proving the error is
// returned rather than swallowed.
func TestWriteFailuresSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "w"}
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0); err != nil {
		t.Fatal(err)
	}
	cmd := &mdm.Command{UUID: "W1", RequestType: "ProfileList", Raw: []byte("<plist/>")}
	if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{Now: t0}); err != nil {
		t.Fatal(err)
	}
	// A history insert that fails must roll the pin back too.
	if _, err := s.DB().ExecContext(ctx, "CREATE TRIGGER no_insert_cert_associations BEFORE INSERT ON cert_associations BEGIN SELECT RAISE(FAIL, 'history disabled'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.AssociateCert(ctx, id, "h-new", t0); err == nil || !strings.Contains(err.Error(), "history disabled") {
		t.Fatalf("AssociateCert with history disabled: %v", err)
	}
	if h, _ := s.CertHash(ctx, id); h != "" {
		t.Fatalf("pin written despite the failed history insert: %q", h)
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TRIGGER no_insert_cert_associations"); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"CREATE TRIGGER no_update_enrollments BEFORE UPDATE ON enrollments BEGIN SELECT RAISE(FAIL, 'writes disabled'); END",
		"CREATE TRIGGER no_update_commands BEFORE UPDATE ON commands BEGIN SELECT RAISE(FAIL, 'writes disabled'); END",
		"CREATE TRIGGER no_insert_commands BEFORE INSERT ON commands BEGIN SELECT RAISE(FAIL, 'writes disabled'); END",
	} {
		if _, err := s.DB().ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	uid := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "w:u", ParentID: "w"}
	if err := s.StoreUserAuthChallenge(ctx, uid, "c", nil, t0); err != nil {
		t.Fatal(err)
	}
	pushCertPEM, pushKeyPEM := pushPair(t, t0)
	if _, err := s.StorePushCert(ctx, "", pushCertPEM, pushKeyPEM, t0); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"CREATE TRIGGER no_update_user_auth BEFORE UPDATE ON user_auth BEGIN SELECT RAISE(FAIL, 'writes disabled'); END",
		"CREATE TRIGGER no_insert_user_auth BEFORE INSERT ON user_auth BEGIN SELECT RAISE(FAIL, 'writes disabled'); END",
		"CREATE TRIGGER no_delete_user_auth BEFORE DELETE ON user_auth BEGIN SELECT RAISE(FAIL, 'writes disabled'); END",
		"CREATE TRIGGER no_update_push_certs BEFORE UPDATE ON push_certs BEGIN SELECT RAISE(FAIL, 'writes disabled'); END",
		"CREATE TRIGGER no_insert_push_certs BEFORE INSERT ON push_certs BEGIN SELECT RAISE(FAIL, 'writes disabled'); END",
	} {
		if _, err := s.DB().ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	otherCert, otherKey := pushPair(t, t0)
	resp := &mdm.Response{CommandUUID: "W1", Status: mdm.StatusAcknowledged}
	calls := map[string]func() error{
		"UserAuthChallenge": func() error { return s.StoreUserAuthChallenge(ctx, uid, "c2", nil, t0) },
		"UserAuthToken":     func() error { return s.StoreUserAuthToken(ctx, uid, "t", nil, t0) },
		"ClearUserAuth":     func() error { return s.ClearUserAuth(ctx, uid) },
		"StorePushCertNew":  func() error { _, err := s.StorePushCert(ctx, "", otherCert, otherKey, t0); return err },
		"StorePushCertRenew": func() error {
			_, err := s.StorePushCert(ctx, "", pushCertPEM, pushKeyPEM, t0)
			return err
		},
		"Import": func() error {
			return s.Import(ctx, storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, EnrolledAt: t0, LastSeenAt: t0}})
		},
		"StoreTokenUpdate": func() error {
			return s.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0)
		},
		"Disable":       func() error { return s.Disable(ctx, id, t0) },
		"TouchLastSeen": func() error { return s.TouchLastSeen(ctx, id, t0.Add(time.Hour)) },
		"Enqueue": func() error {
			_, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, &mdm.Command{UUID: "W2", RequestType: "ProfileList"}, storage.EnqueueOptions{})
			return err
		},
		"Next":           func() error { _, err := s.Next(ctx, id, false, t0); return err },
		"StoreResult":    func() error { return s.StoreResult(ctx, id, resp, t0) },
		"Clear":          func() error { _, err := s.Clear(ctx, id, storage.ClearFilter{}); return err },
		"AssociateCert":  func() error { return s.AssociateCert(ctx, id, "h", t0) },
		"StoreBootstrap": func() error { return s.StoreBootstrapToken(ctx, id, []byte{1}, t0) },
		"Upsert":         func() error { return s.UpsertAuthenticate(ctx, id, nil, nil, t0) },
	}
	for name, call := range calls {
		err := call()
		if err == nil || errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrInvalid) || !strings.Contains(err.Error(), "writes disabled") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestIsUniqueViolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "u"}
	if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err := s.DB().ExecContext(ctx, "INSERT INTO enrollments (id, channel, enrolled_at, last_seen_at) VALUES ('u', 1, '2026-01-01', '2026-01-01')")
	if !sqlite.IsUniqueViolation(err) {
		t.Fatalf("duplicate primary key not detected: %v", err)
	}
	_, err = s.DB().ExecContext(ctx, "INSERT INTO enrollments (id, channel, cert_hash, enrolled_at, last_seen_at) VALUES ('v', 1, 'h', '2026-01-01', '2026-01-01')")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB().ExecContext(ctx, "UPDATE enrollments SET cert_hash = 'h' WHERE id = 'u'")
	if !sqlite.IsUniqueViolation(err) {
		t.Fatalf("duplicate unique index not detected: %v", err)
	}
	for name, err := range map[string]error{"nil": nil, "plain": errors.New("x"), "no rows": sql.ErrNoRows} {
		if sqlite.IsUniqueViolation(err) {
			t.Errorf("%s reported as unique violation", name)
		}
	}
	_, err = s.DB().ExecContext(ctx, "INSERT INTO nope VALUES (1)")
	if sqlite.IsUniqueViolation(err) {
		t.Fatal("missing table reported as unique violation")
	}
}

// BenchmarkClear100k measures Clear over a queue of 100,000 pending
// commands (plan phase 4 exit criterion; the gate is enforced on
// PostgreSQL in the integration tests).
func BenchmarkClear100k(b *testing.B) {
	ctx := context.Background()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "bench"}
	for range b.N {
		b.StopTimer()
		s, err := sqlite.Open(ctx, filepath.Join(b.TempDir(), "bench.db"), sqlite.Options{})
		if err != nil {
			b.Fatal(err)
		}
		if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, time.Now()); err != nil {
			b.Fatal(err)
		}
		sqltest.Seed(ctx, b, s.DB(), sqlite.Dialect, id.ID, 100_000)
		b.StartTimer()
		n, err := s.Clear(ctx, id, storage.ClearFilter{})
		if err != nil || n != 100_000 {
			b.Fatalf("cleared %d: %v", n, err)
		}
		b.StopTimer()
		_ = s.Close()
	}
}

func TestOpenMigrationFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conflict.db")
	raw, err := sqlite.Open(ctx, path, sqlite.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.DB().ExecContext(ctx, "CREATE TABLE enrollments (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, err := sqlite.Open(ctx, path, sqlite.Options{}); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("conflicting schema: %v", err)
	}
	garbage := filepath.Join(t.TempDir(), "garbage.db")
	if err := os.WriteFile(garbage, []byte("this is not a database file at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Open(ctx, garbage, sqlite.Options{}); err == nil {
		t.Fatal("garbage file opened")
	}
}
