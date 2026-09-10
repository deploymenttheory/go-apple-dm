package httpapi

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/plist"
)

// CertFromTLS takes the device certificate from the TLS peer certificates
// (mutual TLS terminated by this process). Requests without one pass
// through unchanged; the service decides whether that is acceptable.
func CertFromTLS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			if len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) == 0 ||
				!bytes.Equal(r.TLS.PeerCertificates[0].Raw, r.TLS.VerifiedChains[0][0].Raw) {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			var ok bool
			r, ok = verifiedIdentity(w, r, r.TLS.PeerCertificates[0])
			if !ok {
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// WithHeaderPeers permits assertions only from these socket peer networks.
// X-Forwarded-For and similar headers do not establish proxy trust.
func WithHeaderPeers(peers ...netip.Prefix) HeaderOption {
	return func(c *headerConfig) { c.peers = append([]netip.Prefix(nil), peers...) }
}

// HeaderOption configures CertFromHeader.
type HeaderOption func(*headerConfig)

type headerConfig struct {
	roots *x509.CertPool
	peers []netip.Prefix
}

// WithHeaderRoots verifies the header certificate chains to roots before it
// becomes the device identity.
//
// A header carries no proof of possession, so on its own it is a statement by
// whatever sent the request. A device certificate is not secret: it travels to
// this server on every check-in and appears in the SCEP CertRep, so anyone who
// reaches the listener past the proxy can present another device's. Checking
// the chain narrows that to holders of a certificate the enrollment CA issued,
// and does not replace the proxy's responsibility to prove possession.
func WithHeaderRoots(roots *x509.CertPool) HeaderOption {
	return func(c *headerConfig) { c.roots = roots }
}

// CertFromHeader takes the device certificate from a header set by a TLS
// terminating proxy. Two encodings are accepted: RFC 9440 (":<base64 DER>:")
// and URL-escaped PEM, as nginx and Apache produce. A malformed header, or one
// that fails WithHeaderRoots, is rejected with 400; an absent header passes
// through.
//
// Both WithHeaderRoots and WithHeaderPeers are required to accept an assertion.
func CertFromHeader(name string, opts ...HeaderOption) func(http.Handler) http.Handler {
	var cfg headerConfig
	for _, o := range opts {
		o(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(r.Header.Values(name)) > 1 {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			v := r.Header.Get(name)
			if v == "" {
				next.ServeHTTP(w, r)
				return
			}
			peer, err := netip.ParseAddrPort(r.RemoteAddr)
			trusted := false
			if err == nil {
				for _, prefix := range cfg.peers {
					if prefix.Contains(peer.Addr().Unmap()) {
						trusted = true
						break
					}
				}
			}
			if !trusted || cfg.roots == nil {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			cert, err := parseHeaderCert(v)
			if err == nil && cfg.roots != nil {
				_, err = cert.Verify(x509.VerifyOptions{
					Roots:     cfg.roots,
					KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				})
			}
			if err != nil {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			verified, ok := verifiedIdentity(w, r, cert)
			if !ok {
				return
			}
			next.ServeHTTP(w, verified)
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
			if len(r.Header.Values(cms.HeaderName)) > 1 {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
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
			verified, ok := verifiedIdentity(w, r, cert)
			if !ok {
				return
			}
			next.ServeHTTP(w, verified)
		})
	}
}

func verifiedIdentity(
	w http.ResponseWriter,
	r *http.Request,
	cert *x509.Certificate,
) (*http.Request, bool) {
	if existing := CertFromContext(
		r.Context(),
	); existing != nil &&
		!bytes.Equal(existing.Raw, cert.Raw) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return r, false
	}
	return r.WithContext(WithCert(r.Context(), cert)), true
}
