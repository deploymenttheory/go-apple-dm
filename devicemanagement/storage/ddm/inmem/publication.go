package inmem

import "context"

// LockPublication needs no additional lock: Update holds the store's mutex
// while operating on a private state copy.
func (t *tx) LockPublication(_ context.Context, name string) error {
	return validName("set name", name)
}
