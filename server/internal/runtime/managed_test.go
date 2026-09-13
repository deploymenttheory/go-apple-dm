package runtime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func managedRuntimeConfig(t *testing.T) (app.Config, *x509.CertPool) {
	t.Helper()
	path, err := app.InitSetupFile(
		app.SetupInitOptions{
			Directory: t.TempDir(),
			Role:      "vendor",
			Storage:   "sqlite",
			Listen:    "127.0.0.1:0",
			PublicURL: "https://localhost:8443",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.LoadSetupFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.OpenSetup(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	request := app.SetupRequest{
		Request: lifecycle.Request{
			Subject:  pkix.Name{CommonName: "localhost"},
			DNSNames: []string{"localhost", "127.0.0.1"},
		},
	}
	result, err := a.ExecuteSetup(t.Context(), lifecycle.HTTPS, "lab", request)
	if err != nil {
		t.Fatal(err)
	}
	request.Revision = result.Identity.Pending
	if _, err := a.ExecuteSetup(t.Context(), lifecycle.HTTPS, "activate", request); err != nil {
		t.Fatal(err)
	}
	material, err := a.Certificates.LoadMaterial(t.Context(), cfg.Setup.HTTPSCAID, "")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(material.Certificate) {
		t.Fatal("missing test CA")
	}
	return cfg, roots
}

func runtimeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func TestManagedRuntimeTLSHTTP01AndShutdown(t *testing.T) {
	cfg, roots := managedRuntimeConfig(t)
	cfg.Listen, cfg.Setup.HTTP01Listen = runtimeAddress(t), runtimeAddress(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, cfg) }()
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get("https://" + cfg.Listen + "/healthz")
		if err == nil {
			response.Body.Close()
			break
		}
		select {
		case err := <-done:
			t.Fatal("server exited before TLS handshake", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("managed TLS did not become available", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := client.Get(
		"http://" + cfg.Setup.HTTP01Listen + "/.well-known/acme-challenge/missing",
	)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("unissued challenge exposed", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("managed runtime did not drain")
	}
}

func TestManagedRuntimeRejectsMissingIdentityAndOccupiedListeners(t *testing.T) {
	cfg, _ := managedRuntimeConfig(t)
	validHTTPS := cfg.Setup.HTTPSID
	cfg.Setup.HTTPSID = "missing"
	if err := Serve(t.Context(), cfg); !errors.Is(err, lifecycle.ErrNotFound) {
		t.Fatal("missing active HTTPS accepted", err)
	}
	cfg.Setup.HTTPSID = validHTTPS
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg.Listen = listener.Addr().String()
	if err := Serve(t.Context(), cfg); err == nil {
		t.Fatal("occupied HTTPS listener accepted")
	}
	cfg.Listen, cfg.Setup.HTTP01Listen = "127.0.0.1:0", listener.Addr().String()
	if err := Serve(t.Context(), cfg); err == nil {
		t.Fatal("occupied HTTP-01 listener accepted")
	}
	if err := wrapError(nil); err != nil {
		t.Fatal(err)
	}
}
