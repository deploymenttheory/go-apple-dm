package sqlcommon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrMigration is returned for malformed or failing migrations.
var ErrMigration = errors.New("sqlcommon: migration")

// DefaultMigrationsTable records the storage schema's applied versions.
const DefaultMigrationsTable = "schema_migrations"

// MigrationSet is one package's versioned migrations and the table that
// records them, so several packages (storage, ddm) can share a database
// without sharing a version sequence.
type MigrationSet struct {
	// Table is the version table, for example "schema_migrations" or
	// "ddm_schema_migrations". Lower-case identifiers only.
	Table string
	FS    fs.FS
}

var tableName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func (m MigrationSet) validate() error {
	if !tableName.MatchString(m.Table) {
		return fmt.Errorf("%w: bad migrations table %q", ErrMigration, m.Table)
	}
	return nil
}

func (d Dialect) set() MigrationSet {
	return MigrationSet{Table: DefaultMigrationsTable, FS: d.Migrations}
}

// Migration is one parsed migration file.
type Migration struct {
	Version int
	Name    string
	Up      []string
	Down    []string
}

// LoadMigrations parses every NNNN_name.sql file in fsys, sorted by version.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("%w: read dir: %w", ErrMigration, err)
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m, err := parseMigration(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[m.Version]; dup {
			return nil, fmt.Errorf("%w: version %d defined by %s and %s", ErrMigration, m.Version, prev, e.Name())
		}
		seen[m.Version] = e.Name()
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func parseMigration(fsys fs.FS, name string) (Migration, error) {
	base := strings.TrimSuffix(name, ".sql")
	verStr, title, ok := strings.Cut(base, "_")
	if !ok {
		return Migration{}, fmt.Errorf("%w: %s: want NNNN_name.sql", ErrMigration, name)
	}
	version, err := strconv.Atoi(verStr)
	if err != nil || version <= 0 {
		return Migration{}, fmt.Errorf("%w: %s: bad version %q", ErrMigration, name, verStr)
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Migration{}, fmt.Errorf("%w: %s: %w", ErrMigration, name, err)
	}
	m := Migration{Version: version, Name: title}
	section := ""
	var current []string
	flush := func() {
		stmts := splitStatements(strings.Join(current, "\n"))
		switch section {
		case "up":
			m.Up = append(m.Up, stmts...)
		case "down":
			m.Down = append(m.Down, stmts...)
		}
		current = current[:0]
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case "-- +up":
			flush()
			section = "up"
		case "-- +down":
			flush()
			section = "down"
		default:
			if section != "" {
				current = append(current, line)
			}
		}
	}
	flush()
	if len(m.Up) == 0 {
		return Migration{}, fmt.Errorf("%w: %s: no statements under -- +up", ErrMigration, name)
	}
	return m, nil
}

// splitStatements splits on semicolons at line ends, dropping comment-only
// lines, so each statement runs on its own (MySQL needs that without
// multiStatements).
func splitStatements(sqlText string) []string {
	var out []string
	var sb strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
		if strings.HasSuffix(trimmed, ";") {
			out = append(out, strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sb.String()), ";")))
			sb.Reset()
		}
	}
	if rest := strings.TrimSpace(sb.String()); rest != "" {
		out = append(out, rest)
	}
	return out
}

// Migrate applies every pending migration of the dialect's own set in
// order, each in its own transaction, and returns the versions applied.
func Migrate(ctx context.Context, db *sql.DB, d Dialect) ([]int, error) {
	return MigrateSet(ctx, db, d, d.set())
}

// MigrateSet is Migrate for an arbitrary migration set and version table.
func MigrateSet(ctx context.Context, db *sql.DB, d Dialect, set MigrationSet) ([]int, error) {
	if err := set.validate(); err != nil {
		return nil, err
	}
	ms, err := LoadMigrations(set.FS)
	if err != nil {
		return nil, err
	}
	if err := ensureMigrationsTable(ctx, db, d, set.Table); err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, db, set.Table)
	if err != nil {
		return nil, err
	}
	var done []int
	for _, m := range ms {
		if applied[m.Version] {
			continue
		}
		if err := runInTx(ctx, db, func(tx *sql.Tx) error {
			for _, stmt := range m.Up {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("%w: %s: %w", ErrMigration, m.Name, err)
				}
			}
			_, err := tx.ExecContext(ctx, d.Rebind("INSERT INTO "+set.Table+" (version, name, applied_at) VALUES (?, ?, ?)"), m.Version, m.Name, time.Now().UTC()) // #nosec G202 -- table name validated by tableName
			if err != nil {
				return fmt.Errorf("%w: record %d: %w", ErrMigration, m.Version, err)
			}
			return nil
		}); err != nil {
			return done, err
		}
		done = append(done, m.Version)
	}
	return done, nil
}

// Rollback reverts applied migrations of the dialect's own set newer than
// target (0 reverts all), newest first, each in its own transaction.
func Rollback(ctx context.Context, db *sql.DB, d Dialect, target int) ([]int, error) {
	return RollbackSet(ctx, db, d, d.set(), target)
}

// RollbackSet is Rollback for an arbitrary migration set and version table.
func RollbackSet(ctx context.Context, db *sql.DB, d Dialect, set MigrationSet, target int) ([]int, error) {
	if err := set.validate(); err != nil {
		return nil, err
	}
	ms, err := LoadMigrations(set.FS)
	if err != nil {
		return nil, err
	}
	if err := ensureMigrationsTable(ctx, db, d, set.Table); err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, db, set.Table)
	if err != nil {
		return nil, err
	}
	var done []int
	for i := len(ms) - 1; i >= 0; i-- {
		m := ms[i]
		if m.Version <= target || !applied[m.Version] {
			continue
		}
		if err := runInTx(ctx, db, func(tx *sql.Tx) error {
			for _, stmt := range m.Down {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("%w: %s down: %w", ErrMigration, m.Name, err)
				}
			}
			if _, err := tx.ExecContext(ctx, d.Rebind("DELETE FROM "+set.Table+" WHERE version = ?"), m.Version); err != nil { // #nosec G202 -- table name validated by tableName
				return fmt.Errorf("%w: unrecord %d: %w", ErrMigration, m.Version, err)
			}
			return nil
		}); err != nil {
			return done, err
		}
		done = append(done, m.Version)
	}
	return done, nil
}

// Version returns the highest applied version of the default set (0 when
// none).
func Version(ctx context.Context, db *sql.DB, d Dialect) (int, error) {
	return VersionOf(ctx, db, d, DefaultMigrationsTable)
}

// VersionOf returns the highest applied version recorded in table.
func VersionOf(ctx context.Context, db *sql.DB, d Dialect, table string) (int, error) {
	if err := (MigrationSet{Table: table}).validate(); err != nil {
		return 0, err
	}
	if err := ensureMigrationsTable(ctx, db, d, table); err != nil {
		return 0, err
	}
	applied, err := appliedVersions(ctx, db, table)
	if err != nil {
		return 0, err
	}
	v := 0
	for k := range applied {
		v = max(v, k)
	}
	return v, nil
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB, d Dialect, table string) error {
	_, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+table+ // #nosec G202 -- table name validated by tableName
		" (version INTEGER NOT NULL PRIMARY KEY, name VARCHAR(255) NOT NULL, applied_at "+timestampType(d)+" NOT NULL)")
	if err != nil {
		return fmt.Errorf("%w: create %s: %w", ErrMigration, table, err)
	}
	return nil
}

func timestampType(d Dialect) string {
	switch d.Name {
	case "postgres":
		return "TIMESTAMPTZ"
	case "mysql":
		return "DATETIME(6)"
	default:
		return "TIMESTAMP"
	}
}

func appliedVersions(ctx context.Context, db *sql.DB, table string) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT version FROM "+table) // #nosec G202 -- table name validated by tableName
	if err != nil {
		return nil, fmt.Errorf("%w: read versions: %w", ErrMigration, err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("%w: scan version: %w", ErrMigration, err)
		}
		out[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: versions: %w", ErrMigration, err)
	}
	return out, nil
}

func runInTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlcommon: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlcommon: commit: %w", err)
	}
	return nil
}

// MustSub returns the sub-tree dir of fsys, panicking when it does not
// exist. Backends use it to expose their embedded migration directory as
// a package-level Dialect value.
func MustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(fmt.Sprintf("sqlcommon: migrations directory %q: %v", dir, err))
	}
	return sub
}
