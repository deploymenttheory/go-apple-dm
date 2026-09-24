package recovery

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/audit"
	auditsql "github.com/deploymenttheory/go-apple-dm/server/audit/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// emptySQLite opens an empty temporary SQLite database and registers cleanup.
func emptySQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(
		"sqlite",
		// Several replicas heartbeat to this one file, and Windows runners serialise
		// that far more slowly than the production default allows for.
		sqlite.DSN(
			filepath.Join(t.TempDir(), "state.sqlite"),
			sqlite.Options{BusyTimeout: 60 * time.Second},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// sqlFixture migrates every reference-server schema into the supplied SQL database.
func sqlFixture(t *testing.T, db *sql.DB, d sqlcommon.Dialect) SQL {
	t.Helper()
	sets, err := ServerSchema(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range sets {
		if _, err := sqlcommon.MigrateSet(t.Context(), db, d, set); err != nil {
			t.Fatal(err)
		}
	}
	return SQL{DB: db, Dialect: d, Schema: sets}
}

// TestSQLiteDatabaseRecovery checks SQLite database recovery.
func TestSQLiteDatabaseRecovery(t *testing.T) {
	source := sqlFixture(t, emptySQLite(t), sqlite.Dialect)
	exerciseSQLRecovery(t, source, func() *sql.DB { return emptySQLite(t) })
}

// exerciseSQLRecovery checks SQL recovery preservation, encrypted snapshots, sequence
// monotonicity, delivery state, and rejection of invalid or occupied targets.
func exerciseSQLRecovery(t *testing.T, source SQL, target func() *sql.DB) {
	t.Helper()
	ctx := t.Context()
	keys, err := crypt.NewKeyring(
		ctx,
		crypt.Options{
			Keys:     crypt.Keys{Active: "original-key-id", Strict: true},
			Provider: secrets.Static{"original-key-id": bytes.Repeat([]byte{7}, 32)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	store := sqlcommon.New(source.DB, source.Dialect, sqlcommon.WithKeyring(keys))
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "recovery-device"}
	now := time.Date(2026, 9, 13, 12, 30, 1, 123456000, time.UTC)
	record := storage.EnrollmentExport{
		Enrollment: storage.Enrollment{
			ID:             id,
			Enabled:        true,
			CertHash:       "accepted-certificate",
			CertHashAt:     now,
			EnrolledAt:     now,
			LastSeenAt:     now,
			TokenUpdatedAt: now,
			Device:         storage.DeviceInfo{ProductName: "Mac16,12", OSVersion: "26.6.2"},
			Push: mdm.Push{
				Topic: "original-topic",
				Magic: "original-magic",
				Token: []byte("original-push-token"),
			},
		},
	}
	if err := store.Import(ctx, record); err != nil {
		t.Fatal(err)
	}
	st, err := statestore.Open(ctx, source.DB, source.Dialect, keys)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("original issuer private key and replacement binding")
	if err := st.Update(ctx, []string{"recovery-fixture/issuer"}, func(tx state.Tx) error {
		return tx.Put(ctx, state.Record{Key: "recovery-fixture/issuer", Value: secret})
	}); err != nil {
		t.Fatal(err)
	}
	trail, err := auditsql.Open(ctx, source.DB, source.Dialect, auditsql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var high int64
	for i := 0; i < 4; i++ {
		row, err := trail.Append(
			ctx,
			audit.Record{At: now, Type: "restore-fixture", Enrollment: id},
		)
		if err != nil {
			t.Fatal(err)
		}
		high = row.ID
	}
	if _, err := source.DB.ExecContext(ctx, "DELETE FROM audit_records WHERE id > 1"); err != nil {
		t.Fatal(err)
	}
	events, err := eventstore.Open(ctx, source.DB, source.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Capture(
		ctx,
		eventsink.Record{EventID: "original-event", Type: "admin.action", At: now, ID: id.ID},
		[]string{"audit", "webhook"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Claim(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	stopParticipants := fenceForSnapshot(t, source)
	directory := filepath.Join(t.TempDir(), "database")
	if err := source.Snapshot(ctx, directory); err != nil {
		t.Fatal(err)
	}
	if err := source.ValidateKeys(ctx, directory, keys); err != nil {
		t.Fatal(err)
	}
	wrong, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "original-key-id", Strict: true}, Provider: secrets.Static{"original-key-id": bytes.Repeat([]byte{8}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.ValidateKeys(ctx, directory, wrong); err == nil {
		t.Fatal("accepted wrong database storage key")
	}
	stopParticipants()
	var snapshot databaseSnapshot
	if err := readJSONFile(filepath.Join(directory, "database.json"), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Schemas) != len(source.Schema) {
		t.Fatal("schema omitted")
	}
	for _, file := range snapshot.Tables {
		// #nosec G304 -- The test controls this fixture path within its private workspace.
		raw, err := os.ReadFile(filepath.Join(directory, file.Name+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, secret) {
			t.Fatal("snapshot decrypted a secret column")
		}
	}
	destination := SQL{DB: target(), Dialect: source.Dialect, Schema: source.Schema}
	if err := destination.Restore(ctx, directory); err != nil {
		t.Fatal(err)
	}
	restored := sqlcommon.New(destination.DB, destination.Dialect, sqlcommon.WithKeyring(keys))
	got, err := restored.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.CertHash != record.CertHash || got.Push.Topic != record.Push.Topic ||
		!bytes.Equal(got.Push.Token, record.Push.Token) ||
		!got.TokenUpdatedAt.Equal(now) {
		t.Fatal("identity or tokens changed", got)
	}
	restoredState, err := statestore.Open(ctx, destination.DB, destination.Dialect, keys)
	if err != nil {
		t.Fatal(err)
	}
	value, err := restoredState.Get(ctx, "recovery-fixture/issuer")
	if err != nil || !bytes.Equal(value.Value, secret) {
		t.Fatal("state/key binding changed", err)
	}
	restoredAudit, err := auditsql.Open(
		ctx,
		destination.DB,
		destination.Dialect,
		auditsql.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	next, err := restoredAudit.Append(ctx, audit.Record{At: now, Type: "after-restore"})
	if err != nil || next.ID <= high {
		t.Fatal("retained audit cursor was reused", next.ID, high, err)
	}
	restoredEvents, err := eventstore.Open(ctx, destination.DB, destination.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	status, err := restoredEvents.Status(ctx)
	if err != nil || status.Records != 1 || status.Pending != 2 {
		t.Fatal("event delivery state lost", status, err)
	}
	if err := destination.Restore(ctx, directory); !errors.Is(err, ErrOccupied) {
		t.Fatal("overwrote recovered deployment", err)
	}
	// A checkpoint can authenticate correctly yet contain inconsistent cursor
	// metadata. Refuse it before committing rows or allowing cursor reuse.
	for i := range snapshot.Sequences {
		if snapshot.Sequences[i].Table == "audit_records" {
			snapshot.Sequences[i].Value = 0
		}
	}
	corrupt, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "database.json"), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := SQL{DB: target(), Dialect: source.Dialect, Schema: source.Schema}
	if err := invalid.Restore(ctx, directory); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted regressed cursor", err)
	}
	var rows int
	if err := invalid.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_records").Scan(&rows); err != nil || rows != 0 {
		t.Fatal("rows escaped failed cursor validation", rows, err)
	}
}

// fenceForSnapshot starts and drains two maintenance participants, rejects late registration, and
// returns their cleanup function.
func fenceForSnapshot(t *testing.T, s SQL) func() {
	t.Helper()
	control, err := maintenance.Open(t.Context(), s.DB, s.Dialect, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	participants := []*maintenance.Participant{}
	done := make(chan error, 2)
	started := make(chan struct{}, 2)
	for _, name := range []string{"mdm-replica", "ddm-replica"} {
		p, err := control.Register(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		participants = append(participants, p)
		go func() {
			done <- p.Run(ctx, func(ctx context.Context) error { started <- struct{}{}; <-ctx.Done(); return ctx.Err() })
		}()
	}
	<-started
	<-started
	ticket := rand.Text()
	if err := control.Request(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	deadline, stop := context.WithTimeout(t.Context(), 60*time.Second)
	defer stop()
	if err := control.WaitDrained(deadline, ticket); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Register(t.Context(), "late-replica"); !errors.Is(err, maintenance.ErrFenced) {
		t.Fatal(err)
	}
	return func() {
		cancel()
		for _, p := range participants {
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := p.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestSnapshotRejectsUnregisteredAndIncompleteSchema checks that snapshot rejects unregistered and
// incomplete schema.
func TestSnapshotRejectsUnregisteredAndIncompleteSchema(t *testing.T) {
	s := sqlFixture(t, emptySQLite(t), sqlite.Dialect)
	if _, err := s.DB.ExecContext(
		t.Context(),
		"CREATE TABLE unregistered (private_value TEXT)",
	); err != nil {
		t.Fatal(err)
	}
	if err := s.Snapshot(
		t.Context(),
		filepath.Join(t.TempDir(), "unknown"),
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(t.Context(), "DROP TABLE unregistered"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(t.Context(), "DROP TABLE event_deliveries"); err != nil {
		t.Fatal(err)
	}
	if err := s.Snapshot(
		t.Context(),
		filepath.Join(t.TempDir(), "missing"),
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatal(err)
	}
}

// TestDatabaseRejectsCorruptMetadataAndRollsBackRows checks that database rejects corrupt metadata
// and rolls back rows.
func TestDatabaseRejectsCorruptMetadataAndRollsBackRows(t *testing.T) {
	s := sqlFixture(t, emptySQLite(t), sqlite.Dialect)
	dir := filepath.Join(t.TempDir(), "database")
	if err := s.Snapshot(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	raw, _ := os.ReadFile(filepath.Join(dir, "database.json"))
	for _, name := range []string{"schema", "backend", "table", "columns", "sequence", "rows"} {
		t.Run(name, func(t *testing.T) {
			var info databaseSnapshot
			if err := json.Unmarshal(raw, &info); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "schema":
				info.Schemas[0].Signature = "changed"
			case "backend":
				info.Backend = "different"
			case "table":
				info.Tables[0].Name = "unregistered"
			case "columns":
				info.Tables[0].Columns[0] = "different"
			case "sequence":
				info.Sequences[0].Value = -1
			case "rows":
				info.Tables[0].Rows++
			}
			modified, _ := json.Marshal(info)
			if err := os.WriteFile(
				filepath.Join(dir, "database.json"),
				modified,
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			destination := SQL{DB: emptySQLite(t), Dialect: s.Dialect, Schema: s.Schema}
			if err := destination.Restore(t.Context(), dir); err == nil {
				t.Fatal("corrupted database accepted")
			}
		})
	}
}
