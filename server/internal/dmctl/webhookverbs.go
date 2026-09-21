package dmctl

import (
	"context"
	"crypto/rand"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/webhook"
)

// runWebhooks parses and executes the webhooks subcommand, reporting argument and operation
// failures to the CLI caller.
func runWebhooks(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: webhooks needs create, update, list, get, delete, pause, resume, enable, disable, rotate, test, catalogue, deliveries, retry, replay or status", ErrUsage)
	}
	verb := args[0]
	fs := e.verbFlags("webhooks " + verb)
	id := fs.String("id", "", "subscription or delivery identifier")
	file := fs.String("file", "-", "subscription or replay JSON file; - reads stdin")
	revision := fs.Int("revision", 0, "expected subscription revision for update")
	after := fs.String("after", "", "last identifier from the previous page")
	subscription := fs.String("subscription", "", "filter deliveries to this subscription")
	dry := fs.Bool("dry-run", false, "preview a replay without creating deliveries")
	key := fs.String("key", "", "replay idempotency key; generated when omitted")
	overlap := fs.Duration("overlap", 24*time.Hour, "signing-key rotation overlap; zero revokes immediately")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("%w: unexpected webhooks arguments", ErrUsage)
	}
	method, path := http.MethodGet, "/webhooks"
	query := url.Values{}
	var body []byte
	needsID := false
	switch verb {
	case "create", "update":
		raw, err := e.readSource(*file)
		if err != nil {
			return err
		}
		var spec webhook.Spec
		if err = json.Unmarshal([]byte(raw), &spec, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("%w: invalid subscription JSON", ErrUsage)
		}
		method = http.MethodPost
		if verb == "update" {
			needsID = true
			method = http.MethodPut
			path += "/" + url.PathEscape(*id)
			if *revision <= 0 {
				return fmt.Errorf("%w: update requires -revision", ErrUsage)
			}
			body, err = json.Marshal(struct {
				Revision int          `json:"revision"`
				Spec     webhook.Spec `json:"spec"`
			}{*revision, spec})
		} else {
			body, err = json.Marshal(spec)
		}
		if err != nil {
			return err
		}
	case "list":
		query.Set("after", *after)
	case "get", "delete":
		needsID = true
		path += "/" + url.PathEscape(*id)
		if verb == "delete" {
			method = http.MethodDelete
		}
	case "pause", "resume", "enable", "disable", "test", "rotate":
		needsID = true
		method = http.MethodPost
		operation := verb
		if verb == "rotate" {
			operation = "credentials"
			body, err = json.Marshal(map[string]int64{"overlap_seconds": int64(overlap.Seconds())})
			if err != nil {
				return err
			}
		}
		path += "/" + url.PathEscape(*id) + "/" + operation
	case "catalogue", "status":
		path += "/" + verb
	case "deliveries":
		path += "/deliveries"
		query.Set("after", *after)
		query.Set("subscription_id", *subscription)
		if *id != "" {
			path += "/" + url.PathEscape(*id)
		}
	case "retry":
		needsID = true
		method = http.MethodPost
		path += "/deliveries/" + url.PathEscape(*id) + "/retry"
	case "replay":
		raw, err := e.readSource(*file)
		if err != nil {
			return err
		}
		var req webhook.ReplayRequest
		if err = json.Unmarshal([]byte(raw), &req, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("%w: invalid replay JSON", ErrUsage)
		}
		if *dry {
			req.DryRun = true
		}
		if *key != "" {
			req.Key = *key
		}
		if req.Key == "" {
			req.Key = rand.Text()
		}
		method = http.MethodPost
		path += "/replays"
		body, err = json.Marshal(req)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unknown webhooks operation", ErrUsage)
	}
	if needsID && *id == "" {
		return fmt.Errorf("%w: operation requires -id", ErrUsage)
	}
	if e.opts.limit != 0 {
		query.Set("limit", strconv.Itoa(e.opts.limit))
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	response, err := c.Do(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	if len(response.Body) == 0 {
		return nil
	}
	return e.emit(response, nil)
}
