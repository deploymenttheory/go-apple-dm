package dmctl

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/url"
)

func runContentCache(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: content-cache needs reports, rotate or revoke", ErrUsage)
	}
	sub := args[0]
	if sub != "reports" && sub != "rotate" && sub != "revoke" {
		return fmt.Errorf("%w: unknown content-cache operation", ErrUsage)
	}
	fs := e.verbFlags("content-cache " + sub)
	cursor := fs.String("cursor", "", "report continuation cursor")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("%w: content-cache needs a device enrollment ID", ErrUsage)
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	path, q := enrollmentPath("device", rest[0], "", url.Values{})
	path += "/content-cache/"
	if sub == "reports" {
		q.Set("cursor", *cursor)
		return e.list(ctx, c, path+"reports", q, []string{"ID", "RECEIVED"}, func(row jsontext.Value) []string { return []string{field(row, "ID"), field(row, "ReceivedAt")} })
	}
	method := http.MethodPost
	if sub == "revoke" {
		method = http.MethodDelete
	}
	response, err := c.Do(ctx, method, path+"credential", nil, nil)
	if err != nil {
		return err
	}
	if sub == "revoke" {
		return nil
	}
	return e.emit(response, nil)
}
