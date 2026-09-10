package sqlstore

import (
	"context"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

func (s *Store) seal(p string, b []byte, keys ...string) ([]byte, error) {
	return sqlcommon.SealBlob(s.keyring, p, b, keys...)
}

func (s *Store) open(p string, b []byte, keys ...string) ([]byte, error) {
	return sqlcommon.OpenBlob(s.keyring, p, b, keys...)
}

// Rewrap rotates retained DDM values using bounded pages and guarded writes.
func (s *Store) Rewrap(ctx context.Context) (int, error) {
	return sqlcommon.RewrapBlobs(ctx, s.db, s.d, s.keyring, []sqlcommon.BlobColumn{
		{
			Table:  "ddm_declarations",
			Column: "canonical",
			Keys:   []string{"identifier", "server_token"},
		},
		{
			Table:  "ddm_declaration_versions",
			Column: "canonical",
			Keys:   []string{"identifier", "server_token"},
		},
		{
			Table:  "ddm_snapshot_items",
			Column: "expanded",
			Keys:   []string{"enrollment_id", "kind", "identifier"},
		},
	})
}
