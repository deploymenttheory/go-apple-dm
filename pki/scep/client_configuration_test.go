package scep_test

import (
	"crypto/x509"
	"errors"
	"net/http"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/pki/scep"
)

func TestClientRejectsInvalidTrustAndRenewalBeforeSending(t *testing.T) {
	f := newFixture(t)
	key := rsaKey(t)
	for _, mode := range []string{"nil-recipient", "untrusted-recipient", "missing-renewal-key", "missing-renewal-certificate"} {
		t.Run(mode, func(t *testing.T) {
			options := scep.EnrollOptions{Recipients: []*x509.Certificate{f.caCert}}
			switch mode {
			case "nil-recipient":
				options.Recipients = []*x509.Certificate{nil}
			case "untrusted-recipient":
				options.Roots = x509.NewCertPool()
			case "missing-renewal-key":
				options.Renew = &scep.Identity{Cert: f.caCert}
			case "missing-renewal-certificate":
				options.Renew = &scep.Identity{Key: key}
			}
			transport := &unexpectedSCEPTransport{}
			client := scep.NewClient(
				"https://scep.example.test",
				&http.Client{Transport: transport},
			)
			cert, err := client.Enroll(t.Context(), key, options)
			if !errors.Is(err, scep.ErrClient) || cert != nil || transport.called {
				t.Fatalf(
					"invalid configuration: cert=%v err=%v request sent=%v",
					cert,
					err,
					transport.called,
				)
			}
		})
	}
}

type unexpectedSCEPTransport struct{ called bool }

func (t *unexpectedSCEPTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.called = true
	return nil, errors.New("unexpected SCEP request")
}
