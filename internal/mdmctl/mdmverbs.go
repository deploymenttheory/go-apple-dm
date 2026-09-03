package mdmctl

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// enrollmentPath builds the /enrollments/{channel}/{id} prefix every
// per-enrollment route shares. The user channel needs its device parent,
// which travels as a query parameter rather than another positional.
func enrollmentPath(channel, id, parent string, q url.Values) (string, url.Values) {
	if q == nil {
		q = url.Values{}
	}
	if parent != "" {
		q.Set("parent", parent)
	}
	return "/enrollments/" + url.PathEscape(channel) + "/" + url.PathEscape(id), q
}

// needTarget reads the channel and id every per-enrollment subcommand takes.
func needTarget(e *env, name string, args []string) (channel, id, parent string, err error) {
	fs := e.verbFlags(name)
	p := fs.String("parent", "", "the device enrollment a user channel belongs to")
	rest, err := e.parseVerb(fs, args)
	if err != nil {
		return "", "", "", err
	}
	if len(rest) < 2 {
		return "", "", "", fmt.Errorf("%w: %s needs a channel and an id, for example: device UDID", ErrUsage, name)
	}
	return rest[0], rest[1], *p, nil
}

// runEnrollments reads and disables the enrollments the server manages.
func runEnrollments(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: enrollments needs a subcommand: list, get, disable", ErrUsage)
	}
	sub, rest := args[0], args[1:]
	c, err := e.client()
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		fs := e.verbFlags("enrollments list")
		var (
			channel = fs.String("channel", "", "only this channel, for example device or user")
			serial  = fs.String("serial", "", "only this serial number")
			enabled = fs.String("enabled", "", "only enabled (true) or disabled (false) enrollments")
		)
		if _, err := e.parseVerb(fs, rest); err != nil {
			return err
		}
		q := url.Values{}
		for key, val := range map[string]string{"channel": *channel, "serial": *serial, "enabled": *enabled} {
			if val != "" {
				q.Set(key, val)
			}
		}
		err := e.list(ctx, c, "/enrollments", q,
			[]string{"CHANNEL", "ID", "ENABLED", "SERIAL", "OS", "LAST SEEN"},
			func(item jsontext.Value) []string {
				return []string{
					field(item, "Channel"), field(item, "ID"), field(item, "Enabled"),
					dash(field(item, "SerialNumber")), dash(field(item, "OSVersion")),
					dash(field(item, "LastSeenAt")),
				}
			})
		return e.explainNotFound(ctx, c, "mdm", err)
	case "get":
		channel, id, parent, err := needTarget(e, "enrollments get", rest)
		if err != nil {
			return err
		}
		path, q := enrollmentPath(channel, id, parent, nil)
		resp, err := c.Do(ctx, http.MethodGet, path, q, nil)
		if err != nil {
			return e.explainNotFound(ctx, c, "mdm", err)
		}
		return e.emit(resp, nil)
	case "disable":
		channel, id, parent, err := needTarget(e, "enrollments disable", rest)
		if err != nil {
			return err
		}
		path, q := enrollmentPath(channel, id, parent, nil)
		_, err = c.Do(ctx, http.MethodDelete, path, q, nil)
		return e.explainNotFound(ctx, c, "mdm", err)
	default:
		return fmt.Errorf("%w: unknown enrollments subcommand %q", ErrUsage, sub)
	}
}

// runCommands drives the command queue: send one, read the queue, clear it.
func runCommands(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: commands needs a subcommand: send, list, clear", ErrUsage)
	}
	sub, rest := args[0], args[1:]
	c, err := e.client()
	if err != nil {
		return err
	}
	switch sub {
	case "send":
		fs := e.verbFlags("commands send")
		file := fs.String("file", "", "read the command plist from a file, or - for stdin")
		parent := fs.String("parent", "", "the device enrollment a user channel belongs to")
		positional, err := e.parseVerb(fs, rest)
		if err != nil {
			return err
		}
		if len(positional) < 2 {
			return fmt.Errorf("%w: commands send needs a channel and an id", ErrUsage)
		}
		// The command travels as the plist the device will receive, so any
		// command the schema describes can be sent without this CLI growing
		// a subcommand per RequestType.
		src, err := e.readSource(*file)
		if err != nil {
			return err
		}
		path, q := enrollmentPath(positional[0], positional[1], *parent, nil)
		resp, err := c.Do(ctx, http.MethodPost, path+"/commands", q, src)
		if err != nil {
			return e.explainNotFound(ctx, c, "mdm", err)
		}
		return e.emit(resp, nil)
	case "list":
		fs := e.verbFlags("commands list")
		typ := fs.String("type", "", "only this RequestType")
		parent := fs.String("parent", "", "the device enrollment a user channel belongs to")
		positional, err := e.parseVerb(fs, rest)
		if err != nil {
			return err
		}
		if len(positional) < 2 {
			return fmt.Errorf("%w: commands list needs a channel and an id", ErrUsage)
		}
		q := url.Values{}
		if *typ != "" {
			q.Set("type", *typ)
		}
		path, q := enrollmentPath(positional[0], positional[1], *parent, q)
		err = e.list(ctx, c, path+"/commands", q,
			[]string{"UUID", "REQUEST TYPE", "STATE", "ATTEMPTS", "STATUS"},
			func(item jsontext.Value) []string {
				return []string{
					field(item, "CommandUUID"), field(item, "RequestType"), field(item, "State"),
					field(item, "Attempts"), dash(field(item, "Status")),
				}
			})
		return e.explainNotFound(ctx, c, "mdm", err)
	case "clear":
		fs := e.verbFlags("commands clear")
		typ := fs.String("type", "", "only this RequestType")
		parent := fs.String("parent", "", "the device enrollment a user channel belongs to")
		positional, err := e.parseVerb(fs, rest)
		if err != nil {
			return err
		}
		if len(positional) < 2 {
			return fmt.Errorf("%w: commands clear needs a channel and an id", ErrUsage)
		}
		q := url.Values{}
		if *typ != "" {
			q.Set("type", *typ)
		}
		path, q := enrollmentPath(positional[0], positional[1], *parent, q)
		resp, err := c.Do(ctx, http.MethodDelete, path+"/commands", q, nil)
		if err != nil {
			return e.explainNotFound(ctx, c, "mdm", err)
		}
		return e.emit(resp, nil)
	default:
		return fmt.Errorf("%w: unknown commands subcommand %q", ErrUsage, sub)
	}
}

// runPush wakes a device now, without queueing anything.
func runPush(ctx context.Context, e *env, args []string) error {
	channel, id, parent, err := needTarget(e, "push", args)
	if err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	path, q := enrollmentPath(channel, id, parent, nil)
	resp, err := c.Do(ctx, http.MethodPost, path+"/push", q, nil)
	if err != nil {
		return e.explainNotFound(ctx, c, "mdm", err)
	}
	return e.emit(resp, nil)
}

// runPushCerts reads the push certificates and uploads a renewal.
func runPushCerts(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: pushcerts needs a subcommand: list, put", ErrUsage)
	}
	sub, rest := args[0], args[1:]
	c, err := e.client()
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		fs := e.verbFlags("pushcerts list")
		if _, err := e.parseVerb(fs, rest); err != nil {
			return err
		}
		err := e.list(ctx, c, "/pushcerts", nil,
			[]string{"TOPIC", "NOT AFTER", "VERSION"},
			func(item jsontext.Value) []string {
				return []string{field(item, "Topic"), field(item, "NotAfter"), field(item, "Version")}
			})
		return e.explainNotFound(ctx, c, "mdm", err)
	case "put":
		fs := e.verbFlags("pushcerts put")
		file := fs.String("file", "", "read the JSON body from a file, or - for stdin")
		if _, err := e.parseVerb(fs, rest); err != nil {
			return err
		}
		src, err := e.readSource(*file)
		if err != nil {
			return err
		}
		resp, err := c.Do(ctx, http.MethodPut, "/pushcerts", nil, src)
		if err != nil {
			return e.explainNotFound(ctx, c, "mdm", err)
		}
		return e.emit(resp, nil)
	default:
		return fmt.Errorf("%w: unknown pushcerts subcommand %q", ErrUsage, sub)
	}
}

// runExport streams the enrollment export, which carries bootstrap and
// unlock tokens, so it defaults to NDJSON: this is migration input, not a
// table to read.
func runExport(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("export")
	if _, err := e.parseVerb(fs, args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	err = e.list(ctx, c, "/export", nil,
		[]string{"CHANNEL", "ID", "ENABLED"},
		func(item jsontext.Value) []string {
			return []string{field(item, "Channel"), field(item, "ID"), field(item, "Enabled")}
		})
	return e.explainNotFound(ctx, c, "mdm", err)
}

// runImport writes one exported record back.
func runImport(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("import")
	file := fs.String("file", "", "read the record from a file, or - for stdin")
	if _, err := e.parseVerb(fs, args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	src, err := e.readSource(*file)
	if err != nil {
		return err
	}
	_, err = c.Do(ctx, http.MethodPost, "/import", nil, src)
	return e.explainNotFound(ctx, c, "mdm", err)
}

// runSets manages declaration sets and their assignment to enrollments.
func runSets(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: sets needs a subcommand: add, remove, assign, unassign", ErrUsage)
	}
	sub, rest := args[0], args[1:]
	c, err := e.client()
	if err != nil {
		return err
	}
	switch sub {
	case "add", "remove":
		fs := e.verbFlags("sets " + sub)
		positional, err := e.parseVerb(fs, rest)
		if err != nil {
			return err
		}
		if len(positional) < 2 {
			return fmt.Errorf("%w: sets %s needs a set and a declaration identifier", ErrUsage, sub)
		}
		method := http.MethodPut
		if sub == "remove" {
			method = http.MethodDelete
		}
		path := "/sets/" + url.PathEscape(positional[0]) + "/declarations/" + url.PathEscape(positional[1])
		resp, err := c.Do(ctx, method, path, nil, nil)
		if err != nil {
			return e.explainNotFound(ctx, c, "ddm", err)
		}
		return e.emit(resp, nil)
	case "assign", "unassign":
		fs := e.verbFlags("sets " + sub)
		parent := fs.String("parent", "", "the device enrollment a user channel belongs to")
		positional, err := e.parseVerb(fs, rest)
		if err != nil {
			return err
		}
		if len(positional) < 3 {
			return fmt.Errorf("%w: sets %s needs a channel, an id, and a set", ErrUsage, sub)
		}
		method := http.MethodPut
		if sub == "unassign" {
			method = http.MethodDelete
		}
		path, q := enrollmentPath(positional[0], positional[1], *parent, nil)
		resp, err := c.Do(ctx, method, path+"/sets/"+url.PathEscape(positional[2]), q, nil)
		if err != nil {
			return e.explainNotFound(ctx, c, "ddm", err)
		}
		return e.emit(resp, nil)
	default:
		return fmt.Errorf("%w: unknown sets subcommand %q", ErrUsage, sub)
	}
}

// runNotify drains pending declaration changes and wakes the affected
// devices, rather than waiting for the notifier's next pass.
func runNotify(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("notify")
	if _, err := e.parseVerb(fs, args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodPost, "/notify", nil, nil)
	if err != nil {
		return e.explainNotFound(ctx, c, "ddm", err)
	}
	return e.emit(resp, nil)
}

// runAPI is the escape hatch: any admin route, by method and path.
//
// The dep, axm and acme families are proxies onto Apple-shaped APIs whose
// surface is theirs rather than ours, so wrapping each in a typed verb would
// mean tracking dozens of endpoints we do not define. This keeps every route
// reachable -- which is what "mdmctl drives every admin route" has to mean --
// without pretending to model them.
func runAPI(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("api")
	file := fs.String("file", "", "read the request body from a file, or - for stdin")
	positional, err := e.parseVerb(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 2 {
		return fmt.Errorf("%w: api needs a method and a path, for example: api GET /dep/accounts", ErrUsage)
	}
	method := strings.ToUpper(positional[0])
	path := positional[1]
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	raw, query, _ := strings.Cut(path, "?")
	q, err := url.ParseQuery(query)
	if err != nil {
		return fmt.Errorf("%w: query %q: %w", ErrUsage, query, err)
	}
	var body any
	if *file != "" || method == http.MethodPost || method == http.MethodPut {
		src, err := e.readSource(*file)
		if err != nil {
			return err
		}
		if src != "" {
			body = src
		}
	}
	cl, err := e.client()
	if err != nil {
		return err
	}
	resp, err := cl.Do(ctx, method, raw, q, body)
	if err != nil {
		return err
	}
	return e.emit(resp, nil)
}
