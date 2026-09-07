package dmctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
)

func runCertificates(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: certificates needs status, import, or revoke", ErrUsage)
	}
	fs := e.verbFlags("certificates " + args[0])
	file := fs.String("file", "", "PEM or DER certificate to import")
	reason := fs.Int("reason", 0, "RFC 5280 irreversible revocation reason")
	if err := fs.Parse(reorder(fs, args[1:])); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	rest := fs.Args()
	client, err := e.client()
	if err != nil {
		return err
	}
	var method, path string
	var body any
	switch args[0] {
	case "import":
		if len(rest) != 1 || *file == "" {
			return fmt.Errorf("%w: certificates import -file cert.pem ISSUER", ErrUsage)
		}
		data, err := e.readSource(*file)
		if err != nil {
			return err
		}
		method, path, body = http.MethodPost, "/pki/certificates/import", map[string]any{
			"issuer":      rest[0],
			"certificate": []byte(data),
		}
	case "status", "revoke":
		if len(rest) != 2 {
			return fmt.Errorf("%w: certificates %s ISSUER HEX-SERIAL", ErrUsage, args[0])
		}
		method, path = http.MethodGet, "/pki/certificates/"+url.PathEscape(
			rest[0],
		)+"/"+url.PathEscape(
			rest[1],
		)
		if args[0] == "revoke" {
			method = http.MethodPost
			path += "/revoke"
			body = map[string]any{"reason": *reason}
		}
	default:
		return fmt.Errorf("%w: unknown certificates command", ErrUsage)
	}
	resp, err := client.Do(ctx, method, path, nil, body)
	if err != nil {
		return e.explainNotFound(ctx, client, "pki", err)
	}
	return e.emit(resp, nil)
}
