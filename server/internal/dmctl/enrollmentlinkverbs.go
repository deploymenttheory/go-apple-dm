package dmctl

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"text/tabwriter"
)

// runEnrollmentLinks parses and executes the enrollment-links subcommands: create issues a
// single-use link and prints its token-bearing URL once, list pages retained link
// metadata and revoke prevents an unredeemed link from issuing a profile.
func runEnrollmentLinks(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: enrollment-links needs create, list or revoke", ErrUsage)
	}
	verb := args[0]
	fs := e.verbFlags("enrollment-links " + verb)
	device := fs.String("device-id", "", "device UDID the profile is issued for (create)")
	serial := fs.String("serial", "", "device serial number (create)")
	product := fs.String("product", "", "product name, for example Mac or iPhone (create)")
	osVersion := fs.String("os-version", "", "device OS version, for example 26.6 (create)")
	identity := fs.String("identity", "", "identity method: scep or acme; empty uses the server default (create)")
	rights := fs.Int("access-rights", 0, "MDM AccessRights bitmask; zero uses the server default (create)")
	scope := fs.String("scope", "", "profile scope: System or User (create)")
	ttl := fs.String("ttl", "", "link lifetime, for example 30m; empty uses the server default (create)")
	id := fs.String("id", "", "link identifier (revoke)")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("%w: unexpected enrollment-links arguments", ErrUsage)
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	switch verb {
	case "create":
		if *device == "" {
			return fmt.Errorf("%w: create requires -device-id", ErrUsage)
		}
		body, err := json.Marshal(map[string]any{
			"DeviceID": *device, "Serial": *serial, "Product": *product, "OSVersion": *osVersion,
			"Identity": *identity, "AccessRights": *rights, "Scope": *scope, "TTL": *ttl,
		})
		if err != nil {
			return err
		}
		resp, err := c.Do(ctx, http.MethodPost, "/enrollment-links", nil, body)
		if err != nil {
			return e.explainNotFound(ctx, c, "enrollment", err)
		}
		return e.emit(resp, func(w *tabwriter.Writer) {
			item := jsontext.Value(resp.Body)
			_, _ = fmt.Fprintf(w, "URL\t%s\nID\t%s\nEXPIRES\t%s\n",
				field(item, "URL"), field(item, "ID"), field(item, "ExpiresAt"))
		})
	case "list":
		return e.explainNotFound(ctx, c, "enrollment", e.list(ctx, c, "/enrollment-links", url.Values{},
			[]string{"ID", "STATE", "DEVICE", "IDENTITY", "EXPIRES", "REDEEMED"},
			func(item jsontext.Value) []string {
				return []string{
					field(item, "ID"), field(item, "State"), field(item, "DeviceID"),
					field(item, "Identity"), field(item, "ExpiresAt"), field(item, "RedeemedAt"),
				}
			}))
	case "revoke":
		if *id == "" {
			return fmt.Errorf("%w: revoke requires -id", ErrUsage)
		}
		resp, err := c.Do(ctx, http.MethodDelete, "/enrollment-links/"+url.PathEscape(*id), nil, nil)
		if err != nil {
			return e.explainNotFound(ctx, c, "enrollment", err)
		}
		return e.emit(resp, func(w *tabwriter.Writer) {
			item := jsontext.Value(resp.Body)
			_, _ = fmt.Fprintf(w, "ID\t%s\nSTATE\t%s\n", field(item, "ID"), field(item, "State"))
		})
	default:
		return fmt.Errorf("%w: unknown enrollment-links operation", ErrUsage)
	}
}
