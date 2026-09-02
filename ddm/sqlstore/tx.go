package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/deploymenttheory/go-apple-mdm/ddm"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// querier is *sql.DB or *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// txStore is the view every method runs against: the pool for a call
// outside Update, or the transaction inside it. It also satisfies
// ddm.Store so a callback can discover that nesting Update is invalid.
type txStore struct {
	s *Store
	q querier
}

var _ ddm.Store = (*txStore)(nil)

// Update implements ddm.Store on the transaction view: nested transactions
// are not supported.
func (t *txStore) Update(context.Context, func(ddm.Tx) error) error {
	return fmt.Errorf("%w: nested Update", ddm.ErrInvalid)
}

// Update implements ddm.Store. fn runs in one transaction that commits
// when fn returns nil and rolls back otherwise. fn must use the Tx it is
// given; the Store's own methods would run outside the transaction.
func (s *Store) Update(ctx context.Context, fn func(ddm.Tx) error) error {
	if fn == nil {
		return fmt.Errorf("%w: nil Update callback", ddm.ErrInvalid)
	}
	return s.runInTx(ctx, func(t *txStore) error { return fn(t) })
}

func (s *Store) runInTx(ctx context.Context, fn func(*txStore) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wrap("begin", err)
	}
	if err := fn(&txStore{s: s, q: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return wrap("commit", err)
	}
	return nil
}

// writeAttempts bounds the retries of a self-contained write that lost a
// unique-key race: once the winner has committed, the retry sees its row.
const writeAttempts = 3

// write runs fn in its own transaction for a method called outside
// Update. Two writers racing to create the same row (declaration, set,
// membership, assignment) produce a unique violation on one side; that
// side retries and reports the row as already present.
func (s *Store) write(ctx context.Context, fn func(*txStore) error) error {
	var err error
	for range writeAttempts {
		if err = s.runInTx(ctx, fn); !s.raced(err) {
			return err
		}
	}
	return err
}

// raced reports whether err is the engine's unique-key violation.
func (s *Store) raced(err error) bool {
	return err != nil && s.d.IsUniqueViolation != nil && s.d.IsUniqueViolation(err)
}

// view is the pool-backed view for reads and single-statement writes.
func (s *Store) view() *txStore { return &txStore{s: s, q: s.db} }

// exec runs one statement. A unique violation is reported as
// ddm.ErrConflict, still wrapping the driver error so write can retry.
func (t *txStore) exec(ctx context.Context, op, query string, args ...any) (sql.Result, error) {
	res, err := t.q.ExecContext(ctx, t.s.d.Rebind(query), args...)
	if err != nil {
		if t.s.raced(err) {
			return nil, fmt.Errorf("%w: sqlstore: %s: %w", ddm.ErrConflict, op, err)
		}
		return nil, wrap(op, err)
	}
	return res, nil
}

// affected returns the rows a statement touched.
func affected(op string, res sql.Result) (int64, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, wrap(op, err)
	}
	return n, nil
}

// row scans one row into dest; found is false when there is none.
func (t *txStore) row(ctx context.Context, op, query string, args []any, dest ...any) (bool, error) {
	err := t.q.QueryRowContext(ctx, t.s.d.Rebind(query), args...).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, wrap(op, err)
	}
	return true, nil
}

// exists reports whether table has a row whose column equals value.
func (t *txStore) exists(ctx context.Context, table, column, value string) (bool, error) {
	var one int
	return t.row(ctx, "lookup "+table, "SELECT 1 FROM "+table+" WHERE "+column+" = ?", []any{value}, &one) // #nosec G202 -- table and column are literals at every call site
}

// each runs query and calls scan for every row. scan wraps its own
// errors.
func (t *txStore) each(ctx context.Context, op, query string, args []any, scan func(*sql.Rows) error) error {
	rows, err := t.q.QueryContext(ctx, t.s.d.Rebind(query), args...)
	if err != nil {
		return wrap(op, err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return wrap(op, err)
	}
	return nil
}

// column returns the single string column of every row.
func (t *txStore) column(ctx context.Context, op, query string, args ...any) ([]string, error) {
	var out []string
	err := t.each(ctx, op, query, args, func(rows *sql.Rows) error {
		var v string
		if err := rows.Scan(&v); err != nil {
			return wrap(op, err)
		}
		out = append(out, v)
		return nil
	})
	return out, err
}

// keyset runs a query ordered by its cursor key, asking for one row more
// than the page so the cursor is set only when more rows exist. scan
// returns each item with the key that continues after it.
func keyset[T any](ctx context.Context, t *txStore, op, query string, args []any, p storage.Page, scan func(*sql.Rows) (T, string, error)) (storage.Result[T], error) {
	limit := pageLimit(p)
	var out storage.Result[T]
	keys := make([]string, 0, limit+1)
	err := t.each(ctx, op, query+" LIMIT ?", append(args, limit+1), func(rows *sql.Rows) error {
		item, key, err := scan(rows)
		if err != nil {
			return err
		}
		out.Items = append(out.Items, item)
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		return storage.Result[T]{}, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = keys[limit-1]
	}
	return out, nil
}

// seqCursor parses a newest-first cursor: the seq of the last row served,
// or 0 with ok false for the first page. Anything but a decimal integer
// is ddm.ErrInvalid.
func seqCursor(p storage.Page) (seq int64, ok bool, err error) {
	if p.Cursor == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(p.Cursor, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%w: cursor %q: %w", ddm.ErrInvalid, p.Cursor, err)
	}
	return n, true, nil
}

// insertSeq inserts one row of a table with an autoincrement seq and
// returns the seq: RETURNING where the engine has it, LAST_INSERT_ID on
// MySQL.
func (t *txStore) insertSeq(ctx context.Context, op, query string, args ...any) (int64, error) {
	if t.s.mysql {
		res, err := t.exec(ctx, op, query, args...)
		if err != nil {
			return 0, err
		}
		seq, err := res.LastInsertId()
		if err != nil {
			return 0, wrap(op, err)
		}
		return seq, nil
	}
	var seq int64
	if err := t.q.QueryRowContext(ctx, t.s.d.Rebind(query+" RETURNING seq"), args...).Scan(&seq); err != nil {
		return 0, wrap(op, err)
	}
	return seq, nil
}

// upsert renders an INSERT that, on a key conflict, updates every column
// outside key and keep. keep names columns written on insert only, such
// as first_seen.
func (t *txStore) upsert(table string, cols, key, keep []string) string {
	skip := map[string]bool{}
	for _, c := range append(append([]string{}, key...), keep...) {
		skip[c] = true
	}
	src := "excluded."
	if t.s.mysql {
		src = "new."
	}
	set := make([]string, 0, len(cols))
	for _, c := range cols {
		if !skip[c] {
			set = append(set, c+" = "+src+c)
		}
	}
	insert := "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (" + placeholders(len(cols)) + ")"
	if t.s.mysql {
		return insert + " AS new ON DUPLICATE KEY UPDATE " + strings.Join(set, ", ")
	}
	return insert + " ON CONFLICT (" + strings.Join(key, ", ") + ") DO UPDATE SET " + strings.Join(set, ", ")
}
