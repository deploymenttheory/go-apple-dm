package dmctl

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/explain"
)

// runBlueprints parses and executes the blueprints subcommand, reporting argument and
// operation failures to the CLI caller.
func runBlueprints(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: blueprints needs validate, publish, list, get, delete, assign or unassign", ErrUsage)
	}
	sub := args[0]
	fs := e.verbFlags("blueprints " + sub)
	file := fs.String("file", "-", "Blueprint JSON file, or - for stdin")
	revision := fs.String("revision", "", "current revision required for updates and deletion")
	channel := fs.String("channel", "device", "enrollment channel: device or user")
	parent := fs.String("parent", "", "parent device ID for a user channel")
	target := fs.String("target", "", "validation target: OS:version,channel=device|user[,supervised,...] (validate only)")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	path, method := "/blueprints", http.MethodGet
	q := url.Values{}
	if *target != "" {
		if sub != "validate" {
			return fmt.Errorf("%w: -target applies only to blueprints validate", ErrUsage)
		}
		t, err := explain.ParseTarget(*target)
		if err != nil || t.Version.IsZero() || t.Channel == "" {
			return fmt.Errorf("%w: -target requires a valid OS, version and channel", ErrUsage)
		}
		q.Set("os", string(t.OS))
		q.Set("version", t.Version.String())
		q.Set("channel", string(t.Channel))
		for key, value := range map[string]bool{"supervised": t.Supervised, "sharedIPad": t.SharedIPad, "userEnrollment": t.UserEnrollment, "dep": t.DEP, "userApproved": t.UserApproved} {
			q.Set(key, strconv.FormatBool(value))
		}
	}
	var body any
	switch sub {
	case "validate", "publish":
		if len(rest) != 0 {
			return fmt.Errorf("%w: use -file for Blueprint source", ErrUsage)
		}
		raw, err := e.readSource(*file)
		if err != nil {
			return err
		}
		var spec blueprint.Spec
		if err := json.Unmarshal([]byte(raw), &spec, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("%w: invalid Blueprint JSON", ErrUsage)
		}
		body = spec
		if sub == "validate" {
			path += "/validate"
			method = http.MethodPost
		} else {
			if !blueprint.ValidIdentifier(spec.Identifier) {
				return fmt.Errorf("%w: invalid Blueprint identifier", ErrUsage)
			}
			path += "/" + spec.Identifier
			method = http.MethodPut
		}
	case "list":
		if len(rest) != 0 {
			return ErrUsage
		}
		return e.list(ctx, c, path, q, []string{"BLUEPRINT", "REVISION"}, func(row jsontext.Value) []string {
			var v blueprints.Record
			_ = json.Unmarshal(row, &v)
			return []string{v.Spec.Identifier, v.Revision}
		})
	case "get", "delete":
		if len(rest) != 1 || !blueprint.ValidIdentifier(rest[0]) {
			return fmt.Errorf("%w: Blueprint identifier required", ErrUsage)
		}
		path += "/" + rest[0]
		if sub == "delete" {
			if *revision == "" {
				return fmt.Errorf("%w: delete requires -revision", ErrUsage)
			}
			method = http.MethodDelete
		}
	case "assign", "unassign":
		if len(rest) != 2 || !blueprint.ValidIdentifier(rest[0]) {
			return fmt.Errorf("%w: provide Blueprint identifier and enrollment ID", ErrUsage)
		}
		path, q = enrollmentPath(*channel, rest[1], *parent, q)
		path += "/blueprints/" + rest[0]
		method = http.MethodPut
		if sub == "unassign" {
			method = http.MethodDelete
		}
	default:
		return fmt.Errorf("%w: unknown Blueprint operation", ErrUsage)
	}
	headers := http.Header{}
	if *revision != "" {
		headers.Set("If-Match", `"`+*revision+`"`)
	}
	resp, err := c.DoWithHeaders(ctx, method, path, q, body, headers)
	if err != nil {
		return err
	}
	if resp.Status == http.StatusNoContent {
		return nil
	}
	return e.emit(resp, nil)
}
