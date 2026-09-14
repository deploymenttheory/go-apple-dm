package dmctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

func runEvents(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: events requires status, list, or retry", ErrUsage)
	}
	verb := args[0]
	if verb != "status" && verb != "list" && verb != "retry" {
		return fmt.Errorf("%w: unknown events operation", ErrUsage)
	}
	fs := e.verbFlags("events " + verb)
	state := fs.String("state", "", "pending, blocked, or delivered")
	deliveries := fs.Bool("deliveries", false, "list destination acknowledgements and retries")
	kind := fs.String("type", "", "event type to list")
	id := fs.String("event-id", "", "event occurrence ID")
	dest := fs.String("destination", "", "destination identifier")
	afterEvent := fs.String("after-event", "", "last event ID from the previous page")
	afterDest := fs.String("after-destination", "", "last destination from the previous page")
	pos, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return fmt.Errorf("%w: unexpected events arguments", ErrUsage)
	}
	method, path := http.MethodGet, "/events/status"
	var body []byte
	query := url.Values{}
	if verb == "list" {
		path = "/events"
		if *deliveries || *state != "" {
			path = "/events/deliveries"
		}
		if *id != "" {
			path = "/events/" + url.PathEscape(*id)
		}
		query.Set("type", *kind)
		query.Set("state", *state)
		query.Set("after_event", *afterEvent)
		query.Set("after_destination", *afterDest)
		if e.opts.limit != 0 {
			query.Set("limit", strconv.Itoa(e.opts.limit))
		}
	}
	if verb == "retry" {
		if *id == "" || *dest == "" {
			return fmt.Errorf("%w: retry requires -event-id and -destination", ErrUsage)
		}
		method, path = http.MethodPost, "/events/"+url.PathEscape(*id)+"/retry"
		body, err = json.Marshal(map[string]string{"destination": *dest})
		if err != nil {
			return wrapError(err)
		}
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, method, path, query, body)
	if err != nil {
		return wrapError(err)
	}
	if len(resp.Body) == 0 {
		return nil
	}
	return e.emit(resp, nil)
}
