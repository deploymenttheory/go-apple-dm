package sqlstore

import (
	"context"
	"database/sql"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

func (s *Store) runInTx(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	return (sqlcommon.UnitOfWork{DB: s.db, Dialect: s.d}).Run(ctx, func(ctx context.Context) error {
		tx, _ := sqlcommon.CurrentTransaction(ctx, s.db)
		return fn(ctx, tx)
	})
}
