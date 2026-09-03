package mdmctl

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/deploymenttheory/go-apple-mdm/internal/mdmctl/adminclient"
)

// emit writes one server response in the selected mode.
//
// In json mode the server's bytes are written unchanged. Re-encoding would
// reorder keys and reformat numbers, which is exactly what makes nanohubctl's
// output unusable for anything that cares about canonical JSON.
func (e *env) emit(resp *adminclient.Response, human func(w *tabwriter.Writer)) error {
	switch e.opts.output {
	case outputJSON, outputNDJSON:
		if _, err := e.stdout.Write(resp.Body); err != nil {
			return fmt.Errorf("mdmctl: write: %w", err)
		}
		if len(resp.Body) > 0 && resp.Body[len(resp.Body)-1] != '\n' {
			_, _ = fmt.Fprintln(e.stdout)
		}
		return nil
	case outputHuman:
		if human == nil {
			if _, err := e.stdout.Write(resp.Body); err != nil {
				return fmt.Errorf("mdmctl: write: %w", err)
			}
			return nil
		}
		tw := tabwriter.NewWriter(e.stdout, 0, 8, 2, ' ', 0)
		human(tw)
		if err := tw.Flush(); err != nil {
			return fmt.Errorf("mdmctl: write: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown -output %q (want human, json, or ndjson)", ErrUsage, e.opts.output)
	}
}

// list renders a paged listing.
//
// With -all the cursor is followed to the end. Without it, one page is printed
// and the next cursor goes to stderr, so stdout stays machine-clean and a
// caller piping to jq is never handed a pagination hint mixed into the data.
func (e *env) list(ctx context.Context, c *adminclient.Client, path string, q url.Values, header []string, row func(jsontext.Value) []string) error {
	if q == nil {
		q = url.Values{}
	}
	if e.opts.limit > 0 {
		q.Set("limit", strconv.Itoa(e.opts.limit))
	}

	switch e.opts.output {
	case outputNDJSON:
		return e.streamNDJSON(ctx, c, path, q)
	case outputJSON:
		if !e.opts.all {
			resp, err := c.Do(ctx, "GET", path, q, nil)
			if err != nil {
				return err
			}
			return e.emit(resp, nil)
		}
		return e.streamNDJSON(ctx, c, path, q)
	}

	tw := tabwriter.NewWriter(e.stdout, 0, 8, 2, ' ', 0)
	if len(header) > 0 {
		fmt.Fprintln(tw, strings.Join(header, "\t"))
	}
	emitRow := func(item jsontext.Value) error {
		fmt.Fprintln(tw, strings.Join(row(item), "\t"))
		return nil
	}
	var err error
	if e.opts.all {
		err = c.Each(ctx, path, q, emitRow)
	} else {
		var items []jsontext.Value
		var next string
		items, next, err = c.Page(ctx, path, q)
		if err == nil {
			for _, it := range items {
				_ = emitRow(it)
			}
			if next != "" {
				defer fmt.Fprintf(e.stderr, "mdmctl: more results; next cursor %s (use -all to follow)\n", next)
			}
		}
	}
	if err != nil {
		return err
	}
	if ferr := tw.Flush(); ferr != nil {
		return fmt.Errorf("mdmctl: write: %w", ferr)
	}
	return nil
}

// streamNDJSON writes one item per line, following cursors.
func (e *env) streamNDJSON(ctx context.Context, c *adminclient.Client, path string, q url.Values) error {
	return c.Each(ctx, path, q, func(item jsontext.Value) error {
		if _, err := e.stdout.Write(item); err != nil {
			return fmt.Errorf("mdmctl: write: %w", err)
		}
		_, _ = fmt.Fprintln(e.stdout)
		return nil
	})
}

// field reads one string field out of a raw JSON object, for table rows.
func field(item jsontext.Value, name string) string {
	var m map[string]jsontext.Value
	if err := json.Unmarshal(item, &m); err != nil {
		return ""
	}
	raw, ok := m[name]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

// fields reads a string-slice field, rendered comma-separated.
func fields(item jsontext.Value, name string) string {
	var m map[string]jsontext.Value
	if err := json.Unmarshal(item, &m); err != nil {
		return ""
	}
	raw, ok := m[name]
	if !ok {
		return ""
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return ""
	}
	return strings.Join(out, ",")
}

// dash renders an empty value as "-", so a column never looks like a value
// that happens to be blank.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
