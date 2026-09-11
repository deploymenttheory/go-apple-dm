// Package httpsurl validates credential-bearing Apple service endpoints.
package httpsurl

import (
	"errors"
	"net/url"
)

// ErrURL deliberately excludes the input, which may contain credentials.
var ErrURL = errors.New(
	"endpoint must be an absolute HTTPS URL without user information or a fragment",
)

// Parse validates a server endpoint before credentials or device data are sent.
func Parse(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Fragment != "" ||
		u.Opaque != "" {
		return nil, ErrURL
	}
	return u, nil
}
