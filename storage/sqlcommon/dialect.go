// Package sqlcommon implements storage.Store over database/sql once, for
// every SQL backend. A backend supplies a Dialect (placeholder style, row
// locking, upsert syntax, and its migration files) and an opened *sql.DB
// (decision record 0012).
package sqlcommon

import (
	"io/fs"
	"strconv"
	"strings"
)

// Dialect describes what differs between SQL engines.
type Dialect struct {
	// Name is used in errors and in the migration table.
	Name string
	// Dollar selects $1-style placeholders (PostgreSQL); otherwise "?".
	Dollar bool
	// ForUpdate is appended to row-locking SELECTs inside transactions
	// ("FOR UPDATE"), or empty when the engine serialises writers (SQLite).
	ForUpdate string
	// Upsert renders an INSERT that updates the non-key columns on a key
	// conflict. See UpsertOnConflict and UpsertOnDuplicateKey.
	Upsert func(table string, cols, key []string) string
	// Migrations holds NNNN_name.sql files with "-- +up" and "-- +down"
	// sections.
	Migrations fs.FS
	// InsertIgnore renders an INSERT that does nothing when the key already
	// exists. See InsertIgnoreOnConflict and InsertIgnoreDuplicateKey.
	InsertIgnore func(table string, cols, key []string) string
	// IsUniqueViolation reports whether err is the engine's unique-constraint
	// error, so the store returns storage.ErrConflict instead of a driver
	// error when two writers race for the same key.
	IsUniqueViolation func(error) bool
}

func (d Dialect) uniqueViolation(err error) bool {
	return err != nil && d.IsUniqueViolation != nil && d.IsUniqueViolation(err)
}

// Rebind converts "?" placeholders to the dialect's form. A "?" inside a
// single-quoted literal is left alone.
func (d Dialect) Rebind(query string) string {
	if !d.Dollar {
		return query
	}
	var sb strings.Builder
	sb.Grow(len(query) + 8)
	n := 0
	quoted := false
	for _, r := range query {
		switch {
		case r == '\'':
			quoted = !quoted
		case r == '?' && !quoted:
			n++
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(n))
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// UpsertOnConflict renders INSERT ... ON CONFLICT (key) DO UPDATE SET
// col = excluded.col (PostgreSQL and SQLite).
func UpsertOnConflict(table string, cols, key []string) string {
	var sb strings.Builder
	sb.WriteString(insertPrefix(table, cols))
	sb.WriteString(" ON CONFLICT (")
	sb.WriteString(strings.Join(key, ", "))
	sb.WriteString(") DO UPDATE SET ")
	sb.WriteString(assignments(cols, key, "excluded."))
	return sb.String()
}

// UpsertOnDuplicateKey renders INSERT ... AS new ON DUPLICATE KEY UPDATE
// col = new.col (MySQL 8.0.19 and later).
func UpsertOnDuplicateKey(table string, cols, _ []string) string {
	var sb strings.Builder
	sb.WriteString(insertPrefix(table, cols))
	sb.WriteString(" AS new ON DUPLICATE KEY UPDATE ")
	sb.WriteString(assignments(cols, nil, "new."))
	return sb.String()
}

// InsertIgnoreOnConflict renders INSERT ... ON CONFLICT (key) DO NOTHING
// (PostgreSQL and SQLite).
func InsertIgnoreOnConflict(table string, cols, key []string) string {
	return insertPrefix(table, cols) + " ON CONFLICT (" + strings.Join(key, ", ") + ") DO NOTHING"
}

// InsertIgnoreDuplicateKey renders INSERT ... AS new ON DUPLICATE KEY
// UPDATE k = new.k, a no-op update on conflict (MySQL 8.0.19 and later).
func InsertIgnoreDuplicateKey(table string, cols, key []string) string {
	return insertPrefix(table, cols) + " AS new ON DUPLICATE KEY UPDATE " + key[0] + " = new." + key[0]
}

func insertPrefix(table string, cols []string) string {
	return "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (" +
		strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ") + ")"
}

func assignments(cols, key []string, src string) string {
	skip := map[string]bool{}
	for _, k := range key {
		skip[k] = true
	}
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		if skip[c] {
			continue
		}
		parts = append(parts, c+" = "+src+c)
	}
	return strings.Join(parts, ", ")
}

// placeholders returns "?, ?, ?" for n values.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
