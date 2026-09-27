package dmctl

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
)

// runApplicationPackages manages complete package metadata and native delivery.
// Package and manifest input streams are sent without loading installer bytes into memory.
func runApplicationPackages(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: application-packages needs create, update, list, get, delete, delete-multiple, upload, import, manifest-set, manifest-delete, history, note, export, history-export, download, deliver or revoke", ErrUsage)
	}
	sub := args[0]
	fs := e.verbFlags("application-packages " + sub)
	file := fs.String("file", "-", "metadata, installer or manifest source; - reads stdin")
	revision := fs.String("revision", "", "current catalogue revision for mutations")
	backend := fs.String("backend", "", "configured package storage backend")
	kind := fs.String("kind", "", "source kind: file or https")
	source := fs.String("source", "", "path beneath the configured import root, or HTTPS URL")
	filename := fs.String("filename", "", "manifest display filename")
	digest := fs.String("sha256", "", "independently obtained expected SHA-256")
	note := fs.String("note", "", "history note")
	format := fs.String("format", "json", "export format: json or csv")
	fields := fs.String("fields", "", "comma-separated package CSV fields")
	cursor := fs.String("cursor", "", "explicit export or history cursor")
	content := fs.String("content-revision", "", "immutable package content revision")
	delivery := fs.String("method", "mdm", "native delivery: mdm or ddm")
	ttl := fs.Duration("grant-ttl", time.Hour, "download grant lifetime, at most 168h")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	path, method := "/application-packages", http.MethodGet
	q := url.Values{}
	if *cursor != "" {
		q.Set("cursor", *cursor)
	}
	if e.opts.limit > 0 {
		q.Set("limit", strconv.Itoa(e.opts.limit))
	}
	headers := http.Header{}
	var body any
	mutates := map[string]bool{"update": true, "delete": true, "upload": true, "import": true, "manifest-set": true, "manifest-delete": true, "note": true}
	if mutates[sub] && *revision == "" {
		return fmt.Errorf("%w: -revision is required", ErrUsage)
	}
	if *revision != "" {
		if !applications.ValidID(*revision) {
			return ErrUsage
		}
		headers.Set("If-Match", `"`+*revision+`"`)
	}
	if sub != "create" && sub != "list" && sub != "export" && sub != "delete-multiple" {
		if len(rest) < 1 || !applications.ValidID(rest[0]) {
			return fmt.Errorf("%w: package or grant ID is required", ErrUsage)
		}
		path += "/" + rest[0]
	}
	reader := func() (io.Reader, func(), error) {
		if *file == "-" {
			return e.stdin, func() {}, nil
		}
		f, err := os.Open(*file)
		if err != nil {
			return nil, nil, err
		}
		return f, func() { _ = f.Close() }, nil
	}
	switch sub {
	case "delete-multiple":
		if len(rest) != 0 {
			return ErrUsage
		}
		r, close, err := reader()
		if err != nil {
			return err
		}
		defer close()
		data, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
		if err != nil {
			return err
		}
		if len(data) > 1<<20 {
			return ErrUsage
		}
		var request struct {
			Packages []applications.DeleteRequest `json:"packages"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&request); err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF || len(request.Packages) == 0 {
			return ErrUsage
		}
		for _, item := range request.Packages {
			if !applications.ValidID(item.ID) || !applications.ValidID(item.Revision) {
				return ErrUsage
			}
		}
		path += "/delete-multiple"
		method, body = http.MethodPost, request
	case "create", "update":
		want := 0
		if sub == "update" {
			want = 1
		}
		if len(rest) != want {
			return ErrUsage
		}
		r, close, err := reader()
		if err != nil {
			return err
		}
		defer close()
		metadata, err := applications.DecodeMetadata(r)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
		body = metadata
		method = http.MethodPost
		if sub == "update" {
			method = http.MethodPut
		}
	case "list", "export":
		if len(rest) != 0 {
			return ErrUsage
		}
		if sub == "export" {
			path += "/export"
		}
	case "get", "delete", "manifest-delete", "history", "history-export", "download":
		if len(rest) != 1 {
			return ErrUsage
		}
		switch sub {
		case "delete":
			method = http.MethodDelete
		case "manifest-delete":
			method = http.MethodDelete
			path += "/manifest"
		case "history":
			path += "/history"
		case "history-export":
			path += "/history/export"
		case "download":
			if !applications.ValidID(*content) {
				return fmt.Errorf("%w: -content-revision is required", ErrUsage)
			}
			path += "/revisions/" + *content + "/content"
		}
	case "upload", "manifest-set":
		if len(rest) != 1 {
			return ErrUsage
		}
		if sub == "upload" && !applications.ValidID(*backend) {
			return fmt.Errorf("%w: -backend is required", ErrUsage)
		}
		if err := (applications.Digests{SHA256: *digest}).Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
		r, close, err := reader()
		if err != nil {
			return err
		}
		defer close()
		body = r
		method = http.MethodPost
		headers.Set("Content-Type", "application/octet-stream")
		if sub == "upload" {
			path += "/upload"
			q.Set("backend", *backend)
			if *digest != "" {
				q.Set("sha256", *digest)
			}
		} else {
			path += "/manifest"
			if *filename == "" && *file != "-" {
				*filename = filepath.Base(*file)
			}
			if *filename == "" {
				return fmt.Errorf("%w: -filename is required for a stdin manifest", ErrUsage)
			}
			q.Set("filename", *filename)
		}
	case "import":
		if len(rest) != 1 || !applications.ValidID(*backend) || (*kind != "file" && *kind != "https") || *source == "" {
			return ErrUsage
		}
		method = http.MethodPost
		path += "/source"
		body = map[string]string{"kind": *kind, "location": *source, "backend": *backend}
	case "note":
		if len(rest) != 1 || strings.TrimSpace(*note) == "" {
			return ErrUsage
		}
		method = http.MethodPost
		path += "/history"
		body = map[string]string{"note": *note}
	case "deliver":
		if len(rest) != 2 || rest[1] == "" || !applications.ValidID(*content) || (*delivery != "mdm" && *delivery != "ddm") || *ttl < 0 || *ttl > 7*24*time.Hour {
			return ErrUsage
		}
		path, q = enrollmentPath("device", rest[1], "", q)
		path += "/application-packages"
		method = http.MethodPost
		body = map[string]any{"packageId": rest[0], "contentRevision": *content, "method": *delivery, "ttlSeconds": int64(ttl.Seconds())}
	case "revoke":
		if len(rest) != 2 || rest[1] == "" {
			return ErrUsage
		}
		path, q = enrollmentPath("device", rest[1], "", q)
		path += "/application-packages/grants/" + rest[0]
		method = http.MethodDelete
	default:
		return fmt.Errorf("%w: unknown package operation %q", ErrUsage, sub)
	}
	exporting := sub == "export" || sub == "history-export"
	if exporting {
		if *format != "json" && *format != "csv" {
			return ErrUsage
		}
		q.Set("format", *format)
		if *fields != "" {
			q.Set("fields", *fields)
		}
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	if sub == "download" {
		return c.Download(ctx, path, q, e.stdout)
	}
	if sub == "history" {
		return e.list(ctx, c, path, q, []string{"SEQUENCE", "ACTION", "REVISION"}, func(row jsontext.Value) []string {
			return []string{field(row, "sequence"), field(row, "action"), field(row, "revision")}
		})
	}
	if sub == "list" {
		return e.list(ctx, c, path, q, []string{"ID", "REVISION", "STATUS"}, func(row jsontext.Value) []string {
			return []string{field(row, "id"), field(row, "revision"), field(row, "cloudTransferStatus")}
		})
	}
	response, err := c.DoWithHeaders(ctx, method, path, q, body, headers)
	if err != nil {
		return err
	}
	if exporting {
		if _, err = e.stdout.Write(response.Body); err != nil {
			return err
		}
		if next := response.Headers.Get("X-Next-Cursor"); next != "" {
			_, err = fmt.Fprintln(e.stderr, "next cursor:", next)
		}
		return err
	}
	if response.Status == http.StatusNoContent {
		return nil
	}
	if sub == "deliver" && *delivery == "mdm" {
		// Enqueue and wake use separate permissions, matching the existing MDM API.
		wakePath, wakeQuery := enrollmentPath("device", rest[1], "", nil)
		wake, err := c.Do(ctx, http.MethodPost, wakePath+"/push", wakeQuery, nil)
		if err != nil {
			return fmt.Errorf("package queued; APNs wake failed: %w", err)
		}
		var outcome struct {
			Sent    bool   `json:"Sent"`
			Outcome string `json:"Outcome"`
		}
		if err = json.Unmarshal(wake.Body, &outcome); err != nil {
			return fmt.Errorf("package queued; invalid APNs response: %w", err)
		}
		if !outcome.Sent {
			return fmt.Errorf("package queued; APNs wake was not accepted: %s", outcome.Outcome)
		}
	}
	return e.emit(response, nil)
}
