package dmctl

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/adminclient"
	"github.com/deploymenttheory/go-apple-dm/server/internal/privatefile"
)

// depTokenLimit matches the server's administrative body limit for a portal
// server token.
const depTokenLimit = 1 << 20

// depHelp documents server-token registration and account-scoped Automated
// Device Enrollment operations.
const depHelp = `Automated Device Enrollment accounts

  dmctl dep accounts list
  dmctl dep accounts keypair ACCOUNT [-out public.pem]
  dmctl dep accounts import-token ACCOUNT -file token.p7m|- [-force]
  dmctl dep accounts devices|profiles|targets|sync ACCOUNT
  dmctl dep accounts define-profile ACCOUNT -file profile.json [-default]
  dmctl dep accounts default-profile ACCOUNT PROFILE_UUID
  dmctl dep accounts clear-default ACCOUNT
  dmctl dep accounts target ACCOUNT SERIAL PROFILE_UUID
  dmctl dep accounts clear-target ACCOUNT SERIAL

Register this server with Apple Business Manager or Apple School Manager in
two steps. keypair stages a token keypair and returns the public certificate
to upload when adding the MDM server; import-token sends the downloaded .p7m,
which the server decrypts and validates with Apple before storing. Running
keypair again replaces the staged keypair, so a token issued for the earlier
certificate can no longer be imported. -out never replaces an existing file
and is checked before the keypair is staged.

-force permits an intentional consumer-key or Apple server identity change.
sync fetches devices, then assigns desired profiles once. Clearing a default or
target changes local desired state only; no command disowns devices or clears
Apple profile assignments. Credentials are sensitive: keypair and import-token
need manageDEPCredentials.

`

// runDEP dispatches Automated Device Enrollment account administration.
func runDEP(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("dep")
	fs.Usage = func() { _, _ = fmt.Fprint(e.stderr, depHelp); fs.PrintDefaults() }
	file := fs.String("file", "", "import-token .p7m or define-profile JSON file, or - for stdin")
	out := fs.String("out", "", "keypair: write the certificate to this new file instead of stdout")
	force := fs.Bool("force", false, "import-token: permit an Apple server identity change")
	chooseDefault := fs.Bool("default", false, "define-profile: also select the profile as the account default")
	pos, err := e.parseVerb(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 || pos[0] != "accounts" {
		return usagef("dep takes accounts and an operation, for example: dmctl dep accounts list")
	}
	op, operands := pos[1], pos[2:]
	want, ok := depArity[op]
	if !ok {
		return usagef("dep accounts has no %q operation; see dmctl dep -h", op)
	}
	if len(operands) != want || slices.Contains(operands, "") {
		return usagef("dep accounts %s takes %d argument(s); see dmctl dep -h", op, want)
	}
	for _, f := range []struct {
		name    string
		applies bool
	}{
		{"-out", *out == "" || op == "keypair"},
		{"-force", !*force || op == "import-token"},
		{"-default", !*chooseDefault || op == "define-profile"},
		{"-file", *file == "" || op == "import-token" || op == "define-profile"},
	} {
		if !f.applies {
			return usagef("%s does not apply to dep accounts %s; see dmctl dep -h", f.name, op)
		}
	}

	path := "/dep/accounts"
	if want > 0 {
		path += "/" + url.PathEscape(operands[0])
	}
	var body []byte
	switch op {
	case "import-token":
		if body, err = e.readDEPInput(*file, "import-token needs -file token.p7m, or -file - for stdin"); err != nil {
			return err
		}
	case "define-profile":
		if body, err = e.readDEPInput(*file, "define-profile needs -file profile.json, or -file - for stdin"); err != nil {
			return err
		}
		if !jsontext.Value(body).IsValid() {
			return usagef("define-profile input is not valid JSON")
		}
	}

	// The output file is created before the keypair is staged, so an unusable
	// path never strands a certificate the operator did not receive.
	var certFile *os.File
	if op == "keypair" && *out != "" {
		certFile, err = privatefile.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- explicit operator output path
		if err != nil {
			return fmt.Errorf("create %s: %w", *out, err)
		}
	}

	c, err := e.client()
	if err == nil {
		err = e.depOperation(ctx, c, op, path, operands, body, depQuery(*force, *chooseDefault), certFile)
	}
	if certFile != nil {
		closeErr := certFile.Close()
		if err != nil {
			_ = os.Remove(*out)
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", *out, closeErr)
		}
	}
	return err
}

// depArity is the number of positional arguments each operation takes.
var depArity = map[string]int{
	"list": 0, "keypair": 1, "import-token": 1, "devices": 1, "profiles": 1, "targets": 1,
	"sync": 1, "define-profile": 1, "default-profile": 2, "clear-default": 1, "target": 3,
	"clear-target": 2,
}

// depQuery carries the explicit identity-replacement and default-selection choices.
func depQuery(force, chooseDefault bool) url.Values {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	if chooseDefault {
		q.Set("default", "true")
	}
	return q
}

// readDEPInput reads a required token or profile file, bounded by the server's limit.
func (e *env) readDEPInput(file, missing string) ([]byte, error) {
	if file == "" {
		return nil, usagef("%s", missing)
	}
	var src io.Reader = e.stdin
	if file != "-" {
		f, err := os.Open(file) // #nosec G304 -- an operator's own file
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		defer func() { _ = f.Close() }()
		src = f
	}
	b, err := io.ReadAll(io.LimitReader(src, depTokenLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	if len(b) == 0 || len(b) > depTokenLimit {
		return nil, usagef("%s must contain between 1 byte and 1 MiB", file)
	}
	return b, nil
}

// depOperation sends one account operation and renders its response.
func (e *env) depOperation(
	ctx context.Context,
	c *adminclient.Client,
	op, path string,
	operands []string,
	body []byte,
	q url.Values,
	certFile *os.File,
) error {
	var resp *adminclient.Response
	var err error
	switch op {
	case "list":
		err = e.list(ctx, c, path, nil,
			[]string{"NAME", "ORGANIZATION", "SERVER UUID", "TOKENS", "TOKEN EXPIRES", "STATE"},
			func(item jsontext.Value) []string {
				return []string{
					field(item, "Name"), dash(field(item, "OrgName")), dash(field(item, "ServerUUID")),
					field(item, "HasTokens"), dash(field(item, "AccessTokenExpiry")), depState(item),
				}
			})
	case "devices":
		err = e.list(ctx, c, path+"/devices", nil,
			[]string{"SERIAL", "MODEL", "FAMILY", "OS", "PROFILE STATUS", "PROFILE UUID"},
			func(item jsontext.Value) []string {
				return []string{
					field(item, "serial_number"), dash(field(item, "model")), dash(field(item, "device_family")),
					dash(field(item, "os")), dash(field(item, "profile_status")), dash(field(item, "profile_uuid")),
				}
			})
	case "profiles":
		err = e.list(ctx, c, path+"/profiles", nil,
			[]string{"PROFILE UUID", "NAME", "URL"},
			func(item jsontext.Value) []string {
				return []string{dash(field(item, "profile_uuid")), dash(field(item, "profile_name")), dash(field(item, "url"))}
			})
	case "keypair":
		if resp, err = c.Do(ctx, http.MethodPut, path+"/keypair", nil, nil); err == nil {
			w := io.Writer(e.stdout)
			if certFile != nil {
				w = certFile
			}
			if _, err = w.Write(resp.Body); err == nil && certFile != nil {
				err = certFile.Sync()
			}
		}
	case "import-token":
		resp, err = c.DoWithHeaders(ctx, http.MethodPut, path+"/token", q, body,
			http.Header{"Content-Type": {"application/pkcs7-mime"}})
		if err == nil {
			err = e.emit(resp, func(w *tabwriter.Writer) { depAccountDetail(w, resp.Body) })
		}
	case "targets":
		if resp, err = c.Do(ctx, http.MethodGet, path+"/targets", nil, nil); err == nil {
			err = e.emit(resp, func(w *tabwriter.Writer) { depTargets(w, resp.Body) })
		}
	case "sync":
		if resp, err = c.Do(ctx, http.MethodPost, path+"/sync", nil, nil); err == nil {
			err = e.emit(resp, nil)
		}
	case "define-profile":
		if resp, err = c.Do(ctx, http.MethodPut, path+"/profile", q, body); err == nil {
			err = e.emit(resp, nil)
		}
	default:
		method, target, input := depChoice(op, path, operands)
		if resp, err = c.Do(ctx, method, target, nil, input); err == nil {
			err = e.emit(resp, nil)
		}
	}
	return e.explainNotFound(ctx, c, "dep", err)
}

// depChoice addresses the local default and serial target operations.
func depChoice(op, path string, operands []string) (string, string, any) {
	switch op {
	case "default-profile":
		return http.MethodPut, path + "/default-profile", map[string]string{"profile_uuid": operands[1]}
	case "clear-default":
		return http.MethodDelete, path + "/default-profile", nil
	case "target":
		return http.MethodPut, path + "/targets/" + url.PathEscape(operands[1]), map[string]string{"profile_uuid": operands[2]}
	default:
		return http.MethodDelete, path + "/targets/" + url.PathEscape(operands[1]), nil
	}
}

// depState summarizes the Apple-reported credential state of a listed account.
func depState(item jsontext.Value) string {
	var row struct {
		State struct{ TermsExpired, TokenInvalid bool }
	}
	if json.Unmarshal(item, &row) != nil {
		return "-"
	}
	switch {
	case row.State.TokenInvalid:
		return "token-invalid"
	case row.State.TermsExpired:
		return "terms-expired"
	default:
		return "ok"
	}
}

// depAccountDetail renders the Apple account identity that a token import
// validated. Contact fields remain in the JSON output.
func depAccountDetail(w io.Writer, body []byte) {
	item := jsontext.Value(body)
	for _, row := range [][2]string{
		{"Server", "server_name"},
		{"Server UUID", "server_uuid"},
		{"Organization", "org_name"},
		{"Organization ID", "org_id"},
		{"Organization type", "org_type"},
		{"Organization version", "org_version"},
	} {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", row[0], dash(field(item, row[1])))
	}
}

// depTargets renders the account default, explicit serial choices and revision.
func depTargets(w io.Writer, body []byte) {
	var t struct {
		Default  string
		Serials  map[string]string
		Revision int64
	}
	if err := json.Unmarshal(body, &t); err != nil {
		_, _ = w.Write(body)
		return
	}
	_, _ = fmt.Fprintf(w, "Default\t%s\nRevision\t%d\n", dash(t.Default), t.Revision)
	if len(t.Serials) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "SERIAL\tPROFILE UUID")
	for _, serial := range slices.Sorted(maps.Keys(t.Serials)) {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", serial, t.Serials[serial])
	}
}
