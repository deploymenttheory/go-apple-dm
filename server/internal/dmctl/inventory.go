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
	if family == "axm" {
		fs.Usage = func() { _, _ = fmt.Fprint(e.stderr, axmHelp); fs.PrintDefaults() }
	}
	if family == "devices" {
		fs.Usage = func() {
			_, _ = fmt.Fprint(e.stderr, devicesHelp)
			fs.PrintDefaults()
		}
	}
	cron := fs.String("cron", "", "five-field cron expression for schedule set")
	every := fs.Int("every", 0, "repeat interval count for schedule set")
	unit := fs.String("unit", "", "repeat unit: minutes, hours, days, weeks, months")
	zone := fs.String("zone", "UTC", "IANA time zone for schedule set")
	disabled := fs.Bool("disabled", false, "disable a saved schedule")
	file := fs.String("file", "", "JSON request file, or - for stdin (keys belong in a file, never arguments)")
	account := fs.String("account", "", "AxM source account ID")
	focus := fs.String("focus", "", "combined, apple, or managed")
	search := fs.String("search", "", "search source identities and kinds")
	where := fs.String("where", "", "JSON condition array: [{\"field\":\"data.attributes.imei\",\"operator\":\"contains\",\"value\":\"...\"}]")
	source := fs.String("source", "", "source kind, for example axm.device")
	cursor := fs.String("cursor", "", "continuation cursor")
	format := fs.String("format", "json", "export format: json, ndjson, csv")
	columns := fs.String("columns", "", "ordered, comma-separated export fields")
	raw := fs.Bool("raw", false, "request complete source evidence (requires readRawInventory)")
	force := fs.Bool("force", false, "bypass successful snapshot freshness")
	revision := fs.Int64("revision", 0, "expected account revision for delete")
	wait := fs.Bool("wait", false, "wait for axm accounts sync to finish; does not wait for devices collect")
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
		case "inspect":
			if id(1) != "" {
				path = "/inventory/serials/" + id(1)
				if *raw {
					path = "/inventory/raw/serials/" + id(1)
				}
			}
		case "evidence":
			if id(1) != "" {
				path = prefix + "/" + id(1) + "/evidence"
			}
		case "resolve":
			if id(1) != "" && *raw {
				method = http.MethodPost
				path = prefix + "/" + id(1) + "/resolve"
			}
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
			case "diagnostics", "services", "device", "coverage", "assigned-service", "assignment-service", "configure-service", "assign", "unassign", "operation", "operation-log", "jobs", "job":
				if id(2) != "" {
					path = axmAccountOperation(pos, id)
					switch pos[1] {
					case "configure-service":
						method = http.MethodPut
					case "assign", "unassign":
						method = http.MethodPost
					}
				}
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
		case "correlate":
			method = http.MethodPost
			path = "/inventory/correlations"
		case "sync":
			method = http.MethodPost
			path = "/inventory/sync"
		case "reports", "diagnostics", "presets":
			path = "/inventory/" + pos[0]
			if *raw && pos[0] == "presets" {
				path = "/inventory/raw/presets"
			}
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
		return fmt.Errorf("%w: unknown or incomplete %s operation; use devices list|get|inspect|evidence|resolve|fields|collect, axm accounts list|create|get|update|delete|verify|sync|diagnostics|services|device|coverage|assigned-service|assignment-service|configure-service|assign|unassign|operation|operation-log|jobs|job, inventory correlate|sync|jobs|schedules|reports|export|presets|diagnostics", ErrUsage, family)
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
	if method == http.MethodGet && (strings.HasSuffix(path, "/exports") || strings.HasSuffix(path, "/diagnostics") && family == "inventory" || strings.HasSuffix(path, "/log")) {
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
			resp, err = cl.Do(ctx, http.MethodGet, "/axm/accounts/"+id(2)+"/jobs/"+url.PathEscape(job.ID), nil, nil)
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

// axmAccountOperation requires a named account and opaque resource/operation IDs.
func axmAccountOperation(pos []string, id func(int) string) string {
	prefix := "/axm/accounts/" + id(2)
	switch pos[1] {
	case "diagnostics", "services", "jobs":
		return prefix + "/" + pos[1]
	case "assignment-service", "configure-service":
		return prefix + "/assignment-service"
	case "assign":
		return prefix + "/assignments"
	case "unassign":
		return prefix + "/unassignments"
	}
	if id(3) == "" {
		return ""
	}
	switch pos[1] {
	case "device":
		return prefix + "/devices/" + id(3)
	case "coverage":
		return prefix + "/devices/" + id(3) + "/coverage"
	case "assigned-service":
		return prefix + "/devices/" + id(3) + "/assigned-service"
	case "operation":
		return prefix + "/assignments/" + id(3)
	case "operation-log":
		return prefix + "/assignments/" + id(3) + "/log"
	case "job":
		return prefix + "/jobs/" + id(3)
	}
	return ""
}
