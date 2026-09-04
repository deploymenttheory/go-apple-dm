package sqlcommon_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func TestRebindAndUpserts(t *testing.T) {
	t.Parallel()
	q := "SELECT ? WHERE a = ? AND b IN (?, ?)"
	if got := (sqlcommon.Dialect{Dollar: true}).Rebind(q); got != "SELECT $1 WHERE a = $2 AND b IN ($3, $4)" {
		t.Fatal(got)
	}
	if got := (sqlcommon.Dialect{}).Rebind(q); got != q {
		t.Fatal(got)
	}
	// A "?" inside a string literal is not a placeholder.
	if got := (sqlcommon.Dialect{Dollar: true}).Rebind("SELECT '?' WHERE a = ? AND b = 'x?y' AND c = ?"); got != "SELECT '?' WHERE a = $1 AND b = 'x?y' AND c = $2" {
		t.Fatal(got)
	}
	cols, key := []string{"id", "a", "b"}, []string{"id"}
	if got := sqlcommon.UpsertOnConflict("t", cols, key); got != "INSERT INTO t (id, a, b) VALUES (?, ?, ?) ON CONFLICT (id) DO UPDATE SET a = excluded.a, b = excluded.b" {
		t.Fatal(got)
	}
	if got := sqlcommon.UpsertOnDuplicateKey("t", cols, key); got != "INSERT INTO t (id, a, b) VALUES (?, ?, ?) AS new ON DUPLICATE KEY UPDATE id = new.id, a = new.a, b = new.b" {
		t.Fatal(got)
	}
}

func TestMustSub(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"migrations/0001_a.sql": {Data: []byte("-- +up\nSELECT 1;")}}
	if ms, err := sqlcommon.LoadMigrations(sqlcommon.MustSub(fsys, "migrations")); err != nil || len(ms) != 1 {
		t.Fatalf("%v %v", ms, err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustSub with an invalid directory did not panic")
		}
	}()
	sqlcommon.MustSub(fsys, "../escape")
}

func TestPoolApply(t *testing.T) {
	t.Parallel()
	db := openRaw(t)
	(sqlcommon.Pool{}).Apply(db)
	if db.Stats().MaxOpenConnections != 0 {
		t.Fatal("zero pool changed the limit")
	}
	(sqlcommon.Pool{MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Second}).Apply(db)
	if db.Stats().MaxOpenConnections != 4 {
		t.Fatalf("MaxOpenConnections = %d", db.Stats().MaxOpenConnections)
	}
}

// TestDialectMigrationsAgree pins the three embedded migration directories
// to the same version and name sequence with a down section each, so a
// schema change cannot land in one engine only.
func TestDialectMigrationsAgree(t *testing.T) {
	t.Parallel()
	ref, err := sqlcommon.LoadMigrations(sqlite.Dialect.Migrations)
	if err != nil || len(ref) == 0 {
		t.Fatalf("sqlite migrations: %v %v", ref, err)
	}
	for _, d := range []sqlcommon.Dialect{postgres.Dialect, mysql.Dialect} {
		ms, err := sqlcommon.LoadMigrations(d.Migrations)
		if err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
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
		}
	}
}

func TestMigrationSetTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRaw(t)
	d := sqlcommon.Dialect{Name: "sqlite"}
	core := sqlcommon.MigrationSet{Table: "schema_migrations", FS: fstest.MapFS{
		"0001_a.sql": {Data: []byte("-- +up\nCREATE TABLE a (x INT);\n-- +down\nDROP TABLE a;")},
	}}
	ddm := sqlcommon.MigrationSet{Table: "ddm_schema_migrations", FS: fstest.MapFS{
		"0001_d.sql": {Data: []byte("-- +up\nCREATE TABLE d1 (x INT);\n-- +down\nDROP TABLE d1;")},
		"0002_e.sql": {Data: []byte("-- +up\nCREATE TABLE d2 (x INT);\n-- +down\nDROP TABLE d2;")},
	}}
	if _, err := sqlcommon.MigrateSet(ctx, db, d, core); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlcommon.MigrateSet(ctx, db, d, ddm); err != nil {
		t.Fatal(err)
	}
	// Each set has its own version sequence.
	if v, _ := sqlcommon.VersionOf(ctx, db, d, core.Table); v != 1 {
		t.Fatalf("core version %d", v)
	}
	if v, _ := sqlcommon.VersionOf(ctx, db, d, ddm.Table); v != 2 {
		t.Fatalf("ddm version %d", v)
	}
	if reverted, err := sqlcommon.RollbackSet(ctx, db, d, ddm, 0); err != nil || len(reverted) != 2 {
		t.Fatalf("rollback ddm: %v %v", reverted, err)
	}
	if v, _ := sqlcommon.VersionOf(ctx, db, d, core.Table); v != 1 {
		t.Fatal("rolling back one set touched the other")
	}
	// Table names are validated before they reach SQL.
	for _, bad := range []string{"", "Schema", "x;drop", "1abc", "a-b"} {
		set := sqlcommon.MigrationSet{Table: bad, FS: core.FS}
		if _, err := sqlcommon.MigrateSet(ctx, db, d, set); !errors.Is(err, sqlcommon.ErrMigration) {
			t.Errorf("MigrateSet %q: %v", bad, err)
		}
		if _, err := sqlcommon.RollbackSet(ctx, db, d, set, 0); !errors.Is(err, sqlcommon.ErrMigration) {
			t.Errorf("RollbackSet %q: %v", bad, err)
		}
		if _, err := sqlcommon.VersionOf(ctx, db, d, bad); !errors.Is(err, sqlcommon.ErrMigration) {
			t.Errorf("VersionOf %q: %v", bad, err)
		}
	}
}

// brokenFS fails every Open, so directory and file reads both error.
type brokenFS struct{}

func (brokenFS) Open(string) (fs.File, error) { return nil, errors.New("disk on fire") }

// unreadableFS lists one file whose Open fails. It deliberately does not
// embed MapFS so fs.ReadFile cannot bypass Open.
type unreadableFS struct{ m fstest.MapFS }

func (u unreadableFS) Open(name string) (fs.File, error) {
	if name == "0001_a.sql" {
		return nil, errors.New("unreadable")
	}
	return u.m.Open(name)
}

func TestMigrationLoadErrors(t *testing.T) {
	t.Parallel()
	if _, err := sqlcommon.LoadMigrations(brokenFS{}); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("broken fs: %v", err)
	}
	u := unreadableFS{m: fstest.MapFS{"0001_a.sql": {Data: []byte("-- +up\nSELECT 1;")}}}
	if _, err := sqlcommon.LoadMigrations(u); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("unreadable file: %v", err)
	}
}

func TestMigrationParsing(t *testing.T) {
	t.Parallel()
	bad := map[string]fstest.MapFS{
		"no underscore": {"0001.sql": {Data: []byte("-- +up\nSELECT 1;")}},
		"bad version":   {"abcd_x.sql": {Data: []byte("-- +up\nSELECT 1;")}},
		"zero version":  {"0000_x.sql": {Data: []byte("-- +up\nSELECT 1;")}},
		"no up":         {"0001_x.sql": {Data: []byte("-- +down\nSELECT 1;")}},
		"duplicate":     {"0001_x.sql": {Data: []byte("-- +up\nSELECT 1;")}, "0001_y.sql": {Data: []byte("-- +up\nSELECT 1;")}},
	}
	for name, fsys := range bad {
		if _, err := sqlcommon.LoadMigrations(fsys); !errors.Is(err, sqlcommon.ErrMigration) {
			t.Errorf("%s: %v", name, err)
		}
	}
	good := fstest.MapFS{
		"0002_second.sql": {Data: []byte("-- +up\nCREATE TABLE b (x INT);\n-- +down\nDROP TABLE b;\n")},
		"0001_first.sql":  {Data: []byte("-- comment before sections\n-- +up\n\nCREATE TABLE a (\n  x INT\n);\n-- inline comment\nINSERT INTO a VALUES (1)\n-- +down\nDROP TABLE a;")},
		"README.md":       {Data: []byte("ignored")},
		"dir":             {Mode: 0o755 | 1<<31},
	}
	ms, err := sqlcommon.LoadMigrations(good)
	if err != nil || len(ms) != 2 || ms[0].Version != 1 || ms[0].Name != "first" || ms[1].Name != "second" {
		t.Fatalf("%+v %v", ms, err)
	}
	if len(ms[0].Up) != 2 || ms[0].Up[1] != "INSERT INTO a VALUES (1)" || len(ms[0].Down) != 1 || ms[0].Down[0] != "DROP TABLE a" {
		t.Fatalf("%+v", ms[0])
	}
	if _, err := sqlcommon.LoadMigrations(fstest.MapFS{}); err != nil {
		t.Fatal(err)
	}
}

func openRaw(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "m.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateAndRollback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openRaw(t)
	d := sqlcommon.Dialect{Name: "sqlite", Migrations: fstest.MapFS{
		"0001_a.sql": {Data: []byte("-- +up\nCREATE TABLE a (x INT);\n-- +down\nDROP TABLE a;")},
		"0002_b.sql": {Data: []byte("-- +up\nCREATE TABLE b (x INT);\n-- +down\nDROP TABLE b;")},
	}}
	applied, err := sqlcommon.Migrate(ctx, db, d)
	if err != nil || len(applied) != 2 {
		t.Fatalf("%v %v", applied, err)
	}
	if v, _ := sqlcommon.Version(ctx, db, d); v != 2 {
		t.Fatalf("version %d", v)
	}
	reverted, err := sqlcommon.Rollback(ctx, db, d, 1)
	if err != nil || len(reverted) != 1 || reverted[0] != 2 {
		t.Fatalf("%v %v", reverted, err)
	}
	if v, _ := sqlcommon.Version(ctx, db, d); v != 1 {
		t.Fatalf("version %d", v)
	}
	// A third migration whose up fails leaves the version untouched.
	d.Migrations = fstest.MapFS{
		"0001_a.sql":   {Data: []byte("-- +up\nCREATE TABLE a (x INT);\n-- +down\nDROP TABLE a;")},
		"0003_bad.sql": {Data: []byte("-- +up\nCREATE TABLE (;\n-- +down\nDROP TABLE nope;")},
	}
	if _, err := sqlcommon.Migrate(ctx, db, d); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("bad up: %v", err)
	}
	if v, _ := sqlcommon.Version(ctx, db, d); v != 1 {
		t.Fatalf("version after failed migrate %d", v)
	}
	// A failing down stops the rollback.
	d.Migrations = fstest.MapFS{"0001_a.sql": {Data: []byte("-- +up\nCREATE TABLE a (x INT);\n-- +down\nDROP TABLE nope;")}}
	if _, err := sqlcommon.Rollback(ctx, db, d, 0); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatalf("bad down: %v", err)
	}
	// Broken migration sets surface from every entry point.
	broken := sqlcommon.Dialect{Name: "sqlite", Migrations: fstest.MapFS{"x.sql": {Data: []byte("-- +up\nSELECT 1;")}}}
	if _, err := sqlcommon.Migrate(ctx, db, broken); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatal("migrate with broken set")
	}
	if _, err := sqlcommon.Rollback(ctx, db, broken, 0); !errors.Is(err, sqlcommon.ErrMigration) {
		t.Fatal("rollback with broken set")
	}
	// Closed database.
	closed := openRaw(t)
	_ = closed.Close()
	if _, err := sqlcommon.Migrate(ctx, closed, d); err == nil {
		t.Fatal("migrate on closed db")
	}
	if _, err := sqlcommon.Rollback(ctx, closed, d, 0); err == nil {
		t.Fatal("rollback on closed db")
	}
	if _, err := sqlcommon.Version(ctx, closed, d); err == nil {
		t.Fatal("version on closed db")
	}
	s := sqlcommon.New(closed, d)
	if err := s.Ping(ctx); err == nil {
		t.Fatal("ping closed")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
