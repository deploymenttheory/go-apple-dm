package sqlstore

import (
	"context"
	"crypto/sha256"
)

// LockPublication acquires a fixed shard before any publication reads. It also
// covers absent sets and works with SQLite's initial writer-lock acquisition.
func (t *txStore) LockPublication(ctx context.Context, name string) error {
	if err := validName("set name", name); err != nil {
		return err
	}
	h := sha256.Sum256([]byte(name))
	_, err := t.exec(ctx, "lock publication", "UPDATE ddm_publication_locks SET shard = shard WHERE shard = ?", int(h[0]))
	return err
}
