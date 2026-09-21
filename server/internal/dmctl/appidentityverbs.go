package dmctl

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const appIdentityPath = "/authoring/app-identities"

// runAppIdentities parses and executes the app identities subcommand, reporting argument
// and operation failures to the CLI caller.
func runAppIdentities(ctx context.Context, e *env, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "inspect":
			return runArtifactInspection(ctx, e, args[1:])
		case "public-app-store", "apple":
			return runIdentityCatalogue(ctx, e, args[0], args[1:])
		}
	}
	fs := e.verbFlags("app-identities public-app-store|apple search|lookup; app-identities inspect -file PATH")
	if _, err := e.parseVerb(fs, args); err != nil {
		return err
	}
	return fmt.Errorf("%w: app-identities needs public-app-store, apple or inspect", ErrUsage)
}

// runIdentityCatalogue parses and executes the identity catalogue subcommand, reporting
// argument and operation failures to the CLI caller.
func runIdentityCatalogue(ctx context.Context, e *env, source string, args []string) error {
	fs := e.verbFlags("app-identities " + source + " search|lookup")
	term := fs.String("term", "", "application name to search for")
	var country, entity, developer string
	if source == "public-app-store" {
		fs.StringVar(&country, "country", "", "required two-letter storefront country")
		fs.StringVar(&entity, "entity", "", "required entity: software, iPadSoftware or macSoftware")
		fs.StringVar(&developer, "developer", "", "filter search results by developer name")
	}
	rest, err := e.parseVerb(fs, args)
	if err != nil {
		return err
	}
	if len(rest) == 0 || e.opts.all {
		return fmt.Errorf("%w: use search or lookup; discovery has no cursor pagination", ErrUsage)
	}
	q := url.Values{}
	path := appIdentityPath + "/" + source
	if source == "public-app-store" {
		country = strings.ToUpper(country)
		if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
			return fmt.Errorf("%w: -country must be a two-letter storefront", ErrUsage)
		}
		if entity != "software" && entity != "iPadSoftware" && entity != "macSoftware" {
			return fmt.Errorf("%w: -entity must be software, iPadSoftware or macSoftware", ErrUsage)
		}
		q.Set("country", country)
		q.Set("entity", entity)
	}
	switch rest[0] {
	case "search":
		if len(rest) != 1 || (source == "public-app-store" && strings.TrimSpace(*term) == "") {
			return fmt.Errorf("%w: search takes -term; public App Store searches require a nonempty term", ErrUsage)
		}
		q.Set("term", *term)
		if source == "public-app-store" {
			if e.opts.limit < 0 || e.opts.limit > 200 {
				return fmt.Errorf("%w: search -limit must be 1..200, or 0 for the server default", ErrUsage)
			}
			if e.opts.limit != 0 {
				q.Set("limit", strconv.Itoa(e.opts.limit))
			}
			if developer != "" {
				q.Set("developer", developer)
			}
		}
	case "lookup":
		if len(rest) != 2 || strings.TrimSpace(rest[1]) == "" || *term != "" || developer != "" {
			return fmt.Errorf("%w: lookup takes one App Store ID or Apple bundle ID, without search filters", ErrUsage)
		}
		if source == "public-app-store" {
			id, err := strconv.ParseInt(rest[1], 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("%w: lookup needs a positive numeric App Store ID", ErrUsage)
			}
		}
		path += "/" + url.PathEscape(rest[1])
	default:
		return fmt.Errorf("%w: unknown identity operation %q", ErrUsage, rest[0])
	}
	if e.opts.limit != 0 && (source != "public-app-store" || rest[0] != "search") {
		return fmt.Errorf("%w: -limit applies only to public App Store search", ErrUsage)
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return err
	}
	return e.emit(resp, nil)
}

// runArtifactInspection parses and executes the artifact inspection subcommand, reporting
// argument and operation failures to the CLI caller.
func runArtifactInspection(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("app-identities inspect")
	file := fs.String("file", "", "required artifact path, or - to stream stdin")
	rest, err := e.parseVerb(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 || *file == "" {
		return fmt.Errorf("%w: inspect requires -file PATH or -file -", ErrUsage)
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	reader := e.stdin
	if *file != "-" {
		f, err := os.Open(*file) // #nosec G304 -- explicit operator-selected artifact, streamed without execution.
		if err != nil {
			return fmt.Errorf("dmctl: open artifact: %w", err)
		}
		defer func() { _ = f.Close() }()
		reader = f
	}
	resp, err := c.DoWithHeaders(ctx, http.MethodPost, appIdentityPath+"/artifacts", nil, reader, http.Header{"Content-Type": {"application/octet-stream"}})
	if err != nil {
		return err
	}
	return e.emit(resp, nil)
}
