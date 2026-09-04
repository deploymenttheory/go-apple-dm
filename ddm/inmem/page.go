package inmem

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/paging"
)

func limitOf(p paging.Page) int {
	if p.Limit <= 0 {
		return paging.DefaultPageSize
	}
	return p.Limit
}

// pageByKey pages ascending over string keys. The cursor is the last key of
// the previous page; keys at or before it are skipped.
func pageByKey[T any](keys []string, p paging.Page, item func(string) T) paging.Result[T] {
	slices.Sort(keys)
	limit := limitOf(p)
	var out paging.Result[T]
	var last string
	for _, k := range keys {
		if p.Cursor != "" && k <= p.Cursor {
			continue
		}
		if len(out.Items) == limit {
			out.NextCursor = last
			return out
		}
		out.Items = append(out.Items, item(k))
		last = k
	}
	return out
}

// pageBySeq pages newest first over sequence numbers. The cursor is the
// last seq of the previous page as a decimal string; a cursor that is not
// a decimal integer is ErrInvalid.
func pageBySeq[T any](seqs []int64, p paging.Page, item func(int64) T) (paging.Result[T], error) {
	var after int64
	if p.Cursor != "" {
		n, err := strconv.ParseInt(p.Cursor, 10, 64)
		if err != nil {
			return paging.Result[T]{}, fmt.Errorf("%w: cursor %q", ddm.ErrInvalid, p.Cursor)
		}
		after = n
	}
	slices.SortFunc(seqs, func(a, b int64) int { return cmp.Compare(b, a) })
	limit := limitOf(p)
	var out paging.Result[T]
	var last int64
	for _, seq := range seqs {
		if p.Cursor != "" && seq >= after {
			continue
		}
		if len(out.Items) == limit {
			out.NextCursor = strconv.FormatInt(last, 10)
			return out, nil
		}
		out.Items = append(out.Items, item(seq))
		last = seq
	}
	return out, nil
}
