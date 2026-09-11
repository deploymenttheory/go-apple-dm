package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
)

// outboundClient configures private HTTPS trust without weakening verification.
// An explicit client and a root file are mutually exclusive trust configurations.
func outboundClient(client *http.Client, rootFile string) (*http.Client, error) {
	if rootFile == "" {
		return client, nil
	}
	if client != nil {
		return nil, fmt.Errorf(
			"%w: configure either an HTTP client or an outbound root CA file",
			ErrConfig,
		)
	}
	certs, err := readCertsPEM(rootFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	for _, cert := range certs {
		roots.AddCert(cert)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{Transport: transport}, nil
}

// noStore applies to authentication failures as well as credential documents.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
