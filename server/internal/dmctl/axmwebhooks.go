package dmctl

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// runAXMWebhooks administers Apple subscriptions through a named server account.
func runAXMWebhooks(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: axm webhooks needs list, create, get, update, delete, bindings, status, receipts, deliveries, ping, redeliver, rotate or reconcile", ErrUsage)
	}
	verb := args[0]
	fs := e.verbFlags("axm webhooks " + verb)
	account := fs.String("account", "", "named Apple account")
	binding := fs.String("binding", "", "local webhook binding ID")
	file := fs.String("file", "-", "request JSON file; - reads stdin")
	after := fs.String("after", "", "last local item ID")
	start := fs.String("start", "", "inclusive RFC3339 delivery date")
	end := fs.String("end", "", "exclusive RFC3339 delivery date")
	raw := fs.Bool("raw", false, "request original payloads; requires readRawInventory")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) > 0 || *account == "" {
		return fmt.Errorf("%w: named account required; unexpected positional arguments", ErrUsage)
	}
	prefix := "/axm/accounts/" + url.PathEscape(*account)
	path, method := prefix+"/webhooks", http.MethodGet
	query := url.Values{}
	var body []byte
	local := verb == "status" || verb == "receipts" || verb == "reconcile"
	if local {
		path = prefix + "/webhook-bindings"
	}
	switch verb {
	case "list":
	case "bindings":
		path = prefix + "/webhook-bindings"
		query.Set("after", *after)
	case "create":
		method = http.MethodPost
	case "get", "update", "delete", "ping", "deliveries", "redeliver", "rotate", "status", "receipts", "reconcile":
		if *binding == "" {
			return fmt.Errorf("%w: -binding required", ErrUsage)
		}
		path += "/" + url.PathEscape(*binding)
		switch verb {
		case "update":
			method = http.MethodPatch
		case "delete":
			method = http.MethodDelete
		case "ping", "redeliver", "rotate", "reconcile":
			method = http.MethodPost
			path += "/" + verb
		case "deliveries":
			path += "/deliveries"
			query.Set("start", *start)
			query.Set("end", *end)
			if *raw {
				query.Set("raw", "true")
			}
		case "receipts":
			path += "/receipts"
			query.Set("after", *after)
			if *raw {
				path += "/raw"
			}
		}
	default:
		return fmt.Errorf("%w: unknown AXM webhook verb", ErrUsage)
	}
	if verb == "create" || verb == "update" || verb == "redeliver" || verb == "rotate" || verb == "reconcile" {
		input, err := e.readSource(*file)
		if err != nil {
			return err
		}
		body = []byte(input)
	}
	client, err := e.client()
	if err != nil {
		return err
	}
	response, err := client.Do(ctx, method, path, query, body)
	if err != nil {
		return wrapError(err)
	}
	return e.emit(response, nil)
}
