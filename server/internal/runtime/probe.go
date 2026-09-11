package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// ErrProbe identifies invalid probe configuration or an unhealthy endpoint.
var ErrProbe = errors.New("health probe failed")

// ProbeConfig selects an explicit URL or the process configuration with URL=auto.
// Automatic TLS checks trust and pin the configured certificate, independently
// of public DNS or the device-identity CA. CAFile is for explicit URL probes.
type ProbeConfig struct {
	URL         string
	Listen      string
	TLSCertFile string
	TLSKeyFile  string
	CAFile      string
}

// Probe checks storage health with a two-second deadline, without redirects.
// Automatic probes do not use HTTP proxies. Certificate changes require the
// serving process to restart before its local certificate pin matches again.
func Probe(ctx context.Context, cfg ProbeConfig) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	address := cfg.URL
	if address == "auto" {
		transport.Proxy = nil
		var err error
		address, transport.TLSClientConfig, err = automaticProbe(cfg)
		if err != nil {
			return err
		}
	} else if cfg.CAFile != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("%w: system roots: %w", ErrProbe, err)
		}
		data, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return fmt.Errorf("%w: CA file: %w", ErrProbe, err)
		}
		if !roots.AppendCertsFromPEM(data) {
			return fmt.Errorf("%w: CA file has no certificates", ErrProbe)
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("%w: request: %w", ErrProbe, err)
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: request: %w", ErrProbe, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s", ErrProbe, resp.Status)
	}
	return nil
}

func automaticProbe(cfg ProbeConfig) (string, *tls.Config, error) {
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return "", nil, fmt.Errorf("%w: listener: %w", ErrProbe, err)
	}
	if port == "" || port == "0" {
		return "", nil, fmt.Errorf("%w: probe requires a fixed listener port", ErrProbe)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	address := net.JoinHostPort(host, port) + "/healthz"
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return "", nil, fmt.Errorf(
			"%w: TLS certificate and key must be configured together",
			ErrProbe,
		)
	}
	if cfg.TLSCertFile == "" {
		return "http://" + address, nil, nil
	}
	cert, err := probeCertificate(cfg.TLSCertFile)
	if err != nil {
		return "", nil, err
	}
	name, err := probeServerName(cert, host)
	if err != nil {
		return "", nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	config := &tls.Config{
		MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: name,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 ||
				!bytes.Equal(state.PeerCertificates[0].Raw, cert.Raw) {
				return fmt.Errorf(
					"%w: server certificate does not match configured certificate",
					ErrProbe,
				)
			}
			return nil
		},
	}
	return "https://" + address, config, nil
}

func probeCertificate(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- Operator TLS path; no remote input.
	if err != nil {
		return nil, fmt.Errorf("%w: TLS certificate: %w", ErrProbe, err)
	}
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: TLS certificate: %w", ErrProbe, err)
		}
		return cert, nil
	}
	return nil, fmt.Errorf("%w: TLS file has no certificates", ErrProbe)
}

func probeServerName(cert *x509.Certificate, host string) (string, error) {
	if cert.VerifyHostname(host) == nil {
		return host, nil
	}
	for _, name := range cert.DNSNames {
		if strings.HasPrefix(name, "*.") {
			name = "healthcheck" + name[1:]
		}
		if cert.VerifyHostname(name) == nil {
			return name, nil
		}
	}
	if len(cert.IPAddresses) > 0 {
		return cert.IPAddresses[0].String(), nil
	}
	return "", fmt.Errorf("%w: TLS certificate has no usable DNS or IP SAN", ErrProbe)
}
