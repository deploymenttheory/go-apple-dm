package sqlstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlstore "github.com/deploymenttheory/go-apple-dm/server/ddmstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	schemaddm "github.com/deploymenttheory/go-apple-dm/v3/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/ddm/ddmtest"
)

var (
	t0   = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	dev  = ddmtest.Device(1)
	dev2 = ddmtest.Device(2)
	conf = schemaddm.KindConfiguration
)

// openDB opens a fresh SQLite database file under t.TempDir.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "ddm.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// open returns a migrated store on a fresh database.
func open(t *testing.T) *sqlstore.Store {
	t.Helper()
	s, err := sqlstore.Open(context.Background(), openDB(t), sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// exec runs raw SQL against the store's pool.
func exec(t *testing.T, s *sqlstore.Store, stmts ...string) {
	t.Helper()
	for _, stmt := range stmts {
		if _, err := s.DB().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// hardError asserts err is neither nil nor one of the sentinels a caller
// would act on, so a failing backend is never mistaken for missing or
// rejected data.
func hardError(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil || errors.Is(err, ddm.ErrNotFound) || errors.Is(err, ddm.ErrInvalid) || errors.Is(err, ddm.ErrConflict) {
		t.Errorf("%s: %v", name, err)
	}
}

func TestContract(t *testing.T) {
	t.Parallel()
	ddmtest.RunAll(t, func(t *testing.T) ddm.Store { return open(t) })
}

func TestOpenMigrates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.DB() != db {
		t.Fatal("DB() is not the pool given")
	}
	if v, err := sqlstore.Version(ctx, db, sqlite.Dialect); err != nil || v != 1 {
		t.Fatalf("version %d %v", v, err)
	}
	if _, err := s.PutSet(ctx, "s", t0); err != nil {
		t.Fatal(err)
	}
	// Opening again over the migrated schema is idempotent and keeps data.
	again, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.GetSet(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	// The MDM storage schema can share the database: its own migration
	// table is untouched by ours.
	if _, err := sqlcommon.Migrate(ctx, db, sqlite.Dialect); err != nil {
		t.Fatal(err)
	}
	if v, err := sqlcommon.Version(ctx, db, sqlite.Dialect); err != nil || v != 1 {
		t.Fatalf("storage version %d %v", v, err)
	}
	if _, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 0); err != nil {
		t.Fatal(err)
	}
	if v, err := sqlcommon.Version(ctx, db, sqlite.Dialect); err != nil || v != 1 {
		t.Fatalf("storage version after ddm rollback %d %v", v, err)
	}
	// Failures: a nil pool, a closed pool, and a conflicting schema.
	if _, err := sqlstore.Open(ctx, nil, sqlite.Dialect, sqlstore.Options{}); !errors.Is(err, ddm.ErrInvalid) {
		t.Fatalf("nil db: %v", err)
	}
	closed := openDB(t)
	_ = closed.Close()
	if _, err := sqlstore.Open(ctx, closed, sqlite.Dialect, sqlstore.Options{}); err == nil {
		t.Fatal("closed db opened")
	}
	conflict := openDB(t)
	if _, err := conflict.ExecContext(ctx, "CREATE TABLE ddm_sets (x INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Open(ctx, conflict, sqlite.Dialect, sqlstore.Options{}); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("conflicting schema: %v", err)
	}
}

func TestOpenSkipMigrate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := sqlstore.Version(ctx, db, sqlite.Dialect); err != nil || v != 0 {
		t.Fatalf("version %d %v", v, err)
	}
	if _, err := s.GetSet(ctx, "s"); err == nil || errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("query without schema must be a real error: %v", err)
	}
	if applied, err := sqlstore.Migrate(ctx, db, sqlite.Dialect); err != nil || len(applied) != 1 {
		t.Fatalf("migrate: %v %v", applied, err)
	}
	if _, err := s.GetSet(ctx, "s"); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("after migrate: %v", err)
	}
}

func TestOpenUnsupportedDialect(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t)
	d := sqlcommon.Dialect{Name: "oracle"}
	if _, err := sqlstore.MigrationSet(d); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
		t.Fatalf("MigrationSet: %v", err)
	}
	if _, err := sqlstore.Open(ctx, db, d, sqlstore.Options{SkipMigrate: true}); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
		t.Fatalf("Open: %v", err)
	}
	if _, err := sqlstore.Migrate(ctx, db, d); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := sqlstore.Rollback(ctx, db, d, 0); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := sqlstore.Version(ctx, db, d); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
		t.Fatalf("Version: %v", err)
	}
	for _, name := range []string{"sqlite", "postgres", "mysql"} {
		set, err := sqlstore.MigrationSet(sqlcommon.Dialect{Name: name})
		if err != nil || set.Table != sqlstore.MigrationsTable {
			t.Fatalf("%s: %+v %v", name, set, err)
		}
	}
}

func TestMigrateRollbackVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t)
	d := sqlite.Dialect
	if applied, err := sqlstore.Migrate(ctx, db, d); err != nil || len(applied) != 1 || applied[0] != 1 {
		t.Fatalf("migrate: %v %v", applied, err)
	}
	if applied, err := sqlstore.Migrate(ctx, db, d); err != nil || len(applied) != 0 {
		t.Fatalf("second migrate: %v %v", applied, err)
	}
	if v, err := sqlstore.Version(ctx, db, d); err != nil || v != 1 {
		t.Fatalf("version %d %v", v, err)
	}
	if reverted, err := sqlstore.Rollback(ctx, db, d, 0); err != nil || len(reverted) != 1 {
		t.Fatalf("rollback: %v %v", reverted, err)
	}
	if v, err := sqlstore.Version(ctx, db, d); err != nil || v != 0 {
		t.Fatalf("version after rollback %d %v", v, err)
	}
	if reverted, err := sqlstore.Rollback(ctx, db, d, 0); err != nil || len(reverted) != 0 {
		t.Fatalf("second rollback: %v %v", reverted, err)
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM ddm_declarations"); err == nil {
		t.Fatal("tables survived rollback")
	}
	if applied, err := sqlstore.Migrate(ctx, db, d); err != nil || len(applied) != 1 {
		t.Fatalf("re-migrate: %v %v", applied, err)
	}
	// A down statement that fails surfaces.
	if _, err := db.ExecContext(ctx, "DROP TABLE ddm_changes"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Rollback(ctx, db, d, 0); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("rollback over a missing table: %v", err)
	}
	// An up statement that fails surfaces.
	if _, err := db.ExecContext(ctx, "DELETE FROM "+sqlstore.MigrationsTable); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Migrate(ctx, db, d); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("migrate over a conflicting schema: %v", err)
	}
	// An unreadable version table surfaces.
	for _, stmt := range []string{
		"DROP TABLE " + sqlstore.MigrationsTable,
		"CREATE TABLE " + sqlstore.MigrationsTable + " (version TEXT)",
		"INSERT INTO " + sqlstore.MigrationsTable + " (version) VALUES ('abc')",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sqlstore.Version(ctx, db, d); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("unscannable version: %v", err)
	}
}

// TestMigrationsAgreeAcrossDialects pins the three embedded migration
// directories to the same version and name sequence with a down section
// each, so a schema change cannot land in one engine only.
func TestMigrationsAgreeAcrossDialects(t *testing.T) {
	t.Parallel()
	load := func(d sqlcommon.Dialect) []sqlcommon.Migration {
		t.Helper()
		set, err := sqlstore.MigrationSet(d)
		if err != nil {
			t.Fatal(err)
		}
		ms, err := sqlcommon.LoadMigrations(set.FS)
		if err != nil || len(ms) == 0 {
			t.Fatalf("%s migrations: %v %v", d.Name, ms, err)
		}
		return ms
	}
	ref := load(sqlite.Dialect)
	for _, d := range []sqlcommon.Dialect{postgres.Dialect, mysql.Dialect} {
		ms := load(d)
		if len(ms) != len(ref) {
			t.Fatalf("%s has %d migrations, sqlite has %d", d.Name, len(ms), len(ref))
		}
		for i := range ref {
			if ms[i].Version != ref[i].Version || ms[i].Name != ref[i].Name {
				t.Fatalf("%s migration %d is %d_%s, sqlite has %d_%s", d.Name, i, ms[i].Version, ms[i].Name, ref[i].Version, ref[i].Name)
			}
			if len(ms[i].Down) == 0 {
				t.Fatalf("%s %d_%s has no down section", d.Name, ms[i].Version, ms[i].Name)
			}
			// Every dialect creates and drops the same thirteen tables.
			if up, down := countStatements(ms[i].Up, "CREATE TABLE"), countStatements(ms[i].Down, "DROP TABLE"); up != 13 || down != 13 {
				t.Fatalf("%s %d_%s creates %d tables and drops %d, want 13", d.Name, ms[i].Version, ms[i].Name, up, down)
			}
		}
	}
}

func countStatements(stmts []string, prefix string) int {
	n := 0
	for _, s := range stmts {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

// calls exercises every Tx method with valid arguments.
func calls(s ddm.Tx) map[string]func(context.Context) error {
	d := ddmtest.Decl("a", conf, `{}`)
	snap := &ddm.Snapshot{ID: dev, DeclarationsToken: "t", TokenChangedAt: t0, RefreshedAt: t0, Items: []ddm.SnapshotItem{{DeclarationRef: ddm.DeclarationRef{Kind: conf, Identifier: "a", ServerToken: "t"}, BaseToken: "t"}}}
	update := ddm.StatusUpdate{Raw: []byte("{}"), ReceivedAt: t0, FullReport: true, HasDeclarations: true,
		Declarations: []ddm.DeclarationStatus{{Kind: conf, Identifier: "a", ServerToken: "t", Valid: "valid"}},
		Values:       []ddm.StatusValue{{Path: "p", Value: []byte("1")}},
		Errors:       []ddm.StatusError{{StatusItem: "x"}}, KeepReports: 1}
	return map[string]func(context.Context) error{
		"PutDeclaration":        func(ctx context.Context) error { _, err := s.PutDeclaration(ctx, d); return err },
		"GetDeclaration":        func(ctx context.Context) error { _, err := s.GetDeclaration(ctx, "a"); return err },
		"GetDeclarationVersion": func(ctx context.Context) error { _, err := s.GetDeclarationVersion(ctx, "a", "t"); return err },
		"DeleteDeclaration":     func(ctx context.Context) error { return s.DeleteDeclaration(ctx, "a") },
		"ListDeclarations": func(ctx context.Context) error {
			_, err := s.ListDeclarations(ctx, ddm.DeclarationQuery{}, paging.Page{})
			return err
		},
		"PruneVersions":          func(ctx context.Context) error { _, err := s.PruneVersions(ctx); return err },
		"PutSet":                 func(ctx context.Context) error { _, err := s.PutSet(ctx, "s", t0); return err },
		"DeleteSet":              func(ctx context.Context) error { return s.DeleteSet(ctx, "s") },
		"GetSet":                 func(ctx context.Context) error { _, err := s.GetSet(ctx, "s"); return err },
		"ListSets":               func(ctx context.Context) error { _, err := s.ListSets(ctx, paging.Page{}); return err },
		"AddSetDeclaration":      func(ctx context.Context) error { _, err := s.AddSetDeclaration(ctx, "s", "a", t0); return err },
		"RemoveSetDeclaration":   func(ctx context.Context) error { _, err := s.RemoveSetDeclaration(ctx, "s", "a"); return err },
		"SetDeclarations":        func(ctx context.Context) error { _, err := s.SetDeclarations(ctx, "s"); return err },
		"DeclarationSets":        func(ctx context.Context) error { _, err := s.DeclarationSets(ctx, "a"); return err },
		"AssignSet":              func(ctx context.Context) error { _, err := s.AssignSet(ctx, dev, "s", t0); return err },
		"UnassignSet":            func(ctx context.Context) error { _, err := s.UnassignSet(ctx, dev, "s"); return err },
		"EnrollmentSets":         func(ctx context.Context) error { _, err := s.EnrollmentSets(ctx, dev); return err },
		"SetEnrollments":         func(ctx context.Context) error { _, err := s.SetEnrollments(ctx, "s", paging.Page{}); return err },
		"AssignDeclaration":      func(ctx context.Context) error { _, err := s.AssignDeclaration(ctx, dev, "a", t0); return err },
		"UnassignDeclaration":    func(ctx context.Context) error { _, err := s.UnassignDeclaration(ctx, dev, "a"); return err },
		"EnrollmentDeclarations": func(ctx context.Context) error { _, err := s.EnrollmentDeclarations(ctx, dev); return err },
		"StaticDeclarations":     func(ctx context.Context) error { _, err := s.StaticDeclarations(ctx, dev); return err },
		"AffectedEnrollments": func(ctx context.Context) error {
			_, err := s.AffectedEnrollments(ctx, []string{"a"}, []string{"s"})
			return err
		},
		"PutSnapshot":       func(ctx context.Context) error { return s.PutSnapshot(ctx, snap) },
		"Snapshot":          func(ctx context.Context) error { _, err := s.Snapshot(ctx, dev); return err },
		"PutStatus":         func(ctx context.Context) error { _, err := s.PutStatus(ctx, dev, update); return err },
		"DeclarationStatus": func(ctx context.Context) error { _, err := s.DeclarationStatus(ctx, dev); return err },
		"DeclarationStatusByIdentifier": func(ctx context.Context) error {
			_, err := s.DeclarationStatusByIdentifier(ctx, "a", paging.Page{})
			return err
		},
		"StatusValues": func(ctx context.Context) error {
			_, err := s.StatusValues(ctx, dev, ddm.StatusValueQuery{}, paging.Page{})
			return err
		},
		"StatusErrors":    func(ctx context.Context) error { _, err := s.StatusErrors(ctx, dev, paging.Page{}); return err },
		"StatusReports":   func(ctx context.Context) error { _, err := s.StatusReports(ctx, dev, paging.Page{}); return err },
		"RecordChanges":   func(ctx context.Context) error { return s.RecordChanges(ctx, []mdm.EnrollmentID{dev}, "r", t0) },
		"PendingChanges":  func(ctx context.Context) error { _, err := s.PendingChanges(ctx, t0, 0); return err },
		"CompleteChanges": func(ctx context.Context) error { return s.CompleteChanges(ctx, []int64{1}) },
		"FailChanges":     func(ctx context.Context) error { return s.FailChanges(ctx, []int64{1}, "boom", t0) },
		"ChangeStats":     func(ctx context.Context) error { _, _, err := s.ChangeStats(ctx, t0); return err },
		"ClearEnrollment": func(ctx context.Context) error { return s.ClearEnrollment(ctx, dev) },
	}
}

// TestQueriesFailWithoutSchema drives every method's error path through a
// database whose tables were dropped, outside and inside Update, then
// drops tables one at a time so the later statements of the multi-step
// writes fail too.
func TestQueriesFailWithoutSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range calls(s) {
		hardError(t, name, call(ctx))
	}
	err = s.Update(ctx, func(tx ddm.Tx) error {
		for name, call := range calls(tx) {
			hardError(t, "in Update "+name, call(ctx))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, nil); !errors.Is(err, ddm.ErrInvalid) {
		t.Fatalf("nil callback: %v", err)
	}
	if err := s.Update(ctx, func(tx ddm.Tx) error { return tx.(ddm.Store).Update(ctx, func(ddm.Tx) error { return nil }) }); !errors.Is(err, ddm.ErrInvalid) {
		t.Fatalf("nested Update: %v", err)
	}
	// Later statements of the multi-step writes, one dropped table each.
	steps := []struct {
		drop string
		call func(s ddm.Tx) func(context.Context) error
	}{
		{"ddm_status_errors", func(s ddm.Tx) func(context.Context) error { return calls(s)["PutStatus"] }},
		{"ddm_status_values", func(s ddm.Tx) func(context.Context) error { return calls(s)["PutStatus"] }},
		{"ddm_status_values", func(s ddm.Tx) func(context.Context) error {
			return func(ctx context.Context) error {
				_, err := s.PutStatus(ctx, dev, ddm.StatusUpdate{Values: []ddm.StatusValue{{Path: "p", Value: []byte("1")}}})
				return err
			}
		}},
		{"ddm_status_declarations", func(s ddm.Tx) func(context.Context) error { return calls(s)["PutStatus"] }},
		{"ddm_status_declarations", func(s ddm.Tx) func(context.Context) error {
			return func(ctx context.Context) error {
				_, err := s.PutStatus(ctx, dev, ddm.StatusUpdate{Declarations: []ddm.DeclarationStatus{{Kind: conf, Identifier: "a"}}})
				return err
			}
		}},
		{"ddm_snapshot_items", func(s ddm.Tx) func(context.Context) error { return calls(s)["PutSnapshot"] }},
		{"ddm_snapshot_items", func(s ddm.Tx) func(context.Context) error { return calls(s)["Snapshot"] }},
		{"ddm_set_declarations", func(s ddm.Tx) func(context.Context) error { return calls(s)["AddSetDeclaration"] }},
		{"ddm_declarations", func(s ddm.Tx) func(context.Context) error { return calls(s)["AddSetDeclaration"] }},
		{"ddm_enrollment_sets", func(s ddm.Tx) func(context.Context) error { return calls(s)["AssignSet"] }},
	}
	for _, step := range steps {
		fresh := open(t)
		if _, err := fresh.PutSet(ctx, "s", t0); err != nil {
			t.Fatal(err)
		}
		if _, err := fresh.PutDeclaration(ctx, ddmtest.Decl("a", conf, `{}`)); err != nil {
			t.Fatal(err)
		}
		if err := fresh.PutSnapshot(ctx, &ddm.Snapshot{ID: dev, DeclarationsToken: "t", TokenChangedAt: t0, RefreshedAt: t0}); err != nil {
			t.Fatal(err)
		}
		exec(t, fresh, "DROP TABLE "+step.drop)
		hardError(t, "after dropping "+step.drop, step.call(fresh)(ctx))
	}
	// A pool that is closed cannot begin a transaction.
	_ = db.Close()
	hardError(t, "Update on closed pool", s.Update(ctx, func(ddm.Tx) error { return nil }))
}

// seed gives the store a declaration a and b, a set s holding a, an
// enrollment dev with the set and b assigned, a snapshot, a status report
// with one declaration row and one value, and one change; it returns the
// change's seq.
func seed(t *testing.T, s ddm.Tx) int64 {
	t.Helper()
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := s.PutDeclaration(ctx, ddmtest.Decl(id, conf, `{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PutSet(ctx, "s", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSetDeclaration(ctx, "s", "a", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignSet(ctx, dev, "s", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignDeclaration(ctx, dev, "b", t0); err != nil {
		t.Fatal(err)
	}
	snap := &ddm.Snapshot{ID: dev, DeclarationsToken: "t", TokenChangedAt: t0, RefreshedAt: t0, Items: []ddm.SnapshotItem{{DeclarationRef: ddm.DeclarationRef{Kind: conf, Identifier: "a", ServerToken: "t"}, BaseToken: "t"}}}
	if err := s.PutSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	u := ddm.StatusUpdate{Raw: []byte("{}"), ReceivedAt: t0, FullReport: true, HasDeclarations: true,
		Declarations: []ddm.DeclarationStatus{{Kind: conf, Identifier: "a", ServerToken: "t", Valid: "valid"}},
		Values:       []ddm.StatusValue{{Path: "p", Value: []byte("1")}}}
	for range 2 {
		if _, err := s.PutStatus(ctx, dev, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordChanges(ctx, []mdm.EnrollmentID{dev}, "r", t0); err != nil {
		t.Fatal(err)
	}
	rows, err := s.PendingChanges(ctx, t0, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("pending: %v %v", rows, err)
	}
	return rows[0].Seq
}

// TestWriteFailuresSurface uses RAISE triggers so each write statement of
// every multi-step method fails in turn, proving the error is returned
// rather than swallowed and that the statements before it roll back.
func TestWriteFailuresSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	seq := seed(t, s)
	partial := func(u ddm.StatusUpdate) func() error {
		return func() error { _, err := s.PutStatus(ctx, dev, u); return err }
	}
	decl := func(id, payload string) func() error {
		return func() error { _, err := s.PutDeclaration(ctx, ddmtest.Decl(id, conf, payload)); return err }
	}
	snapshot := func(id mdm.EnrollmentID, items ...ddm.SnapshotItem) func() error {
		return func() error {
			return s.PutSnapshot(ctx, &ddm.Snapshot{ID: id, DeclarationsToken: "t2", TokenChangedAt: t0, RefreshedAt: t0, Items: items})
		}
	}
	item := ddm.SnapshotItem{DeclarationRef: ddm.DeclarationRef{Kind: conf, Identifier: "b", ServerToken: "t"}, BaseToken: "t"}
	cases := []struct {
		table, event string
		call         func() error
		// intact checks the state the failed call would have changed.
		intact func() bool
	}{
		{"ddm_declarations", "INSERT", decl("c", `{}`), nil},
		{"ddm_declarations", "UPDATE", decl("a", `{"Echo":"2"}`), nil},
		{"ddm_declaration_versions", "INSERT", decl("d", `{}`), func() bool { _, err := s.GetDeclaration(ctx, "d"); return errors.Is(err, ddm.ErrNotFound) }},
		{"ddm_declaration_versions", "DELETE", func() error { return s.DeleteDeclaration(ctx, "a") }, nil},
		{"ddm_declarations", "DELETE", func() error { return s.DeleteDeclaration(ctx, "a") }, func() bool { m, _ := s.SetDeclarations(ctx, "s"); return len(m) == 1 }},
		{"ddm_sets", "INSERT", func() error { _, err := s.PutSet(ctx, "s2", t0); return err }, nil},
		{"ddm_sets", "DELETE", func() error { return s.DeleteSet(ctx, "s") }, func() bool { m, _ := s.SetDeclarations(ctx, "s"); return len(m) == 1 }},
		{"ddm_set_declarations", "INSERT", func() error { _, err := s.AddSetDeclaration(ctx, "s", "b", t0); return err }, nil},
		{"ddm_sets", "UPDATE", func() error { _, err := s.AddSetDeclaration(ctx, "s", "b", t0); return err }, func() bool { m, _ := s.SetDeclarations(ctx, "s"); return len(m) == 1 }},
		{"ddm_set_declarations", "DELETE", func() error { _, err := s.RemoveSetDeclaration(ctx, "s", "a"); return err }, nil},
		{"ddm_enrollment_sets", "INSERT", func() error { _, err := s.AssignSet(ctx, dev2, "s", t0); return err }, nil},
		{"ddm_enrollment_sets", "DELETE", func() error { _, err := s.UnassignSet(ctx, dev, "s"); return err }, nil},
		{"ddm_enrollment_declarations", "INSERT", func() error { _, err := s.AssignDeclaration(ctx, dev, "a", t0); return err }, nil},
		{"ddm_enrollment_declarations", "DELETE", func() error { _, err := s.UnassignDeclaration(ctx, dev, "b"); return err }, nil},
		{"ddm_snapshots", "INSERT", snapshot(dev2), nil},
		{"ddm_snapshots", "UPDATE", snapshot(dev), nil},
		{"ddm_snapshot_items", "DELETE", snapshot(dev), nil},
		{"ddm_snapshot_items", "INSERT", snapshot(dev, item), func() bool {
			snap, _ := s.Snapshot(ctx, dev)
			return snap.DeclarationsToken == "t" && snap.Items[0].Identifier == "a"
		}},
		{"ddm_status_reports", "INSERT", partial(ddm.StatusUpdate{ReceivedAt: t0}), nil},
		{"ddm_status_declarations", "INSERT", partial(ddm.StatusUpdate{Declarations: []ddm.DeclarationStatus{{Kind: conf, Identifier: "b"}}}), nil},
		{"ddm_status_declarations", "DELETE", partial(ddm.StatusUpdate{FullReport: true, HasDeclarations: true}), nil},
		{"ddm_status_values", "INSERT", partial(ddm.StatusUpdate{Values: []ddm.StatusValue{{Path: "q", Value: []byte("2")}}}), nil},
		{"ddm_status_values", "DELETE", partial(ddm.StatusUpdate{FullReport: true}), nil},
		{"ddm_status_errors", "INSERT", partial(ddm.StatusUpdate{Errors: []ddm.StatusError{{StatusItem: "x"}}}), nil},
		{"ddm_status_reports", "DELETE", partial(ddm.StatusUpdate{KeepReports: 1}), func() bool { r, _ := s.StatusReports(ctx, dev, paging.Page{}); return len(r.Items) == 2 }},
		{"ddm_changes", "INSERT", func() error { return s.RecordChanges(ctx, []mdm.EnrollmentID{dev}, "r", t0) }, nil},
		{"ddm_changes", "DELETE", func() error { return s.CompleteChanges(ctx, []int64{seq}) }, nil},
		{"ddm_changes", "UPDATE", func() error { return s.FailChanges(ctx, []int64{seq}, "boom", t0) }, nil},
		{"ddm_enrollment_sets", "DELETE", func() error { return s.ClearEnrollment(ctx, dev) }, nil},
		{"ddm_changes", "DELETE", func() error { return s.ClearEnrollment(ctx, dev) }, func() bool { sets, _ := s.EnrollmentSets(ctx, dev); return len(sets) == 1 }},
	}
	for _, c := range cases {
		name := c.event + " " + c.table
		exec(t, s, "CREATE TRIGGER guard BEFORE "+c.event+" ON "+c.table+" BEGIN SELECT RAISE(FAIL, 'writes disabled'); END")
		err := c.call()
		if err == nil || errors.Is(err, ddm.ErrNotFound) || errors.Is(err, ddm.ErrInvalid) || !strings.Contains(err.Error(), "writes disabled") {
			t.Errorf("%s: %v", name, err)
		}
		if c.intact != nil && !c.intact() {
			t.Errorf("%s: earlier statements were not rolled back", name)
		}
		exec(t, s, "DROP TRIGGER guard")
	}
}

func TestUniqueViolationMapsToConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	// Two items with one key can only be a caller error, but it takes the
	// same path a lost race takes: the driver error is reported as
	// ErrConflict and still recognisable as the engine's unique violation.
	ref := ddm.DeclarationRef{Kind: conf, Identifier: "a", ServerToken: "t"}
	snap := &ddm.Snapshot{ID: dev, DeclarationsToken: "t", TokenChangedAt: t0, RefreshedAt: t0, Items: []ddm.SnapshotItem{{DeclarationRef: ref, BaseToken: "t"}, {DeclarationRef: ref, BaseToken: "t"}}}
	err := s.PutSnapshot(ctx, snap)
	if !errors.Is(err, ddm.ErrConflict) || !sqlite.IsUniqueViolation(err) {
		t.Fatalf("duplicate items: %v", err)
	}
	// The retries never committed the partial snapshot.
	if _, err := s.Snapshot(ctx, dev); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatalf("snapshot after conflict: %v", err)
	}
	err = s.Update(ctx, func(tx ddm.Tx) error { return tx.PutSnapshot(ctx, snap) })
	if !errors.Is(err, ddm.ErrConflict) {
		t.Fatalf("duplicate items in Update: %v", err)
	}
	// A dialect that cannot classify errors reports the raw failure.
	plain := sqlite.Dialect
	plain.IsUniqueViolation = nil
	ps, err := sqlstore.Open(ctx, s.DB(), plain, sqlstore.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.PutSnapshot(ctx, snap); err == nil || errors.Is(err, ddm.ErrConflict) {
		t.Fatalf("without IsUniqueViolation: %v", err)
	}
}

func TestCanonicalBytesRoundTripExactly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	// Every byte value, including NUL, plus whitespace and non-ASCII the
	// engine must not normalise (decision record 0020 rejects JSON column
	// types for that reason).
	raw := make([]byte, 0, 300)
	for i := range 256 {
		raw = append(raw, byte(i))
	}
	raw = append(raw, []byte("  {\"Z\":1,\n\"a\":\"é \"}  ")...)
	d := &ddm.Declaration{Identifier: "a", Type: "com.apple.configuration.test", Kind: conf, ServerToken: ddm.TokenFor(raw), Canonical: bytes.Clone(raw), CreatedAt: t0, UpdatedAt: t0}
	if _, err := s.PutDeclaration(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssignDeclaration(ctx, dev, "a", t0); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDeclaration(ctx, "a")
	if err != nil || !bytes.Equal(got.Canonical, raw) {
		t.Fatalf("Get: %v %q", err, got)
	}
	v, err := s.GetDeclarationVersion(ctx, "a", d.ServerToken)
	if err != nil || !bytes.Equal(v.Canonical, raw) {
		t.Fatalf("GetVersion: %v", err)
	}
	list, err := s.ListDeclarations(ctx, ddm.DeclarationQuery{}, paging.Page{})
	if err != nil || len(list.Items) != 1 || !bytes.Equal(list.Items[0].Canonical, raw) {
		t.Fatalf("List: %v", err)
	}
	static, err := s.StaticDeclarations(ctx, dev)
	if err != nil || len(static) != 1 || !bytes.Equal(static[0].Canonical, raw) {
		t.Fatalf("Static: %v", err)
	}
	// Expanded bytes, status values, reasons, and raw reports likewise;
	// empty optional blobs read back as nil.
	snap := &ddm.Snapshot{ID: dev, DeclarationsToken: "t", TokenChangedAt: t0, RefreshedAt: t0, Items: []ddm.SnapshotItem{
		{DeclarationRef: ddm.DeclarationRef{Kind: conf, Identifier: "a", ServerToken: "x"}, BaseToken: "b", Expanded: bytes.Clone(raw)},
		{DeclarationRef: ddm.DeclarationRef{Kind: conf, Identifier: "b", ServerToken: "x"}, BaseToken: "b", Expanded: []byte{}},
	}}
	if err := s.PutSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	gotSnap, err := s.Snapshot(ctx, dev)
	if err != nil || !bytes.Equal(gotSnap.Items[0].Expanded, raw) || gotSnap.Items[1].Expanded != nil {
		t.Fatalf("Snapshot: %v %+v", err, gotSnap)
	}
	u := ddm.StatusUpdate{Raw: bytes.Clone(raw), ReceivedAt: t0,
		Declarations: []ddm.DeclarationStatus{{Kind: conf, Identifier: "a", Reasons: bytes.Clone(raw)}, {Kind: conf, Identifier: "b", Reasons: []byte{}}},
		Values:       []ddm.StatusValue{{Path: "p", Value: bytes.Clone(raw)}, {Path: "q"}},
		Errors:       []ddm.StatusError{{StatusItem: "x", Reasons: bytes.Clone(raw)}, {StatusItem: "y"}}}
	if _, err := s.PutStatus(ctx, dev, u); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DeclarationStatus(ctx, dev)
	if err != nil || !bytes.Equal(rows[0].Reasons, raw) || rows[1].Reasons != nil {
		t.Fatalf("DeclarationStatus: %v %+v", err, rows)
	}
	byID, err := s.DeclarationStatusByIdentifier(ctx, "a", paging.Page{})
	if err != nil || !bytes.Equal(byID.Items[0].Reasons, raw) {
		t.Fatalf("ByIdentifier: %v", err)
	}
	vals, err := s.StatusValues(ctx, dev, ddm.StatusValueQuery{}, paging.Page{})
	if err != nil || !bytes.Equal(vals.Items[0].Value, raw) || len(vals.Items[1].Value) != 0 {
		t.Fatalf("StatusValues: %v %+v", err, vals)
	}
	errs, err := s.StatusErrors(ctx, dev, paging.Page{})
	if err != nil || errs.Items[0].Reasons != nil || !bytes.Equal(errs.Items[1].Reasons, raw) {
		t.Fatalf("StatusErrors: %v %+v", err, errs)
	}
	reports, err := s.StatusReports(ctx, dev, paging.Page{})
	if err != nil || !bytes.Equal(reports.Items[0].Raw, raw) {
		t.Fatalf("StatusReports: %v", err)
	}
	// A declaration without bytes is stored as such.
	if _, err := s.PutDeclaration(ctx, &ddm.Declaration{Identifier: "e", Kind: conf, ServerToken: "t"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetDeclaration(ctx, "e"); err != nil || len(got.Canonical) != 0 {
		t.Fatalf("empty canonical: %v %q", err, got.Canonical)
	}
	// A report without bytes reads back nil.
	if _, err := s.PutStatus(ctx, dev2, ddm.StatusUpdate{ReceivedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if reports, err := s.StatusReports(ctx, dev2, paging.Page{}); err != nil || reports.Items[0].Raw != nil {
		t.Fatalf("empty raw: %v %+v", err, reports)
	}
}

func TestBadCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	seed(t, s)
	for _, cursor := range []string{"x", "1.5", "-", "9223372036854775808"} {
		if _, err := s.StatusErrors(ctx, dev, paging.Page{Cursor: cursor}); !errors.Is(err, ddm.ErrInvalid) {
			t.Errorf("StatusErrors cursor %q: %v", cursor, err)
		}
		if _, err := s.StatusReports(ctx, dev, paging.Page{Cursor: cursor}); !errors.Is(err, ddm.ErrInvalid) {
			t.Errorf("StatusReports cursor %q: %v", cursor, err)
		}
	}
	// A seq cursor before every row is an empty page, not an error.
	if r, err := s.StatusReports(ctx, dev, paging.Page{Cursor: "0"}); err != nil || len(r.Items) != 0 || r.NextCursor != "" {
		t.Fatalf("cursor 0: %+v %v", r, err)
	}
	// String cursors are pure keyset: any value is a position.
	if r, err := s.ListDeclarations(ctx, ddm.DeclarationQuery{}, paging.Page{Cursor: "zzz"}); err != nil || len(r.Items) != 0 {
		t.Fatalf("declarations after zzz: %+v %v", r, err)
	}
	if r, err := s.ListDeclarations(ctx, ddm.DeclarationQuery{}, paging.Page{Cursor: "a"}); err != nil || len(r.Items) != 1 || r.Items[0].Identifier != "b" {
		t.Fatalf("declarations after a: %+v %v", r, err)
	}
	if r, err := s.ListSets(ctx, paging.Page{Cursor: "%"}); err != nil || len(r.Items) != 1 {
		t.Fatalf("sets after %%: %+v %v", r, err)
	}
	if r, err := s.SetEnrollments(ctx, "s", paging.Page{Cursor: dev.ID}); err != nil || len(r.Items) != 0 {
		t.Fatalf("enrollments after dev: %+v %v", r, err)
	}
	if r, err := s.DeclarationStatusByIdentifier(ctx, "a", paging.Page{Cursor: "'"}); err != nil || len(r.Items) != 1 {
		t.Fatalf("status after quote: %+v %v", r, err)
	}
	if r, err := s.StatusValues(ctx, dev, ddm.StatusValueQuery{}, paging.Page{Cursor: "p"}); err != nil || len(r.Items) != 0 {
		t.Fatalf("values after p: %+v %v", r, err)
	}
	// Empty batches are no-ops.
	if err := s.CompleteChanges(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.FailChanges(ctx, nil, "boom", t0); err != nil {
		t.Fatal(err)
	}
}

// TestScanFailuresSurface rebuilds a table with looser column types and
// plants a row the scanner cannot read, so every read path reports the
// scan error rather than a partial result.
func TestScanFailuresSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name  string
		setup []string
		calls func(s *sqlstore.Store) map[string]func() error
	}{
		{"declarations", []string{
			"DROP TABLE ddm_declarations",
			"CREATE TABLE ddm_declarations (identifier TEXT PRIMARY KEY, type TEXT, kind TEXT, server_token TEXT, canonical BLOB, created_at TEXT, updated_at TEXT)",
			"INSERT INTO ddm_declarations VALUES ('a', 't', 'configuration', 'tok', x'00', 'bad', 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			if _, err := s.AssignDeclaration(ctx, dev, "a", t0); err != nil {
				t.Fatal(err)
			}
			return map[string]func() error{
				"GetDeclaration":     func() error { _, err := s.GetDeclaration(ctx, "a"); return err },
				"ListDeclarations":   func() error { _, err := s.ListDeclarations(ctx, ddm.DeclarationQuery{}, paging.Page{}); return err },
				"StaticDeclarations": func() error { _, err := s.StaticDeclarations(ctx, dev); return err },
			}
		}},
		{"versions", []string{
			"DROP TABLE ddm_declaration_versions",
			"CREATE TABLE ddm_declaration_versions (identifier TEXT, server_token TEXT, type TEXT, canonical BLOB, created_at TEXT)",
			"INSERT INTO ddm_declaration_versions VALUES ('a', 'tok', 't', x'00', 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{"GetDeclarationVersion": func() error { _, err := s.GetDeclarationVersion(ctx, "a", "tok"); return err }}
		}},
		{"sets", []string{
			"DROP TABLE ddm_sets",
			"CREATE TABLE ddm_sets (name TEXT PRIMARY KEY, created_at TEXT, updated_at TEXT)",
			"INSERT INTO ddm_sets VALUES ('s', 'bad', 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{
				"GetSet":   func() error { _, err := s.GetSet(ctx, "s"); return err },
				"ListSets": func() error { _, err := s.ListSets(ctx, paging.Page{}); return err },
			}
		}},
		{"set declarations", []string{
			"INSERT INTO ddm_sets VALUES ('s', '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')",
			"DROP TABLE ddm_set_declarations",
			"CREATE TABLE ddm_set_declarations (set_name TEXT, identifier TEXT NULL, added_at TEXT)",
			"INSERT INTO ddm_set_declarations VALUES ('s', NULL, 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{"SetDeclarations": func() error { _, err := s.SetDeclarations(ctx, "s"); return err }}
		}},
		{"enrollment sets", []string{
			"INSERT INTO ddm_sets VALUES ('s', '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')",
			"DROP TABLE ddm_enrollment_sets",
			"CREATE TABLE ddm_enrollment_sets (enrollment_id TEXT, channel TEXT, parent_id TEXT, set_name TEXT, assigned_at TEXT)",
			"INSERT INTO ddm_enrollment_sets VALUES ('DEVICE-01', 'x', '', 's', 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{
				"SetEnrollments":      func() error { _, err := s.SetEnrollments(ctx, "s", paging.Page{}); return err },
				"AffectedEnrollments": func() error { _, err := s.AffectedEnrollments(ctx, nil, []string{"s"}); return err },
			}
		}},
		{"snapshots", []string{
			"DROP TABLE ddm_snapshot_items",
			"DROP TABLE ddm_snapshots",
			"CREATE TABLE ddm_snapshots (enrollment_id TEXT PRIMARY KEY, channel INTEGER, parent_id TEXT, declarations_token TEXT, token_changed_at TEXT, refreshed_at TEXT)",
			"INSERT INTO ddm_snapshots VALUES ('DEVICE-01', 1, '', 't', 'bad', 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{"Snapshot": func() error { _, err := s.Snapshot(ctx, dev); return err }}
		}},
		{"snapshot items", []string{
			"INSERT INTO ddm_snapshots VALUES ('DEVICE-01', 1, '', 't', '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')",
			"DROP TABLE ddm_snapshot_items",
			"CREATE TABLE ddm_snapshot_items (enrollment_id TEXT, kind TEXT NULL, identifier TEXT, server_token TEXT, base_token TEXT, expanded BLOB, pos INTEGER)",
			"INSERT INTO ddm_snapshot_items VALUES ('DEVICE-01', NULL, 'a', 't', 't', NULL, 0)",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{"Snapshot": func() error { _, err := s.Snapshot(ctx, dev); return err }}
		}},
		{"status declarations", []string{
			"DROP TABLE ddm_status_declarations",
			"CREATE TABLE ddm_status_declarations (enrollment_id TEXT, channel INTEGER, parent_id TEXT, kind TEXT, identifier TEXT, server_token TEXT NULL, active INTEGER, valid TEXT, reasons BLOB, first_seen TEXT, last_seen TEXT)",
			"INSERT INTO ddm_status_declarations VALUES ('DEVICE-01', 1, '', 'configuration', 'a', NULL, 1, 'valid', NULL, 'bad', 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{
				"DeclarationStatus":             func() error { _, err := s.DeclarationStatus(ctx, dev); return err },
				"DeclarationStatusByIdentifier": func() error { _, err := s.DeclarationStatusByIdentifier(ctx, "a", paging.Page{}); return err },
				"PutStatus full": func() error {
					_, err := s.PutStatus(ctx, dev, ddm.StatusUpdate{FullReport: true, HasDeclarations: true})
					return err
				},
			}
		}},
		{"status values", []string{
			"DROP TABLE ddm_status_values",
			"CREATE TABLE ddm_status_values (enrollment_id TEXT, path TEXT, value BLOB, first_seen TEXT, last_seen TEXT)",
			"INSERT INTO ddm_status_values VALUES ('DEVICE-01', 'p', x'31', 'bad', 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{"StatusValues": func() error { _, err := s.StatusValues(ctx, dev, ddm.StatusValueQuery{}, paging.Page{}); return err }}
		}},
		{"status errors", []string{
			"DROP TABLE ddm_status_errors",
			"CREATE TABLE ddm_status_errors (seq INTEGER PRIMARY KEY, enrollment_id TEXT, status_item TEXT, reasons BLOB, received_at TEXT)",
			"INSERT INTO ddm_status_errors VALUES (1, 'DEVICE-01', 'x', NULL, 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{"StatusErrors": func() error { _, err := s.StatusErrors(ctx, dev, paging.Page{}); return err }}
		}},
		{"status reports", []string{
			"DROP TABLE ddm_status_reports",
			"CREATE TABLE ddm_status_reports (seq INTEGER PRIMARY KEY, enrollment_id TEXT, full_report INTEGER, raw BLOB, received_at TEXT)",
			"INSERT INTO ddm_status_reports VALUES (1, 'DEVICE-01', 1, NULL, 'bad')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{"StatusReports": func() error { _, err := s.StatusReports(ctx, dev, paging.Page{}); return err }}
		}},
		{"changes", []string{
			"DROP TABLE ddm_changes",
			"CREATE TABLE ddm_changes (seq INTEGER PRIMARY KEY, enrollment_id TEXT, channel INTEGER, parent_id TEXT, reason TEXT, created_at TEXT, attempts INTEGER, last_error TEXT, next_attempt_at TEXT)",
			"INSERT INTO ddm_changes VALUES (1, 'DEVICE-01', 1, '', 'r', 'bad', 0, '', '0000')",
		}, func(s *sqlstore.Store) map[string]func() error {
			return map[string]func() error{"PendingChanges": func() error { _, err := s.PendingChanges(ctx, t0, 0); return err }}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := open(t)
			exec(t, s, c.setup...)
			for name, call := range c.calls(s) {
				hardError(t, name, call())
			}
		})
	}
}

// TestMySQLDialectPaths renders the MySQL-only statement shapes (INSERT
// ... AS new ON DUPLICATE KEY UPDATE, LAST_INSERT_ID instead of RETURNING)
// against SQLite, which accepts the latter and rejects the former, so both
// branches run without a server; TestContractMySQL proves them on one.
func TestMySQLDialectPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := open(t)
	s, err := sqlstore.Open(ctx, base.DB(), mysql.Dialect, sqlstore.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.PutStatus(ctx, dev, ddm.StatusUpdate{ReceivedAt: t0, Errors: []ddm.StatusError{{StatusItem: "x"}}})
	if err != nil || out.Seq != 1 {
		t.Fatalf("report seq via LAST_INSERT_ID: %+v %v", out, err)
	}
	if err := s.PutSnapshot(ctx, &ddm.Snapshot{ID: dev, DeclarationsToken: "t", TokenChangedAt: t0, RefreshedAt: t0}); err == nil {
		t.Fatal("MySQL upsert accepted by SQLite")
	}
	if _, err := s.PutStatus(ctx, dev, ddm.StatusUpdate{Values: []ddm.StatusValue{{Path: "p", Value: []byte("1")}}}); err == nil {
		t.Fatal("MySQL upsert accepted by SQLite")
	}
}

// TestFullIdentityRoundTrip checks that every enrollment-keyed result
// carries channel and parent id, for a user channel as well as a device.
func TestFullIdentityRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := open(t)
	usr := ddmtest.User(1, "alice")
	seed(t, s)
	if _, err := s.AssignSet(ctx, usr, "s", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSnapshot(ctx, &ddm.Snapshot{ID: usr, DeclarationsToken: "t", TokenChangedAt: t0, RefreshedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutStatus(ctx, usr, ddm.StatusUpdate{ReceivedAt: t0, Declarations: []ddm.DeclarationStatus{{Kind: conf, Identifier: "a"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordChanges(ctx, []mdm.EnrollmentID{usr}, "r", t0); err != nil {
		t.Fatal(err)
	}
	r, err := s.SetEnrollments(ctx, "s", paging.Page{})
	if err != nil || len(r.Items) != 2 || r.Items[1] != usr {
		t.Fatalf("SetEnrollments: %+v %v", r, err)
	}
	affected, err := s.AffectedEnrollments(ctx, []string{"a"}, nil)
	if err != nil || len(affected) != 2 || affected[0] != dev || affected[1] != usr {
		t.Fatalf("AffectedEnrollments: %+v %v", affected, err)
	}
	snap, err := s.Snapshot(ctx, usr)
	if err != nil || snap.ID != usr {
		t.Fatalf("Snapshot: %+v %v", snap, err)
	}
	byID, err := s.DeclarationStatusByIdentifier(ctx, "a", paging.Page{})
	if err != nil || len(byID.Items) != 2 || byID.Items[1].ID != usr {
		t.Fatalf("ByIdentifier: %+v %v", byID, err)
	}
	pending, err := s.PendingChanges(ctx, t0, 0)
	if err != nil || len(pending) != 2 || pending[1].ID != usr {
		t.Fatalf("PendingChanges: %+v %v", pending, err)
	}
}
