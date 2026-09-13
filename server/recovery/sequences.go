package recovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// These generated IDs are externally visible cursors. Preserve the high-water
// mark even when retention removed the last row, so restoration cannot reuse it.
var generatedColumns = map[string]string{
	"commands": "seq", "audit_records": "id", "ddm_changes": "seq",
	"ddm_status_reports": "seq", "ddm_status_errors": "seq",
}

func (s SQL) snapshotSequences(
	ctx context.Context,
	q sqlcommon.Queryer,
	tables []tableSnapshot,
) ([]sequenceSnapshot, error) {
	out := []sequenceSnapshot{}
	for _, table := range tables {
		column, ok := generatedColumns[table.Name]
		if !ok {
			continue
		}
		var value int64
		switch s.Dialect.Name {
		case "sqlite":
			err := q.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name = ?", table.Name).
				Scan(&value)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, wrap(err)
			}
		case "mysql":
			var next sql.NullInt64
			if err := q.QueryRowContext(ctx, "SELECT auto_increment FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table.Name).
				Scan(&next); err != nil {
				return nil, wrap(err)
			}
			if next.Valid && next.Int64 > 0 {
				value = next.Int64 - 1
			}
		case "postgres":
			name, err := s.sequenceName(ctx, q, table.Name, column)
			if err != nil {
				return nil, err
			}
			var called bool
			if err := q.QueryRowContext(ctx, "SELECT last_value, is_called FROM "+name).
				Scan(&value, &called); err != nil {
				return nil, wrap(err)
			} // #nosec G202 -- each component of the catalog identifier is validated and quoted.
			if !called {
				value--
			}
		}
		out = append(out, sequenceSnapshot{Table: table.Name, Column: column, Value: value})
	}
	return out, nil
}

func (s SQL) sequenceName(
	ctx context.Context,
	q sqlcommon.Queryer,
	table, column string,
) (string, error) {
	var sequence string
	if err := q.QueryRowContext(ctx, s.Dialect.Rebind("SELECT pg_get_serial_sequence(?, ?)"), table, column).
		Scan(&sequence); err != nil {
		return "", wrap(err)
	}
	parts := strings.Split(sequence, ".")
	if len(parts) != 2 {
		return "", ErrInvalid
	}
	for i, part := range parts {
		var err error
		parts[i], err = quoted(s.Dialect, strings.Trim(part, `"`))
		if err != nil {
			return "", err
		}
	}
	return strings.Join(parts, "."), nil
}

func validateSequences(info databaseSnapshot) error {
	expected := map[string]string{}
	for _, table := range info.Tables {
		if column, ok := generatedColumns[table.Name]; ok {
			expected[table.Name] = column
		}
	}
	for _, sequence := range info.Sequences {
		column, ok := expected[sequence.Table]
		if !ok || sequence.Column != column || sequence.Value < 0 || sequence.Value == 1<<63-1 {
			return ErrInvalid
		}
		delete(expected, sequence.Table)
	}
	if len(expected) != 0 {
		return ErrInvalid
	}
	return nil
}

func (s SQL) restoreSequences(
	ctx context.Context,
	q sqlcommon.Queryer,
	sequences []sequenceSnapshot,
) error {
	for _, sequence := range sequences {
		switch s.Dialect.Name {
		case "sqlite":
			if _, err := q.ExecContext(
				ctx,
				"DELETE FROM sqlite_sequence WHERE name = ?",
				sequence.Table,
			); err != nil {
				return wrap(err)
			}
			if _, err := q.ExecContext(
				ctx,
				"INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)",
				sequence.Table,
				sequence.Value,
			); err != nil {
				return wrap(err)
			}
		case "postgres":
			value, called := sequence.Value, true
			if value == 0 {
				value, called = 1, false
			}
			if _, err := q.ExecContext(
				ctx,
				s.Dialect.Rebind("SELECT setval(pg_get_serial_sequence(?, ?), ?, ?)"),
				sequence.Table,
				sequence.Column,
				value,
				called,
			); err != nil {
				return wrap(err)
			}
		case "mysql":
			name, err := quoted(s.Dialect, sequence.Table)
			if err != nil {
				return err
			}
			query := fmt.Sprintf("ALTER TABLE %s AUTO_INCREMENT = %d", name, sequence.Value+1)
			if _, err := q.ExecContext(ctx, query); err != nil {
				return wrap(err)
			} // #nosec G202 -- compiled table identifier and validated integer; called after row commit because MySQL DDL commits implicitly.
		}
	}
	return nil
}
