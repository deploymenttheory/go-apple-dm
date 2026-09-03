package httpapi

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/cms"
	"github.com/deploymenttheory/go-apple-dm/plist"
)

// CertFromTLS takes the device certificate from the TLS peer certificates
// (mutual TLS terminated by this process). Requests without one pass
// through unchanged; the service decides whether that is acceptable.
func CertFromTLS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			r = r.WithContext(WithCert(r.Context(), r.TLS.PeerCertificates[0]))
		}
		next.ServeHTTP(w, r)
	})
}

// CertFromHeader takes the device certificate from a header set by a TLS
// terminating proxy. Two encodings are accepted: RFC 9440 (":<base64 DER>:")
// and URL-escaped PEM, as nginx and Apache produce. A malformed header is
// rejected with 400; an absent header passes through.
func CertFromHeader(name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			v := r.Header.Get(name)
			if v == "" {
				next.ServeHTTP(w, r)
				return
			}
			cert, err := parseHeaderCert(v)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithCert(r.Context(), cert)))
		})
	}
}

func parseHeaderCert(v string) (*x509.Certificate, error) {
	if strings.HasPrefix(v, ":") && strings.HasSuffix(v, ":") && len(v) > 2 {
		der, err := base64.StdEncoding.DecodeString(v[1 : len(v)-1])
		if err != nil {
			return nil, fmt.Errorf("httpapi: certificate header: %w", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("httpapi: certificate header: %w", err)
		}
		return cert, nil
	}
	unescaped, err := url.PathUnescape(v)
	if err != nil {
		return nil, fmt.Errorf("httpapi: certificate header: %w", err)
	}
	block, _ := pem.Decode([]byte(unescaped))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("httpapi: certificate header: %w", errCertMissing)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("httpapi: certificate header: %w", err)
	}
	return cert, nil
}

// CertFromMdmSignature verifies the Mdm-Signature header over the request
// body and takes the signer as the device certificate. The body is read
// (bounded by maxBytes, 0 for the plist default) and handed on to the next
// handler. A missing header passes through; an invalid signature is 400.
func CertFromMdmSignature(o cms.VerifyOptions, maxBytes int64) func(http.Handler) http.Handler {
	if maxBytes == 0 {
		maxBytes = plist.DefaultMaxBytes
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get(cms.HeaderName)
			if header == "" {
				next.ServeHTTP(w, r)
				return
			}
			body, err := readBody(r, maxBytes)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			replaceBody(r, body)
			cert, err := cms.VerifyHeader(header, body, o)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithCert(r.Context(), cert)))
		})
	}
}
