package applications

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// HTTPSSource restricts downloads to explicitly allowed HTTPS authorities
// (hostname, optionally followed by :port). The client supplies TLS trust,
// transport-level egress restrictions and a deadline. Its default transport must
// not attach credentials; use a request-specific signed URL when necessary.
// The allowlist is an origin restriction, not protection against DNS rebinding;
// enforce network destination policy in the supplied transport when required.
type HTTPSSource struct {
	Client       *http.Client
	AllowedHosts []string
}

// Import downloads and verifies a package before associating its content with the
// record. Redirects are limited to five, remain HTTPS and must also be allowlisted.
// Signed URL queries are stripped from stored provenance. Non-200 responses are
// rejected without including their body or signed URL in the error.
func (s HTTPSSource) Import(ctx context.Context, m *Manager, id, revision, backend, location string) (Record, error) {
	if m == nil || s.Client == nil || len(s.AllowedHosts) == 0 {
		return Record{}, ErrInvalid
	}
	allowed := map[string]bool{}
	for _, host := range s.AllowedHosts {
		if host == "" || strings.ContainsAny(host, "/\\@?#\r\n") {
			return Record{}, invalidField("allowedHosts")
		}
		allowed[strings.ToLower(host)] = true
	}
	check := func(raw string) error {
		if err := validateHTTPS(raw); err != nil {
			return err
		}
		u, err := url.Parse(raw)
		if err != nil {
			return ErrInvalid
		}
		if !allowed[strings.ToLower(u.Host)] {
			return fmt.Errorf("%w: source host is not allowed", ErrInvalid)
		}
		return nil
	}
	if err := check(location); err != nil {
		return Record{}, err
	}
	client := *s.Client
	priorRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("%w: too many source redirects", ErrInvalid)
		}
		if err := check(req.URL.String()); err != nil {
			return err
		}
		if priorRedirect != nil {
			return priorRedirect(req, via)
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return Record{}, ErrInvalid
	}
	response, err := client.Do(request)
	if err != nil {
		// url.Error includes the query of a signed URL in Error(). Preserve the cause
		// without leaking that URL into command output or operational logs.
		var e *url.Error
		if errors.As(err, &e) {
			return Record{}, fmt.Errorf("download package source: %w", e.Err)
		}
		return Record{}, fmt.Errorf("download package source: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Record{}, fmt.Errorf("%w: source HTTP status %d", ErrInvalid, response.StatusCode)
	}
	if response.ContentLength > m.cfg.MaxBytes {
		return Record{}, ErrTooLarge
	}
	return m.Upload(ctx, id, revision, backend, Source{Kind: "https", Location: location}, response.Body, "")
}
