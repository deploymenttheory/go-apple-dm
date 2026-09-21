package dmctl

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

// runConfigurationProfiles parses and executes the configuration profiles subcommand,
// reporting argument and operation failures to the CLI caller.
func runConfigurationProfiles(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: configuration-profiles needs upload, list, get or download", ErrUsage)
	}
	sub := args[0]
	fs := e.verbFlags("configuration-profiles " + sub)
	file := fs.String("file", "-", "mobileconfig file, or - for stdin")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	path, method := "/configuration-profiles", http.MethodGet
	var body any
	switch sub {
	case "upload":
		if len(rest) != 0 {
			return ErrUsage
		}
		var reader io.Reader = e.stdin
		if *file != "-" {
			f, err := os.Open(*file)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			reader = f
		}
		b, err := io.ReadAll(io.LimitReader(reader, configurationprofile.MaxBytes+1))
		if err != nil {
			return err
		}
		if len(b) > configurationprofile.MaxBytes {
			return fmt.Errorf("%w: profile exceeds 4 MiB", ErrUsage)
		}
		body = b
		method = http.MethodPost
	case "list":
		if len(rest) != 0 {
			return ErrUsage
		}
		return e.list(ctx, c, path, nil, []string{"REVISION", "IDENTIFIER"}, func(row jsontext.Value) []string {
			return []string{field(row, "Revision"), field(row, "PayloadIdentifier")}
		})
	case "get", "download":
		if len(rest) != 1 || !configurationprofile.ValidRevision(rest[0]) {
			return fmt.Errorf("%w: profile revision required", ErrUsage)
		}
		path += "/" + rest[0]
		if sub == "download" {
			path += "/content"
		}
	default:
		return fmt.Errorf("%w: unknown profile operation", ErrUsage)
	}
	resp, err := c.DoWithHeaders(ctx, method, path, nil, body, http.Header{"Content-Type": {"application/x-apple-aspen-config"}})
	if err != nil {
		return err
	}
	if sub == "download" {
		_, err = e.stdout.Write(resp.Body)
		return err
	}
	return e.emit(resp, nil)
}
