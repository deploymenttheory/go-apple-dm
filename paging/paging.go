package paging

// Page requests one page of results. An empty Cursor starts from the
// beginning; Limit <= 0 uses the backend default.
type Page struct {
	Cursor string
	Limit  int
}

// Result is one page of items with the cursor for the next page ("" at
// the end).
type Result[T any] struct {
	Items      []T
	NextCursor string
}

// DefaultPageSize applies when Page.Limit is not positive.
const DefaultPageSize = 100

// MaxPageSize bounds Page.Limit. A limit reaches a slice allocation before a
// single row is read, so an unbounded one turns one request into an
// out-of-memory kill of the process.
const MaxPageSize = 1000

// Size returns the page size to use: the requested limit, defaulted and
// bounded, so every backend agrees without repeating the rule.
func (p Page) Size() int {
	switch {
	case p.Limit <= 0:
		return DefaultPageSize
	case p.Limit > MaxPageSize:
		return MaxPageSize
	default:
		return p.Limit
	}
}
