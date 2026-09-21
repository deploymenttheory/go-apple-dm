package dmctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

// runDevices dispatches the device-record CLI family.
func runDevices(ctx context.Context, e *env, args []string) error {
	return runInventoryCommand(ctx, e, "devices", args)
}

// runAxM dispatches named Apple connection administration.
func runAxM(ctx context.Context, e *env, args []string) error {
	return runInventoryCommand(ctx, e, "axm", args)
}

// runInventory dispatches inventory jobs, schedules, reports and exports.
func runInventory(ctx context.Context, e *env, args []string) error {
	return runInventoryCommand(ctx, e, "inventory", args)
}

// runInventoryCommand gives all inventory operations the same filter and projection flags.
func runInventoryCommand(ctx context.Context, e *env, family string, args []string) error {
	fs := e.verbFlags(family)
	cron := fs.String("cron", "", "five-field cron expression for schedule set")
	every := fs.Int("every", 0, "repeat interval count for schedule set")
	unit := fs.String("unit", "", "repeat unit: minutes, hours, days, weeks, months")
	zone := fs.String("zone", "UTC", "IANA time zone for schedule set")
	disabled := fs.Bool("disabled", false, "disable a saved schedule")
	file := fs.String("file", "", "JSON request file, or - for stdin (keys belong in a file, never arguments)")
	account := fs.String("account", "", "AxM source account ID")
	focus := fs.String("focus", "", "combined, apple, or managed")
	search := fs.String("search", "", "search public inventory fields")
	where := fs.String("where", "", "JSON condition array: [{\"field\":\"imei\",\"operator\":\"contains\",\"value\":\"...\"}]")
	source := fs.String("source", "", "source kind, for example axm.device")
	cursor := fs.String("cursor", "", "continuation cursor")
	format := fs.String("format", "json", "export format: json, ndjson, csv")
	columns := fs.String("columns", "", "ordered, comma-separated export fields")
	raw := fs.Bool("raw", false, "request complete Apple responses (requires readRawInventory)")
	force := fs.Bool("force", false, "bypass successful snapshot freshness")
	revision := fs.Int64("revision", 0, "expected account revision for delete")
	wait := fs.Bool("wait", false, "wait for a submitted sync job to finish")
	pos, err := e.parseVerb(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("%w: %s requires an operation", ErrUsage, family)
	}
	q := url.Values{}
	for k, v := range map[string]string{"account": *account, "focus": *focus, "search": *search, "where": *where, "source": *source, "cursor": *cursor} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if e.opts.limit > 0 {
		q.Set("limit", strconv.Itoa(e.opts.limit))
	}
	if *where != "" {
		var conditions []inventory.Condition
		if json.Unmarshal([]byte(*where), &conditions) != nil {
			return fmt.Errorf("%w: invalid condition JSON", ErrUsage)
		}
	}
	method, path := http.MethodGet, ""
	id := func(index int) string {
		if len(pos) <= index {
			return ""
		}
		return url.PathEscape(pos[index])
	}
	switch family {
	case "devices":
		prefix := "/devices"
		if *raw {
			prefix = "/inventory/raw/devices"
		}
		switch pos[0] {
		case "list":
			path = prefix
		case "fields":
			path = prefix + "/fields"
		case "get":
			if id(1) != "" {
				path = prefix + "/" + id(1)
			}
		case "collect":
			if id(1) != "" {
				method = http.MethodPost
				path = "/devices/" + id(1) + "/collect"
			}
		}
	case "axm":
		if pos[0] == "accounts" && len(pos) > 1 {
			switch pos[1] {
			case "list":
				path = "/axm/accounts"
			case "create":
				method = http.MethodPost
				path = "/axm/accounts"
			case "get", "update", "delete", "verify", "sync":
				if id(2) != "" {
					path = "/axm/accounts/" + id(2)
					switch pos[1] {
					case "update":
						method = http.MethodPut
					case "delete":
						method = http.MethodDelete
						q.Set("revision", strconv.FormatInt(*revision, 10))
					case "verify", "sync":
						method = http.MethodPost
						path += "/" + pos[1]
					}
				}
			}
		}
	case "inventory":
		switch pos[0] {
		case "sync":
			method = http.MethodPost
			path = "/inventory/sync"
		case "reports", "diagnostics", "presets":
			path = "/inventory/" + pos[0]
			if pos[0] == "presets" && len(pos) > 2 && pos[1] == "save" {
				method = http.MethodPut
				path += "/" + id(2)
			}
		case "export":
			path = "/inventory/exports"
			if *raw {
				path = "/inventory/raw/exports"
			}
			q.Set("format", *format)
			if *columns != "" {
				q.Set("columns", *columns)
			}
		case "jobs":
			if len(pos) > 1 && pos[1] == "cancel-all" {
				method = http.MethodPost
				path = "/inventory/jobs/cancel-all"
			} else if len(pos) == 1 || pos[1] == "list" {
				path = "/inventory/jobs"
			} else if id(2) != "" {
				path = "/inventory/jobs/" + id(2)
				if pos[1] != "get" {
					method = http.MethodPost
					path += "/" + pos[1]
				}
			}
		case "schedules":
			path = "/inventory/schedules"
			if len(pos) > 2 && pos[1] == "set" {
				method = http.MethodPut
				path += "/" + id(2)
			}
		}
	}
	if path == "" {
		return fmt.Errorf("%w: unknown or incomplete %s operation; use devices list|get|fields|collect, axm accounts list|create|get|update|delete|verify|sync, inventory sync|jobs|schedules|reports|export|presets|diagnostics", ErrUsage, family)
	}
	if *force {
		q.Set("force", "true")
	}
	var body any
	if *file != "" {
		src, err := e.readSource(*file)
		if err != nil {
			return err
		}
		body = src
	}
	if family == "inventory" && pos[0] == "schedules" && method == http.MethodPut && *file == "" {
		schedule := inventory.Schedule{Expression: *cron, RepeatEvery: *every, RepeatUnit: *unit, TimeZone: *zone, Enabled: !*disabled, Anchor: time.Now().UTC()}
		if _, err := schedule.NextOccurrence(time.Now().UTC()); err != nil {
			return fmt.Errorf("%w: invalid schedule", ErrUsage)
		}
		body = schedule
	}
	cl, err := e.client()
	if err != nil {
		return err
	}
	// Streaming exports are the all-pages path; normal lists remain bounded.
	if e.opts.all && family == "devices" && pos[0] == "list" {
		path = "/inventory/exports"
		if *raw {
			path = "/inventory/raw/exports"
		}
		q.Set("format", "json")
		if e.opts.output == outputNDJSON {
			q.Set("format", "ndjson")
		}
	}
	if method == http.MethodGet && (strings.HasSuffix(path, "/exports") || strings.HasSuffix(path, "/diagnostics")) {
		return wrapError(cl.Download(ctx, path, q, e.stdout))
	}
	resp, err := cl.Do(ctx, method, path, q, body)
	if err != nil {
		return wrapError(err)
	}
	if *wait && method == http.MethodPost && strings.HasSuffix(path, "/sync") && family == "axm" {
		var job inventory.Job
		if err := json.Unmarshal(resp.Body, &job); err != nil {
			return err
		}
		for job.FinishedAt == nil && job.State != "paused" {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			resp, err = cl.Do(ctx, http.MethodGet, "/inventory/jobs/"+url.PathEscape(job.ID), nil, nil)
			if err != nil {
				return wrapError(err)
			}
			if err := json.Unmarshal(resp.Body, &job); err != nil {
				return err
			}
		}
		if err := e.emit(resp, nil); err != nil {
			return err
		}
		if job.State != "success" {
			return ErrPartial
		}
		return nil
	}
	return e.emit(resp, nil)
}
