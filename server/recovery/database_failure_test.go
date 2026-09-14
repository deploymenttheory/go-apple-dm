package recovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func TestTypedRowsRoundTripAndRejectCorruption(t *testing.T) {
	for _, value := range []any{nil, true, int64(-12), 3.25, "text", []byte{0, 255, 0}, time.Date(2026, 9, 13, 1, 2, 3, 456000, time.UTC)} {
		encoded, err := encodeCell(value, "BLOB")
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeCell(encoded)
		if err != nil || !reflect.DeepEqual(got, value) {
			t.Fatal("SQL type changed", got, value, err)
		}
	}
	if got, err := encodeCell([]byte("text"), "VARCHAR"); err != nil || got.Kind != "text" {
		t.Fatal(got, err)
	}
	if _, err := encodeCell(make(chan int), "TEXT"); err == nil {
		t.Fatal("accepted unsupported SQL driver value")
	}
	for _, c := range []cell{{"null", "value"}, {"unknown", ""}, {"bytes", "%%%"}, {"int", "overflow9223372036854775808"}, {"bool", "maybe"}, {"float", "broken"}, {"time", "yesterday"}} {
		if _, err := decodeCell(c); err == nil {
			t.Fatal("accepted corrupt typed value", c.Kind)
		}
	}
}

func TestRecoveryRequiresCompiledValidSchema(t *testing.T) {
	for _, set := range []sqlcommon.MigrationSet{
		{Table: "schema"},
		{Table: "unsafe-table", FS: fstest.MapFS{}},
		{Table: "schema", FS: fstest.MapFS{"bad.sql": &fstest.MapFile{Data: []byte("invalid")}}},
		{Table: "schema", FS: fstest.MapFS{"0001_init.sql": &fstest.MapFile{Data: []byte("-- +up\nCREATE TABLE bad-name (value TEXT);\n")}}},
	} {
		if _, err := describe(set); err == nil {
			t.Fatal("accepted invalid compiled schema")
		}
	}
	if _, err := ServerSchema(sqlcommon.Dialect{Name: "unsupported"}); err == nil {
		t.Fatal("accepted unsupported backend")
	}
	for _, s := range []SQL{{}, {DB: emptySQLite(t)}, {DB: emptySQLite(t), Dialect: sqlcommon.Dialect{Name: "bad"}, Schema: []sqlcommon.MigrationSet{{}}}} {
		if err := s.Snapshot(t.Context(), filepath.Join(t.TempDir(), "snapshot")); err == nil {
			t.Fatal("accepted invalid snapshot connection")
		}
		if err := s.Restore(t.Context(), t.TempDir()); err == nil {
			t.Fatal("accepted invalid restore connection")
		}
	}
	db := emptySQLite(t)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE "unsafe-name" (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tableNames(t.Context(), db, "sqlite"); err == nil {
		t.Fatal("accepted unsafe SQL identifier")
	}
	if _, err := quoted(sqlite.Dialect, "table; DROP TABLE other"); err == nil {
		t.Fatal("accepted identifier injection")
	}
}

func TestSnapshotFilesystemAndDatabaseFailures(t *testing.T) {
	s := sqlFixture(t, emptySQLite(t), sqlite.Dialect)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Snapshot(ctx, filepath.Join(t.TempDir(), "cancelled")); err == nil {
		t.Fatal("ignored cancellation")
	}
	if err := s.Snapshot(t.Context(), t.TempDir()); err == nil {
		t.Fatal("overwrote existing snapshot")
	}
	for _, table := range []string{"bad-name", "missing"} {
		if _, err := s.snapshotTable(t.Context(), s.DB, t.TempDir(), table); err == nil {
			t.Fatal("accepted invalid table")
		}
	}
	dir := t.TempDir()
	if err := writePrivate(filepath.Join(dir, "enrollments.jsonl"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.snapshotTable(t.Context(), s.DB, dir, "enrollments"); err == nil {
		t.Fatal("overwrote table data")
	}
	if err := s.checkVersions(t.Context(), s.DB, schemaDescription{Name: "bad-name"}); err == nil {
		t.Fatal("accepted unsafe migrations table")
	}
	if err := s.checkVersions(t.Context(), s.DB, schemaDescription{Name: "missing"}); err == nil {
		t.Fatal("accepted missing migrations table")
	}
	if err := s.checkVersions(t.Context(), s.DB, schemaDescription{Name: "schema_migrations", Versions: []int{99}}); err == nil {
		t.Fatal("accepted wrong version")
	}
	if _, err := s.DB.ExecContext(t.Context(), "CREATE TABLE malformed_versions (version TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(t.Context(), "INSERT INTO malformed_versions VALUES ('broken')"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkVersions(t.Context(), s.DB, schemaDescription{Name: "malformed_versions"}); err == nil {
		t.Fatal("accepted malformed migration version")
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Snapshot(t.Context(), filepath.Join(t.TempDir(), "closed")); err == nil {
		t.Fatal("accepted lost source database")
	}
}

func TestRestoreRejectsCorruptRowsBeforeCommit(t *testing.T) {
	for _, corrupt := range []string{"missing-file", "malformed-json", "wrong-count", "wrong-type", "constraint", "trailing"} {
		t.Run(corrupt, func(t *testing.T) {
			source := sqlFixture(t, emptySQLite(t), sqlite.Dialect)
			dir := filepath.Join(t.TempDir(), "snapshot")
			if err := source.Snapshot(t.Context(), dir); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "maintenance_state.jsonl")
			switch corrupt {
			case "missing-file":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			default:
				data := map[string]string{
					"malformed-json": "{", "wrong-count": `[]`, "wrong-type": `[{"kind":"unknown"},{"kind":"text"}]`,
					"constraint": `[{"kind":"text","value":"not an integer"},{"kind":"text"}]`,
					"trailing":   `[{"kind":"int","value":"1"},{"kind":"text"}] []`,
				}[corrupt]
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			target := SQL{DB: emptySQLite(t), Dialect: source.Dialect, Schema: source.Schema}
			if err := target.Restore(t.Context(), dir); err == nil {
				t.Fatal("accepted corrupt rows")
			}
			var n int
			if err := target.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM maintenance_state WHERE token <> ''").Scan(&n); err != nil || n != 0 {
				t.Fatal("partial rows committed", n, err)
			}
		})
	}
}

type failingQueryer struct {
	db           *sql.DB
	rows         [][]any
	exec, failAt int
}

func (f *failingQueryer) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("query failed")
}
func (f *failingQueryer) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	f.exec++
	if f.exec == f.failAt {
		return nil, errors.New("write failed")
	}
	return nil, nil
}
func (f *failingQueryer) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	if len(f.rows) == 0 {
		return f.db.QueryRowContext(ctx, "SELECT missing FROM missing_table")
	}
	row := f.rows[0]
	f.rows = f.rows[1:]
	query := "SELECT " + strings.TrimSuffix(strings.Repeat("?,", len(row)), ",")
	return f.db.QueryRowContext(ctx, query, row...)
}

func TestCatalogAndSequenceFailuresRefuseRecovery(t *testing.T) {
	db := emptySQLite(t)
	for _, d := range []sqlcommon.Dialect{sqlite.Dialect, postgres.Dialect, mysql.Dialect} {
		s := SQL{DB: db, Dialect: d}
		q := &failingQueryer{db: db, failAt: 1}
		if _, err := tableNames(t.Context(), q, d.Name); err == nil {
			t.Fatal("ignored catalog failure")
		}
		if _, err := s.snapshotSequences(t.Context(), q, []tableSnapshot{{Name: "commands"}}); err == nil {
			t.Fatal("ignored source sequence read failure")
		}
		if err := s.restoreSequences(t.Context(), q, []sequenceSnapshot{{Table: "commands", Column: "seq", Value: 1}}); err == nil {
			t.Fatal("ignored target sequence write failure")
		}
	}
	if err := (SQL{Dialect: sqlite.Dialect}).restoreSequences(t.Context(), &failingQueryer{db: db, failAt: 2}, []sequenceSnapshot{{Table: "commands", Column: "seq", Value: 1}}); err == nil {
		t.Fatal("ignored sequence insert failure")
	}
	for _, name := range []string{"unqualified", "schema.bad-name"} {
		if _, err := (SQL{Dialect: postgres.Dialect}).sequenceName(t.Context(), &failingQueryer{db: db, rows: [][]any{{name}}}, "commands", "seq"); err == nil {
			t.Fatal("accepted unsafe catalog sequence")
		}
	}
	if _, err := (SQL{Dialect: postgres.Dialect}).snapshotSequences(t.Context(), &failingQueryer{db: db, rows: [][]any{{"public.commands_seq"}}}, []tableSnapshot{{Name: "commands"}}); err == nil {
		t.Fatal("ignored high-water mark failure")
	}
	if err := (SQL{Dialect: mysql.Dialect}).restoreSequences(t.Context(), &failingQueryer{db: db}, []sequenceSnapshot{{Table: "bad-name", Column: "seq"}}); err == nil {
		t.Fatal("accepted unsafe sequence table")
	}
	for _, seq := range []sequenceSnapshot{{Table: "bad-name", Column: "seq"}, {Table: "commands", Column: "bad-column"}, {Table: "commands", Column: "seq"}} {
		if err := (SQL{Dialect: sqlite.Dialect}).checkSequenceHighWater(t.Context(), &failingQueryer{db: db}, []sequenceSnapshot{seq}); err == nil {
			t.Fatal("accepted unavailable or unsafe cursor", seq)
		}
	}
}

func TestSnapshotRejectsBrokenCompiledAndStoredSchemas(t *testing.T) {
	for _, fault := range []string{"missing migration files", "wrong version", "corrupt cursor", "wrong backend"} {
		t.Run(fault, func(t *testing.T) {
			s := sqlFixture(t, emptySQLite(t), sqlite.Dialect)
			switch fault {
			case "missing migration files":
				s.Schema[0].FS = nil
			case "wrong version":
				if _, err := s.DB.ExecContext(t.Context(), "UPDATE schema_migrations SET version = 99"); err != nil {
					t.Fatal(err)
				}
			case "corrupt cursor":
				if _, err := s.DB.ExecContext(t.Context(), "INSERT INTO sqlite_sequence(name, seq) VALUES ('commands', 'invalid')"); err != nil {
					t.Fatal(err)
				}
			case "wrong backend":
				s.Dialect = mysql.Dialect
			}
			if err := s.Snapshot(t.Context(), filepath.Join(t.TempDir(), "snapshot")); err == nil {
				t.Fatal("accepted inconsistent snapshot source")
			}
		})
	}
}

func TestRestoreRejectsAmbiguousSnapshotCatalog(t *testing.T) {
	s := sqlFixture(t, emptySQLite(t), sqlite.Dialect)
	dir := filepath.Join(t.TempDir(), "snapshot")
	if err := s.Snapshot(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"unknown schema", "duplicate schema", "missing table", "duplicate column", "missing cursor", "missing compiled files"} {
		t.Run(fault, func(t *testing.T) {
			var info databaseSnapshot
			if err := readJSONFile(filepath.Join(dir, "database.json"), &info); err != nil {
				t.Fatal(err)
			}
			candidate := s
			candidate.Schema = append([]sqlcommon.MigrationSet{}, s.Schema...)
			switch fault {
			case "unknown schema":
				info.Schemas[0].Name = "unregistered"
			case "duplicate schema":
				info.Schemas = append(info.Schemas, info.Schemas[0])
			case "missing table":
				info.Tables = info.Tables[1:]
			case "duplicate column":
				info.Tables[0].Columns = append(info.Tables[0].Columns, info.Tables[0].Columns[0])
			case "missing cursor":
				info.Sequences = nil
			case "missing compiled files":
				candidate.Schema[0].FS = nil
			}
			if _, err := candidate.validateSnapshot(info); err == nil {
				t.Fatal("accepted ambiguous catalog")
			}
		})
	}
}

func TestRestoreTableRefusesUnavailableOrUnsafeDestinationSchema(t *testing.T) {
	s := SQL{DB: emptySQLite(t), Dialect: sqlite.Dialect}
	for _, query := range []string{`CREATE TABLE invalid_column ("bad-column" TEXT)`, `CREATE VIEW unwriteable AS SELECT 'value' AS column_name`} {
		if _, err := s.DB.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	for _, tab := range []tableSnapshot{{Name: "bad-name"}, {Name: "missing"}, {Name: "invalid_column", Columns: []string{"bad-column"}}, {Name: "unwriteable", Columns: []string{"column_name"}}} {
		tx, err := s.DB.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		err = s.restoreTable(t.Context(), tx, t.TempDir(), tab)
		_ = tx.Rollback()
		if err == nil {
			t.Fatal("accepted unsafe restore schema", tab.Name)
		}
	}
}

func TestRestoreRequiresReadableSchemaAndEmptyConnection(t *testing.T) {
	source := sqlFixture(t, emptySQLite(t), sqlite.Dialect)
	dir := filepath.Join(t.TempDir(), "snapshot")
	if err := source.Snapshot(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	closed := emptySQLite(t)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	s := SQL{DB: closed, Dialect: source.Dialect, Schema: source.Schema}
	if err := s.Restore(t.Context(), dir); err == nil {
		t.Fatal("ignored target connection failure")
	}
	if err := source.Restore(t.Context(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("accepted missing snapshot")
	}
	if _, err := OpenDatabase(t.Context(), "unknown", "dsn"); err == nil {
		t.Fatal("accepted unsupported database")
	}
	if _, err := OpenDatabase(t.Context(), "sqlite", ""); err == nil {
		t.Fatal("accepted empty DSN")
	}
	if _, err := OpenDatabase(t.Context(), "mysql", "bad DSN"); err == nil {
		t.Fatal("accepted malformed DSN")
	}
	if _, err := OpenDatabase(t.Context(), "postgres", "invalid DSN"); err == nil {
		t.Fatal("accepted malformed postgres DSN")
	}
	if _, err := OpenDatabase(t.Context(), "sqlite", filepath.Join(t.TempDir(), "missing", "database")); err == nil {
		t.Fatal("accepted missing database directory")
	}
}

// Ensure decoder errors are distinguishable from a completed stream.
func TestCorruptMetadataIsNotEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata")
	if err := writePrivate(path, []byte(`{"version":`)); err != nil {
		t.Fatal(err)
	}
	if err := readJSONFile(path, new(databaseSnapshot)); err == nil || errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if err := readJSONFile(path, nil); err == nil {
		t.Fatal("accepted nil metadata target")
	}
	if err := writeJSONFile(filepath.Join(t.TempDir(), "raw"), json.RawMessage(`{`)); err == nil {
		t.Fatal("accepted malformed metadata output")
	}
}
