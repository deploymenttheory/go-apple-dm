package dmctl

import (
	"context"
	"encoding/csv"
	json "encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appsettings"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/publicappstoreidentity"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/explain"
)

func runUtility(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		_, err := fmt.Fprintln(e.stderr, "Usage: dmctl utility <public-app-store-identity search|lookup | appidentity inspect | appsettings allow|deny|privacy> [flags]\nDiscovery supports -output human|json|ndjson|csv. App Settings writes payload JSON. No management-server credentials are required.")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return fmt.Errorf("%w: utility command required", ErrUsage)
		}
		return flag.ErrHelp
	}
	switch args[0] {
	case "public-app-store-identity":
		return runPublicAppStoreIdentity(ctx, e, args[1:])
	case "appidentity":
		return runAppIdentity(ctx, e, args[1:])
	case "appsettings":
		return runAppSettings(e, args[1:])
	default:
		return fmt.Errorf("%w: unknown utility %q", ErrUsage, args[0])
	}
}

func runPublicAppStoreIdentity(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 || (args[0] != "search" && args[0] != "lookup") {
		return fmt.Errorf("%w: Public App Store Identity needs search or lookup", ErrUsage)
	}
	action := args[0]
	fs := e.verbFlags("utility public-app-store-identity " + action)
	country := fs.String("country", "", "required two-letter storefront country")
	entity := fs.String("entity", "", "required software, iPadSoftware, or macSoftware entity")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 1 || *country == "" || *entity == "" || e.opts.all {
		return fmt.Errorf("%w: supply one search term or numeric ID, -country and -entity; -all is unavailable", ErrUsage)
	}
	if !utilityOutput(e.opts.output) {
		return fmt.Errorf("%w: discovery output must be human, json, ndjson, or csv", ErrUsage)
	}
	if e.opts.timeout <= 0 {
		return fmt.Errorf("%w: positive timeout required", ErrUsage)
	}
	client := publicappstoreidentity.Client{Timeout: e.opts.timeout}
	store := publicappstoreidentity.Store{Country: *country, Entity: publicappstoreidentity.Entity(*entity)}
	var apps []publicappstoreidentity.App
	if action == "search" {
		apps, err = client.Search(ctx, publicappstoreidentity.Query{Term: rest[0], Store: store, Limit: e.opts.limit})
	} else {
		var id int64
		id, err = strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			return fmt.Errorf("%w: positive numeric App Store ID required", ErrUsage)
		}
		var app publicappstoreidentity.App
		app, err = client.Lookup(ctx, id, store)
		apps = []publicappstoreidentity.App{app}
	}
	if errors.Is(err, publicappstoreidentity.ErrInvalid) {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(apps))
	records := make([]any, 0, len(apps))
	for _, a := range apps {
		rows = append(rows, []string{strconv.FormatInt(a.ID, 10), a.BundleID, a.Name, a.Developer, a.Version, a.Store.Country, string(a.Store.Entity), a.URL})
		records = append(records, a)
	}
	return emitUtility(e, apps, records, []string{"STORE ID", "BUNDLE ID", "NAME", "DEVELOPER", "VERSION", "COUNTRY", "ENTITY", "URL"}, rows)
}

func runAppIdentity(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 || args[0] != "inspect" {
		return fmt.Errorf("%w: appidentity needs inspect", ErrUsage)
	}
	fs := e.verbFlags("utility appidentity inspect")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 1 || e.opts.timeout <= 0 {
		return fmt.Errorf("%w: inspect requires one path and a positive timeout", ErrUsage)
	}
	if !utilityOutput(e.opts.output) {
		return fmt.Errorf("%w: discovery output must be human, json, ndjson, or csv", ErrUsage)
	}
	ctx, cancel := context.WithTimeout(ctx, e.opts.timeout)
	defer cancel()
	id, err := appidentity.Inspect(ctx, rest[0])
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(id.Architectures))
	for _, a := range id.Architectures {
		rows = append(rows, []string{id.Path, id.Executable, id.BundleID, id.Name, id.Version, a.Name, a.CDHash, a.SigningID, a.TeamID, string(a.Signature.Status), string(a.Signature.Category), a.DesignatedRequirement, a.Signature.Detail})
	}
	return emitUtility(e, id, []any{id}, []string{"PATH", "EXECUTABLE", "BUNDLE ID", "NAME", "VERSION", "ARCHITECTURE", "CDHASH", "SIGNING ID", "TEAM ID", "SIGNATURE", "CATEGORY", "DESIGNATED REQUIREMENT", "DETAIL"}, rows)
}

func runAppSettings(e *env, args []string) error {
	if len(args) == 0 || (args[0] != "allow" && args[0] != "deny" && args[0] != "privacy") {
		return fmt.Errorf("%w: appsettings needs allow, deny, or privacy", ErrUsage)
	}
	action := args[0]
	fs := e.verbFlags("utility appsettings " + action)
	targetText := fs.String("target", "", "required OS:version,channel=device|user and enrollment flags")
	var ids []string
	var identityFile, inputFile, match, prefix, state string
	var managed bool
	if action == "privacy" {
		fs.StringVar(&inputFile, "input", "", "JSON array of privacy entries, or - for stdin")
	} else {
		fs.Func("bundle-id", "bundle ID (repeat for multiple apps)", func(value string) error { ids = append(ids, value); return nil })
		fs.StringVar(&identityFile, "identity", "", "inspection JSON file, or - for stdin")
		fs.StringVar(&match, "match", "", "required with -identity: cdhash, app, team, or signing-id (deny only)")
		fs.StringVar(&prefix, "path-prefix", "", "additional literal absolute path prefix; include / for directory contents")
		fs.StringVar(&state, "signing-state", "", "additional Apple SigningState constraint")
		if action == "allow" {
			fs.BoolVar(&managed, "always-allow-managed-apps", false, "allow managed apps alongside binary allow rules")
		}
	}
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 0 || *targetText == "" {
		return fmt.Errorf("%w: appsettings requires -target and no positional arguments", ErrUsage)
	}
	if e.opts.output != outputHuman && e.opts.output != outputJSON && e.opts.output != outputNDJSON {
		return fmt.Errorf("%w: App Settings output is JSON", ErrUsage)
	}
	target, err := explain.ParseTarget(*targetText)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	var payload *ddm.AppSettings
	if action == "privacy" {
		var entries []appsettings.PrivacyEntry
		if err := readUtilityJSON(e, inputFile, &entries); err != nil {
			return err
		}
		payload, err = appsettings.PrivacyDefaults(entries, target)
	} else {
		options := appsettings.BinaryOptions{Match: appsettings.MatchMode(match), PathPrefix: prefix, SigningState: state}
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "always-allow-managed-apps" {
				options.AlwaysAllowManagedApps = new(managed)
			}
		})
		if (len(ids) > 0) == (identityFile != "") {
			return fmt.Errorf("%w: choose -bundle-id or -identity", ErrUsage)
		}
		if len(ids) > 0 {
			if match != "" || prefix != "" || state != "" || options.AlwaysAllowManagedApps != nil {
				return fmt.Errorf("%w: binary options require -identity", ErrUsage)
			}
			if action == "allow" {
				payload, err = appsettings.AllowApps(ids, target)
			} else {
				payload, err = appsettings.DenyApps(ids, target)
			}
		} else {
			var id appidentity.Identity
			if err := readUtilityJSON(e, identityFile, &id); err != nil {
				return err
			}
			if action == "allow" {
				payload, err = appsettings.AllowBinaries(id, options, target)
			} else {
				payload, err = appsettings.DenyBinaries(id, options, target)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	return writeUtilityJSON(e.stdout, payload)
}

const utilityInputLimit = 8 << 20

func readUtilityJSON(e *env, path string, value any) error {
	if path == "" {
		return fmt.Errorf("%w: input file or - required", ErrUsage)
	}
	reader := e.stdin
	if path != "-" {
		file, err := os.Open(path) // #nosec G304 -- the operator explicitly supplies a local CLI input file.
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, utilityInputLimit+1))
	if err != nil {
		return err
	}
	if len(data) > utilityInputLimit {
		return fmt.Errorf("%w: utility input exceeds 8 MiB", ErrUsage)
	}
	if err := json.Unmarshal(data, value, json.RejectUnknownMembers(true)); err != nil {
		return fmt.Errorf("%w: invalid utility JSON: %w", ErrUsage, err)
	}
	return nil
}

func utilityOutput(mode string) bool {
	return mode == outputHuman || mode == outputJSON || mode == outputNDJSON || mode == "csv"
}

func writeUtilityJSON(w io.Writer, value any) error {
	if err := json.MarshalWrite(w, value); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

func emitUtility(e *env, value any, records []any, header []string, rows [][]string) error {
	switch e.opts.output {
	case outputJSON:
		return writeUtilityJSON(e.stdout, value)
	case outputNDJSON:
		for _, record := range records {
			if err := writeUtilityJSON(e.stdout, record); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		w := csv.NewWriter(e.stdout)
		if err := w.Write(header); err != nil {
			return err
		}
		for _, row := range rows {
			if err := w.Write(row); err != nil {
				return err
			}
		}
		w.Flush()
		return w.Error()
	default:
		w := tabwriter.NewWriter(e.stdout, 0, 8, 2, ' ', 0)
		for _, row := range append([][]string{header}, rows...) {
			cells := make([]string, len(row))
			for i, cell := range row {
				cells[i] = strings.Map(func(r rune) rune {
					if unicode.IsControl(r) {
						return ' '
					}
					return r
				}, cell)
			}
			if _, err := fmt.Fprintln(w, strings.Join(cells, "\t")); err != nil {
				return err
			}
		}
		return w.Flush()
	}
}
