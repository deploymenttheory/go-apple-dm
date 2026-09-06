package axm

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Page is one page of a list response.
type Page[T any] struct {
	Items    []T
	Links    Links
	Meta     Meta
	Included []IncludedResource
}

// HasNext reports whether Apple provided a next link.
func (p Page[T]) HasNext() bool { return p.Links.Next != "" }

// document is the wire form of a paged response.
type document[T any] struct {
	Data     []T                `json:"data"`
	Links    Links              `json:"links"`
	Meta     Meta               `json:"meta"`
	Included []IncludedResource `json:"included"`
}

// single is the wire form of a one-resource response.
type single[T any] struct {
	Data     T                  `json:"data"`
	Links    Links              `json:"links"`
	Included []IncludedResource `json:"included"`
}

// ListOptions are the query parameters shared by the list endpoints.
type ListOptions struct {
	// Fields selects the attributes to return (fields[type]).
	Fields []string
	// Limit is the page size, 1 to MaxLimit; 0 lets Apple choose.
	Limit int
	// Cursor continues an earlier page (meta.paging.nextCursor).
	Cursor string
}

// GetOptions are the query parameters shared by the single-resource
// endpoints.
type GetOptions struct {
	// Fields selects the attributes to return (fields[type]).
	Fields []string
}

// query builds the query for a list call of resource type typ.
func (o ListOptions) query(typ string) (url.Values, error) {
	q := url.Values{}
	if len(o.Fields) > 0 {
		q.Set("fields["+typ+"]", strings.Join(o.Fields, ","))
	}
	if o.Limit != 0 {
		if o.Limit < 1 || o.Limit > MaxLimit {
			return nil, fmt.Errorf("%w: got %d", ErrLimit, o.Limit)
		}
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Cursor != "" {
		q.Set("cursor", o.Cursor)
	}
	return q, nil
}

// query builds the query for a get call of resource type typ.
func (o GetOptions) query(typ string) url.Values {
	q := url.Values{}
	if len(o.Fields) > 0 {
		q.Set("fields["+typ+"]", strings.Join(o.Fields, ","))
	}
	return q
}

// list performs a GET returning a page.
func list[T any](ctx context.Context, c *Client, path string, q url.Values) (Page[T], error) {
	var doc document[T]
	if err := c.do(ctx, request{method: http.MethodGet, path: path, query: q}, &doc); err != nil {
		return Page[T]{}, err
	}
	return Page[T]{Items: doc.Data, Links: doc.Links, Meta: doc.Meta, Included: doc.Included}, nil
}

// get performs a GET returning one resource.
func get[T any](ctx context.Context, c *Client, path string, q url.Values) (*T, error) {
	var doc single[T]
	if err := c.do(ctx, request{method: http.MethodGet, path: path, query: q}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// NextPage fetches the page at page.Links.Next. Query parameters of the
// current page's self link that the next link lacks are carried over, so a
// fields selection survives. ErrNextLink is returned when there is no next
// link or it points off the API host.
func NextPage[T any](ctx context.Context, c *Client, page Page[T]) (Page[T], error) {
	next, err := c.nextURL(page.Links)
	if err != nil {
		return Page[T]{}, err
	}
	var doc document[T]
	if err := c.do(ctx, request{method: http.MethodGet, rawURL: next}, &doc); err != nil {
		return Page[T]{}, err
	}
	return Page[T]{Items: doc.Data, Links: doc.Links, Meta: doc.Meta, Included: doc.Included}, nil
}

// nextURL resolves links.next against the base URL, merges the self
// link's query, and checks the host.
func (c *Client) nextURL(links Links) (string, error) {
	if links.Next == "" {
		return "", fmt.Errorf("%w: no next link", ErrNextLink)
	}
	next, err := c.base.Parse(links.Next)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNextLink, err)
	}
	if next.Scheme != c.base.Scheme || next.Host != c.base.Host {
		return "", fmt.Errorf("%w: %s is not on %s", ErrNextLink, links.Next, c.base.Host)
	}
	if links.Self != "" {
		if self, err := c.base.Parse(links.Self); err == nil {
			q := next.Query()
			for k, v := range self.Query() {
				if _, ok := q[k]; !ok {
					q[k] = v
				}
			}
			next.RawQuery = q.Encode()
		}
	}
	return next.String(), nil
}

// Pages yields first and every page after it by following links.next,
// stopping at the client's page cap (yielding ErrPageCap) or when the
// context is done.
func Pages[T any](ctx context.Context, c *Client, first Page[T]) iter.Seq2[Page[T], error] {
	return func(yield func(Page[T], error) bool) {
		page := first
		for n := 1; ; n++ {
			if !yield(page, nil) {
				return
			}
			if !page.HasNext() {
				return
			}
			if n >= c.cfg.PageCap {
				yield(Page[T]{}, fmt.Errorf("%w: %d pages", ErrPageCap, n))
				return
			}
			if err := ctx.Err(); err != nil {
				yield(Page[T]{}, fmt.Errorf("%w: %w", ErrTransport, err))
				return
			}
			c.log.DebugContext(ctx, "axm: following links.next", "page", n+1, "total", page.Meta.Paging.Total)
			next, err := NextPage(ctx, c, page)
			if err != nil {
				yield(Page[T]{}, err)
				return
			}
			page = next
		}
	}
}

// All collects the items of first and every following page. On error the
// items collected so far are returned with it.
func All[T any](ctx context.Context, c *Client, first Page[T]) ([]T, error) {
	var out []T
	for page, err := range Pages(ctx, c, first) {
		if err != nil {
			return out, err
		}
		out = append(out, page.Items...)
	}
	return out, nil
}

// Each calls fn for every item of first and every following page; an
// error from fn stops the walk and is returned.
func Each[T any](ctx context.Context, c *Client, first Page[T], fn func(T) error) error {
	for page, err := range Pages(ctx, c, first) {
		if err != nil {
			return err
		}
		for _, item := range page.Items {
			if err := fn(item); err != nil {
				return err
			}
		}
	}
	return nil
}
