package dmctl

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
)

// runAppsBooks executes an explicit location or licensing operation with bounded
// JSON input. Credentials are read from files/stdin and never placed in URLs.
func runAppsBooks(ctx context.Context, e *env, args []string) error {
	return runContentOperation(ctx, e, args, false)
}

// runManagedContent exposes safe inspection and explicit queued content intent.
func runManagedContent(ctx context.Context, e *env, args []string) error {
	return runContentOperation(ctx, e, args, true)
}

// runContentOperation maps an explicit CLI operation and bounded JSON object to its authorized endpoint.
func runContentOperation(ctx context.Context, e *env, args []string, managed bool) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: operation required", ErrUsage)
	}
	verb := args[0]
	fs := e.verbFlags("content " + verb)
	file := fs.String("file", "-", "JSON request file, or - for stdin")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) < 1 {
		return fmt.Errorf("%w: explicit ID required", ErrUsage)
	}
	path := "/appsbooks/locations/" + url.PathEscape(rest[0])
	method := http.MethodPost
	if managed {
		path = "/managed-content/operations/" + url.PathEscape(rest[0])
	}
	if managed {
		if len(rest) != 1 {
			return ErrUsage
		}
		switch verb {
		case "get":
			method = http.MethodGet
		case "evidence":
			method = http.MethodGet
			path += "/evidence"
		case "create":
			method = http.MethodPut
		case "step", "reconcile", "cancel":
			path += "/" + verb
		default:
			return ErrUsage
		}
	} else {
		switch verb {
		case "get":
			method = http.MethodGet
		case "evidence":
			method = http.MethodGet
			path += "/evidence"
		case "register":
			method = http.MethodPut
		case "receiver":
			method = http.MethodPut
			path += "/receiver"
		case "configure", "refresh", "probe", "recover-configuration", "recover-licensing":
			path += "/" + verb
		case "job-get", "job-evidence", "job-create", "submit", "poll", "reconcile":
			if len(rest) != 2 {
				return ErrUsage
			}
			path += "/jobs/" + url.PathEscape(rest[1])
			switch verb {
			case "job-get":
				method = http.MethodGet
			case "job-evidence":
				method = http.MethodGet
				path += "/evidence"
			case "job-create":
				method = http.MethodPut
			default:
				path += "/" + verb
			}
		default:
			return ErrUsage
		}
		if len(rest) > 1 && verb != "job-get" && verb != "job-evidence" && verb != "job-create" && verb != "submit" && verb != "poll" && verb != "reconcile" {
			return ErrUsage
		}
	}
	var body any
	if method != http.MethodGet && verb != "cancel" {
		source, err := e.readSource(*file)
		if err != nil {
			return err
		}
		raw := jsontext.Value(source)
		var object map[string]jsontext.Value
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return fmt.Errorf("%w: JSON object required", ErrUsage)
		}
		body = raw
	}
	client, err := e.client()
	if err != nil {
		return err
	}
	response, err := client.Do(ctx, method, path, nil, body)
	if err != nil {
		return err
	}
	return e.emit(response, nil)
}
