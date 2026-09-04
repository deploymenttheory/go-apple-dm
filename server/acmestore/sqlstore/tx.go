package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/v3/paging"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme"
)

// querier is *sql.DB or *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// txStore is the view every method runs against: the pool for a read
// outside Update, or the transaction inside it.
type txStore struct {
	s *Store
	q querier
}

var _ acme.Tx = (*txStore)(nil)

// Update implements acme.Store. fn runs in one transaction that commits
// when fn returns nil and rolls back otherwise, so an order, its
// authorization, its challenge, and the claim on its identifier either all
// exist or none do.
func (s *Store) Update(ctx context.Context, fn func(acme.Tx) error) error {
	if fn == nil {
		return fmt.Errorf("%w: nil Update callback", acme.ErrInvalid)
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

// view is the pool-backed view for reads outside a transaction.
func (s *Store) view() *txStore { return &txStore{s: s, q: s.db} }

// raced reports whether err is the engine's unique-key violation.
func (s *Store) raced(err error) bool {
	return err != nil && s.d.IsUniqueViolation != nil && s.d.IsUniqueViolation(err)
}

// exec runs one statement. A unique violation is reported as
// acme.ErrConflict: the unique index on an account thumbprint and the
// primary key of a claim are what decide a race, and the server answers
// the loser with a problem document.
func (t *txStore) exec(ctx context.Context, op, query string, args ...any) (sql.Result, error) {
	res, err := t.q.ExecContext(ctx, t.s.d.Rebind(query), args...)
	if err != nil {
		if t.s.raced(err) {
			return nil, fmt.Errorf("%w: sqlstore: %s: %w", acme.ErrConflict, op, err)
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

// each runs query and calls scan for every row.
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

// keyset runs a query ordered by its cursor key, asking for one row more
// than the page so the cursor is set only when more rows exist.
func keyset[T any](ctx context.Context, t *txStore, op, query string, args []any, p paging.Page, scan func(*sql.Rows) (T, string, error)) (paging.Result[T], error) {
	limit := pageLimit(p)
	var out paging.Result[T]
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
		return paging.Result[T]{}, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = keys[limit-1]
	}
	return out, nil
}

// after appends the keyset condition for a string cursor.
func after(where []string, args []any, key string, p paging.Page) ([]string, []any) {
	if p.Cursor == "" {
		return where, args
	}
	return append(where, key+" > ?"), append(args, p.Cursor)
}

// get reads one record column by primary key and decodes it into v.
func (t *txStore) get(ctx context.Context, op, table, what, id string, v any) error {
	if err := validID(what+" id", id); err != nil {
		return err
	}
	var raw []byte
	found, err := t.row(ctx, op, "SELECT record FROM "+table+" WHERE id = ?", []any{id}, &raw) // #nosec G202 -- table names are literals
	if err != nil {
		return err
	}
	if !found {
		return notFound(what, id)
	}
	return decode(table, id, raw, v)
}

// put writes a whole record by its key: cols[0] names the key column and
// args[0] carries its value.
//
// It reads the key and then UPDATEs or INSERTs rather than using the
// dialect's upsert because acme_accounts carries a second unique key, the
// thumbprint. MySQL's ON DUPLICATE KEY UPDATE fires on whichever unique
// key the new row collides with, so an account inserted with another
// account's thumbprint would quietly overwrite that account instead of
// failing, and the thumbprint is the account's identity. Naming the key in
// a WHERE clause collides only on the key it names and leaves the
// thumbprint index free to reject the duplicate, which is the conflict the
// server expects.
//
// The read is an ordinary one, not a locking one: it only chooses between
// an insert and an update, and the unique index still decides the race, so
// a put that chose INSERT and lost reports ErrConflict rather than
// overwriting. A SELECT ... FOR UPDATE here would be worse than useless,
// because InnoDB answers a locking read of a row that does not exist with
// a gap lock and concurrent inserts into one table then deadlock each
// other.
func (t *txStore) put(ctx context.Context, op, table string, cols []string, args []any) error {
	key := cols[0]
	var found string
	exists, err := t.row(ctx, op, "SELECT "+key+" FROM "+table+" WHERE "+key+" = ?", []any{args[0]}, &found) // #nosec G202 -- table and column names are literals
	if err != nil {
		return err
	}
	if !exists {
		insert := "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (" + placeholders(len(cols)) + ")"
		_, err = t.exec(ctx, op, insert, args...) // #nosec G202 -- table and column names are literals
		return err
	}
	set := make([]string, 0, len(cols)-1)
	for _, c := range cols[1:] {
		set = append(set, c+" = ?")
	}
	update := make([]any, 0, len(args))
	update = append(update, args[1:]...)
	update = append(update, args[0])
	_, err = t.exec(ctx, op, "UPDATE "+table+" SET "+strings.Join(set, ", ")+" WHERE "+key+" = ?", update...) // #nosec G202 -- table and column names are literals
	return err
}
