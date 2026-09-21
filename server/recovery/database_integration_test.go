//go:build integration

package recovery

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// TestPostgresDatabaseRecovery checks PostgreSQL database and public deployment recovery.
func TestPostgresDatabaseRecovery(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	control := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = control.Close() })
	newDSN := func() string {
		name := fmt.Sprintf("recovery_%d", time.Now().UnixNano())
		if _, err := control.ExecContext(t.Context(), "CREATE SCHEMA "+name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = control.ExecContext(t.Context(), "DROP SCHEMA "+name+" CASCADE") })
		// ConnString retains the input text; mutating RuntimeParams does not
		// serialize it. Include search_path in the DSN actually opened below.
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			q.Set("search_path", name)
			u.RawQuery = q.Encode()
			return u.String()
		}
		return dsn + " search_path=" + name
	}
	newDB := func() *sql.DB {
		s, err := OpenDatabase(t.Context(), "postgres", newDSN())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.DB.Close() })
		return s.DB
	}
	source := sqlFixture(t, newDB(), postgres.Dialect)
	exerciseSQLRecovery(t, source, newDB)
	exercisePublicDeploymentRecovery(t, "postgres", newDSN)
}

// TEST_MYSQL_DSN names the disposable test database, as in the existing storage
// suites. Packages using it must run serially. MySQL's test account cannot create
// another database, so restore runs after removing the checkpointed fixture's
// tables from that same disposable database.
func TestMySQLDatabaseRecovery(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	normalized, err := mysql.NormalizeDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clear := func() *sql.DB {
		names, err := tableNames(t.Context(), db, "mysql")
		if err != nil {
			t.Fatal(err)
		}
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func(cleanup func() error) { _ = cleanup() }(conn.Close)
		if _, err := conn.ExecContext(t.Context(), "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := conn.ExecContext(t.Context(), "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
				t.Error(err)
			}
		}()
		for _, name := range names {
			if strings.Contains(name, "`") {
				t.Fatal("invalid table")
			}
			if _, err := conn.ExecContext(t.Context(), "DROP TABLE `"+name+"`"); err != nil {
				t.Fatal(err)
			}
		}
		return db
	}
	source := sqlFixture(t, clear(), mysql.Dialect)
	// Other integration packages share this disposable database. Remove the
	// restored fence and fixture tables when this contract finishes.
	defer clear()
	exerciseSQLRecovery(t, source, clear)
	exercisePublicDeploymentRecovery(t, "mysql", func() string { clear(); return normalized })
}

// exercisePublicDeploymentRecovery checks public backup and restore behavior, preserving
// enrollment identity and storage-key bindings while restoring paused.
func exercisePublicDeploymentRecovery(t *testing.T, backend string, emptyDSN func() string) {
	t.Helper()
	setup, b := bootstrapFixture(t)
	b.Environment["DM_STORAGE"], b.Environment["DM_DSN"] = backend, emptyDSN()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(setup, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenDatabase(t.Context(), backend, b.Environment["DM_DSN"])
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(s.DB.Close)
	s = sqlFixture(t, s.DB, s.Dialect)
	material, err := readPrivate(filepath.Join(b.Environment["DM_SECRETS_DIR"], "original-key.v1"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "original-key.v1", Strict: true}, Provider: secrets.Static{"original-key.v1": material}})
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "managed-before-recovery"}
	at := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	record := storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, CertHash: "original-pin", EnrolledAt: at, LastSeenAt: at, TokenUpdatedAt: at, Push: mdm.Push{Topic: "original-topic", Token: []byte("original-token"), Magic: "original-magic"}}, BootstrapToken: []byte("original-bootstrap-token")}
	store := sqlcommon.New(s.DB, s.Dialect, sqlcommon.WithKeyring(keys))
	if err := store.Import(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	stop := fenceForSnapshot(t, s)
	control, err := maintenance.Open(t.Context(), s.DB, s.Dialect, false)
	if err != nil {
		t.Fatal(err)
	}
	status, err := control.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	archive := filepath.Join(dir, "checkpoint.age")
	_, err = s.Backup(t.Context(), BackupOptions{SetupFile: setup, Destination: archive, StagingParent: dir, Ticket: status.Token, Revision: "native-backend-contract", Recipients: []age.Recipient{key.Recipient()}})
	if err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	p, err := Prepare(t.Context(), archive, dir, []age.Identity{key}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(p.Close)
	if err := p.CheckDatabase(t.Context(), emptyDSN()); err != nil {
		t.Fatal(err)
	}
	targetDSN := emptyDSN()
	result, err := p.Restore(t.Context(), filepath.Join(dir, "restored"), targetDSN, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "paused" || result.PublicURL != b.Environment["DM_PUBLIC_URL"] {
		t.Fatal("restore changed public identity", result)
	}
	target, err := OpenDatabase(t.Context(), backend, targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(target.DB.Close)
	restored := sqlcommon.New(target.DB, target.Dialect, sqlcommon.WithKeyring(keys))
	got, err := restored.Get(t.Context(), id)
	if err != nil || got.CertHash != record.CertHash || got.Push.Topic != record.Push.Topic || string(got.Push.Token) != string(record.Push.Token) {
		t.Fatal("public recovery changed enrollment", err)
	}
	bootstrap, err := restored.BootstrapToken(t.Context(), id)
	if err != nil || string(bootstrap) != string(record.BootstrapToken) {
		t.Fatal("public recovery lost original storage key binding", err)
	}
	restoredControl, err := maintenance.Open(t.Context(), target.DB, target.Dialect, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := restoredControl.Status(t.Context())
	if err != nil || !state.Ready() || len(state.Members) != 0 {
		t.Fatal("restored source participants or opened fence", state, err)
	}
	ticket, err := readPrivate(result.TicketFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoredControl.Resume(t.Context(), string(ticket)); err != nil {
		t.Fatal(err)
	}
}
