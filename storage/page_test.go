package storage_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/storage"
)

// TestPageSize bounds the page size every backend allocates from. The limit
// arrives from an admin query string and reaches a slice allocation before a
// single row is read, so an unbounded one is a request that kills the process.
func TestPageSize(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		limit int
		want  int
	}{
		"unset":        {0, storage.DefaultPageSize},
		"negative":     {-1, storage.DefaultPageSize},
		"one":          {1, 1},
		"at the max":   {storage.MaxPageSize, storage.MaxPageSize},
		"over":         {storage.MaxPageSize + 1, storage.MaxPageSize},
		"int overflow": {1<<31 - 1, storage.MaxPageSize},
	} {
		if got := (storage.Page{Limit: tc.limit}).Size(); got != tc.want {
			t.Errorf("%s: Size() = %d, want %d", name, got, tc.want)
		}
	}
}
