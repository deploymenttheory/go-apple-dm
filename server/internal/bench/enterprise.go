package bench

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/simulator"
)

func (e *Environment) issuedDevice(certPath, keyPath string) (*simulator.Device, error) {
	pair, err := tls.LoadX509KeyPair(
		e.Workspace.path(certPath),
		e.Workspace.path(keyPath),
	)
	if err != nil {
		return nil, wrapError(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, wrapError(err)
	}
	serial, err := ca.Serial()
	if err != nil {
		return nil, wrapError(err)
	}
	id := "BENCH-" + randomID()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: id},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, pair.Leaf, key.Public(), pair.PrivateKey)
	if err != nil {
		return nil, wrapError(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, wrapError(err)
	}
	return simulator.New(
		id,
		simulator.WithClient(e.Client),
		simulator.WithURLs(e.URL+"/mdm", e.URL+"/mdm"),
		simulator.WithIdentity(&simulator.Identity{Cert: cert, Key: key}),
	), nil
}

func adeEnroll(ctx context.Context, e *Environment, _ string) error {
	d, err := e.factoryDevice()
	if err != nil {
		return wrapError(err)
	}
	return wrapError(d.ADEEnroll(
		ctx,
		e.URL+"/enroll/ade",
		simulator.ADEOptions{
			WebView: func(_ context.Context, r *http.Response) (*http.Response, error) { return r, nil },
		},
	))
}

func browser(ctx context.Context, e *Environment, raw string) (string, error) {
	c := *e.Client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
			return http.ErrUseLastResponse
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return "", wrapError(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", wrapError(err)
	}
	defer resp.Body.Close()
	return resp.Header.Get("Location"), nil
}

func accountDriven(ctx context.Context, e *Environment, _ string) error {
	for _, family := range []string{"Mac", "iPhone"} {
		d, err := e.factoryDevice()
		if err != nil {
			return wrapError(err)
		}
		result, err := d.AccountDrivenEnroll(
			ctx,
			simulator.AccountDrivenOptions{
				UserIdentifier: "user@example.com",
				ModelFamily:    family,
				DiscoveryURL:   e.URL,
				Authenticate: func(ctx context.Context, c simulator.AuthChallenge) (string, error) {
					if c.Method == "apple-oauth2" {
						return d.OAuth2CodeFlow(
							ctx,
							c,
							"user@example.com",
							func(ctx context.Context, u string) (string, error) { return browser(ctx, e, u) },
						)
					}
					location, err := browser(ctx, e, c.URL)
					if err != nil {
						return "", wrapError(err)
					}
					u, err := url.Parse(location)
					if err != nil {
						return "", wrapError(err)
					}
					return u.Query().Get("access-token"), nil
				},
			},
		)
		if err != nil {
			return wrapError(err)
		}
		if result.Chosen.Version == "" {
			return fmt.Errorf("%w: discovery chose no enrollment service", errOperation)
		}
		if _, err = d.Connect(ctx); err != nil {
			return wrapError(err)
		}
	}
	return nil
}

func depAssign(ctx context.Context, e *Environment, _ string) error {
	var tokens dep.Tokens
	b, err := os.ReadFile(e.Workspace.path("fixtures", "dep-tokens.json"))
	if err != nil {
		return wrapError(err)
	}
	if err = json.Unmarshal(b, &tokens); err != nil {
		return wrapError(err)
	}
	name := "bench-" + randomID()
	path := "/dep/accounts/" + name
	if err = e.api(ctx, "PUT", path+"/tokens", tokens, nil); err != nil {
		return wrapError(err)
	}
	if err = e.api(
		ctx,
		"PUT",
		path+"/profile",
		map[string]any{"profile_name": "bench", "url": e.URL + "/enroll/ade", "org_magic": "bench"},
		nil,
	); err != nil {
		return wrapError(err)
	}
	if err = e.api(ctx, "POST", path+"/sync", nil, nil); err != nil {
		return wrapError(err)
	}
	var devices struct{ Items []dep.Device }
	if err = e.api(ctx, "GET", path+"/devices", nil, &devices); err != nil {
		return wrapError(err)
	}
	if len(devices.Items) != 1 {
		return fmt.Errorf("%w: DEP sync did not persist the fixture device", errOperation)
	}
	return nil
}

func abmAssign(ctx context.Context, e *Environment, _ string) error {
	var servers struct{ Items []struct{ ID string } }
	if err := e.api(ctx, "GET", "/axm/servers", nil, &servers); err != nil {
		return wrapError(err)
	}
	if len(servers.Items) != 1 {
		return fmt.Errorf("%w: ABM server list differs", errOperation)
	}
	var devices struct{ Items []struct{ ID string } }
	if err := e.api(ctx, "GET", "/axm/devices?limit=2", nil, &devices); err != nil {
		return wrapError(err)
	}
	if len(devices.Items) != 2 {
		return fmt.Errorf("%w: ABM page limit differs", errOperation)
	}
	serials := []string{"BENCH-ABM-1", "BENCH-ABM-2", "BENCH-ABM-3"}
	var act axm.OrgDeviceActivity
	if err := e.api(
		ctx,
		"POST",
		"/axm/assign",
		map[string]any{"server": servers.Items[0].ID, "serials": serials, "wait": true},
		&act,
	); err != nil {
		return wrapError(err)
	}
	if act.Attributes.Status != axm.ActivityCompleted {
		return fmt.Errorf("%w: ABM assignment did not complete", errOperation)
	}
	return e.api(
		ctx,
		"POST",
		"/axm/unassign",
		map[string]any{"serials": serials, "wait": true},
		nil,
	)
}

func (e *Environment) factoryDevice() (*simulator.Device, error) {
	return e.issuedDevice("fixtures/device-root.pem", "fixtures/device-root.key")
}
