package paging_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/paging"
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
		"unset":        {0, paging.DefaultPageSize},
		"negative":     {-1, paging.DefaultPageSize},
		"one":          {1, 1},
		"at the max":   {paging.MaxPageSize, paging.MaxPageSize},
		"over":         {paging.MaxPageSize + 1, paging.MaxPageSize},
		"int overflow": {1<<31 - 1, paging.MaxPageSize},
	} {
		if got := (paging.Page{Limit: tc.limit}).Size(); got != tc.want {
			t.Errorf("%s: Size() = %d, want %d", name, got, tc.want)
		}
	}
}
