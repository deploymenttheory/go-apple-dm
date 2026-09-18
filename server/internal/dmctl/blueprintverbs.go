package dmctl

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
)

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
