package bench

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
)

// EnrollmentPreflight checks local prerequisites without installing profiles or
// changing trust. Service discovery and device delivery have separate scenarios.
func EnrollmentPreflight(w *Workspace, identity string) map[string]any {
	missing := []string{}
	if identity != "" && identity != "acme" && identity != "scep" {
		missing = append(missing, "identity must be acme or scep")
	}
	caPEM, err := os.ReadFile(w.path("mdm", "ca.pem"))
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(caPEM) {
		missing = append(missing, "HTTPS CA certificate")
	}
	pair, err := tls.LoadX509KeyPair(w.path("mdm", "tls.pem"), w.path("mdm", "tls.key"))
	if err != nil {
		missing = append(missing, "HTTPS certificate and matching key")
	} else {
		host, _, _ := net.SplitHostPort(w.Listen)
		if _, err := pair.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: host}); err != nil {
			missing = append(missing, "valid HTTPS chain and listener hostname")
		}
	}
	if _, err := tls.LoadX509KeyPair(w.path("mdm", "ca.pem"), w.path("mdm", "ca.key")); err != nil {
		missing = append(missing, "identity issuer certificate and matching key")
	}
	if w.Mode == "live" {
		pem, err := os.ReadFile(w.path("mdm", "push.pem"))
		info, inspectErr := pushcert.Inspect(pem)
		if err != nil || inspectErr != nil || !info.MDM {
			missing = append(
				missing,
				"MDM APNs certificate (an app-push certificate is insufficient)",
			)
		} else {
			if !time.Now().Before(info.NotAfter) {
				missing = append(missing, "unexpired MDM APNs certificate")
			}
			if _, err := tls.LoadX509KeyPair(
				w.path("mdm", "push.pem"),
				w.path("mdm", "push.key"),
			); err != nil {
				missing = append(missing, "matching MDM APNs private key")
			}
		}
	}
	return map[string]any{
		"Ready":              len(missing) == 0,
		"Mode":               w.Mode,
		"Identity":           identity,
		"Missing":            missing,
		"DeviceInstallation": "Install the exported profiles in System Settings; then run live enrollment acceptance.",
	}
}

// ExportTrust bootstraps private HTTPS trust before the Mac can fetch an HTTPS profile.
func ExportTrust(w *Workspace, destination string) error {
	data, err := os.ReadFile(w.path("mdm", "ca.pem"))
	if err != nil {
		return wrapError(err)
	}
	p := &profile.Profile{
		Identifier:  "com.deploymenttheory.mdm.https-trust",
		UUID:        profile.NewUUID(),
		DisplayName: "MDM HTTPS trust",
		Scope:       profile.ScopeSystem,
	}
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			return fmt.Errorf("%w: malformed HTTPS CA bundle", errOperation)
		}
		data = bytes.TrimSpace(rest)
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || block.Type != "CERTIFICATE" || !cert.IsCA {
			return fmt.Errorf("%w: invalid HTTPS CA certificate", errOperation)
		}
		p.Payloads = append(
			p.Payloads,
			profile.Payload{
				Identifier:  p.Identifier + "." + cms.Fingerprint(cert),
				UUID:        profile.NewUUID(),
				DisplayName: cert.Subject.CommonName,
				Content:     &profiles.CertificateRoot{PayloadContent: cert.Raw},
			},
		)
	}
	if len(p.Payloads) == 0 {
		return fmt.Errorf("%w: empty HTTPS CA bundle", errOperation)
	}
	b, err := p.Marshal()
	if err != nil {
		return wrapError(err)
	}
	return privateFile(destination, b)
}

func publicEnrollmentGET(ctx context.Context, e *Environment, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", e.URL+path, nil)
	if err != nil {
		return nil, wrapError(err)
	}
	r, err := e.Client.Do(req)
	if err != nil {
		return nil, wrapError(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: enrollment endpoint returned HTTP %d", ErrBlocked, r.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	return b, wrapError(err)
}

func serviceDiscovery(ctx context.Context, e *Environment, _ string) error {
	b, err := publicEnrollmentGET(ctx, e, "/MDMServiceConfig")
	if err != nil {
		return err
	}
	var doc map[string]string
	if err = json.Unmarshal(b, &doc); err != nil {
		return wrapError(err)
	}
	if doc["dep_enrollment_url"] == "" ||
		doc["dep_anchor_certs_url"] != e.URL+"/enroll/trust/anchors" {
		return fmt.Errorf("%w: incomplete enrollment discovery", errOperation)
	}
	b, err = publicEnrollmentGET(ctx, e, "/enroll/trust/anchors")
	if err != nil {
		return err
	}
	var anchors [][]byte
	if err = json.Unmarshal(b, &anchors); err != nil {
		return wrapError(err)
	}
	if len(anchors) == 0 {
		return fmt.Errorf("%w: bench HTTPS anchor missing", errOperation)
	}
	if doc["trust_profile_url"] != e.URL+"/enroll/trust/profile" {
		return fmt.Errorf("%w: trust profile URL missing", errOperation)
	}
	b, err = publicEnrollmentGET(ctx, e, "/enroll/trust/profile")
	if err != nil {
		return err
	}
	p, err := profile.Parse(b, profile.ParseOptions{})
	if err != nil {
		return wrapError(err)
	}
	if len(p.Profile.Payloads) != len(anchors) {
		return fmt.Errorf("%w: trust profile differs from anchors", errOperation)
	}
	for i, payload := range p.Profile.Payloads {
		c, ok := payload.Content.(*profiles.CertificateRoot)
		if !ok || !bytes.Equal(c.PayloadContent, anchors[i]) {
			return fmt.Errorf("%w: trust profile contains unrelated payloads", errOperation)
		}
	}
	return nil
}

// ProfileWithIdentity exports the configured profile with the rights needed for
// inventory and controlled profile replacement. Empty identity uses server defaults.
func ProfileWithIdentity(
	ctx context.Context,
	e *Environment,
	device, identity, destination string,
) error {
	if device == "" {
		return fmt.Errorf("%w: -device-id is required", errOperation)
	}
	b, err := requestEnrollmentProfile(ctx, e, device, "", identity)
	if err != nil {
		return err
	}
	return privateFile(destination, b)
}

func requestEnrollmentProfile(
	ctx context.Context,
	e *Environment,
	device, serial, identity string,
) ([]byte, error) {
	if serial == "" && e.Workspace != nil && e.Workspace.Mode == "simulated" {
		serial = benchSerial
	}
	b, _ := json.Marshal(
		map[string]any{
			"DeviceID":     device,
			"Serial":       serial,
			"Identity":     identity,
			"AccessRights": enroll.RightQueryDeviceInfo | enroll.RightInspectProfiles | enroll.RightInstallProfiles,
		},
	)
	raw, _, err := HTTP(
		ctx,
		e.Client,
		e.URL,
		e.Token,
		"POST",
		"/enrollment-profiles",
		bytes.NewReader(b),
	)
	return raw, wrapError(err)
}

// Replace starts an authorized update; the existing push route wakes the device.
func Replace(ctx context.Context, e *Environment, device, identity string) (map[string]any, error) {
	if device == "" {
		return nil, fmt.Errorf("%w: -device-id is required", ErrBlocked)
	}
	path := "/enrollments/device/" + url.PathEscape(device)
	var result map[string]any
	if err := e.api(
		ctx,
		"POST",
		path+"/replacement",
		map[string]string{"Identity": identity},
		&result,
	); err != nil {
		return nil, err
	}
	var pushed struct{ Sent bool }
	if err := e.api(ctx, "POST", path+"/push", nil, &pushed); err != nil {
		return result, err
	}
	if !pushed.Sent {
		return result, fmt.Errorf(
			"%w: replacement started but APNs wake was not accepted",
			errOperation,
		)
	}
	return result, nil
}

func replacementScenario(
	method string,
	fail bool,
) func(context.Context, *Environment, string) error {
	return func(ctx context.Context, e *Environment, _ string) error {
		authority, err := e.attestation()
		if err != nil {
			return err
		}
		d := simulator.New(
			"BENCH-"+randomID(),
			simulator.WithClient(e.Client),
			simulator.WithACME(simulator.ACMEOptions{Attestation: authority}),
		)
		d.SerialNumber = benchSerial
		raw, err := requestEnrollmentProfile(ctx, e, d.UDID, d.SerialNumber, method)
		if err != nil {
			return err
		}
		if err = d.ApplyProfile(ctx, raw, profile.ParseOptions{}); err != nil {
			return wrapError(err)
		}
		if err = d.Enroll(ctx); err != nil {
			return wrapError(err)
		}
		old := d.Identity
		var started struct{ ID string }
		if err = e.api(
			ctx,
			"POST",
			pathOf(d)+"/replacement",
			map[string]string{"Identity": method},
			&started,
		); err != nil {
			return err
		}
		var installErr error
		d.Responder = func(cmd *mdm.Command) simulator.Reply {
			p, ok := cmd.Payload.(*commands.InstallProfile)
			if !ok {
				return simulator.AcknowledgeAll(cmd)
			}
			installErr = d.ApplyProfile(ctx, p.Payload, profile.ParseOptions{})
			if installErr == nil {
				installErr = d.Enroll(ctx)
			}
			if installErr != nil || fail {
				d.Identity = old
				return simulator.Reply{Status: mdm.StatusError}
			}
			return simulator.Reply{Status: mdm.StatusAcknowledged}
		}
		got, err := d.Connect(ctx)
		if err != nil {
			return wrapError(err)
		}
		if installErr != nil {
			return wrapError(installErr)
		}
		if len(got) != 1 || got[0].UUID != started.ID {
			return fmt.Errorf("%w: replacement command not delivered", errOperation)
		}
		var result struct{ State, CandidateCertificate, OldCertificate string }
		if err = e.api(ctx, "GET", pathOf(d)+"/replacement", nil, &result); err != nil {
			return err
		}
		want := "committed"
		if fail {
			want = "failed"
		}
		if result.State != want || result.CandidateCertificate == "" ||
			result.CandidateCertificate == result.OldCertificate {
			return fmt.Errorf("%w: replacement did not reach %s", errOperation, want)
		}
		if fail && cms.Fingerprint(d.Identity.Cert) != result.OldCertificate {
			return fmt.Errorf("%w: simulator did not restore previous identity", errOperation)
		}
		if _, err = enqueue(ctx, e, d, &commands.DeviceInformation{Queries: []string{"OSVersion"}}); err != nil {
			return err
		}
		got, err = d.Connect(ctx)
		if err != nil {
			return wrapError(err)
		}
		if len(got) != 1 || got[0].RequestType != "DeviceInformation" {
			return fmt.Errorf("%w: management did not continue after replacement", errOperation)
		}
		return nil
	}
}

func liveEnrollment(method string) func(context.Context, *Environment, string) error {
	return func(ctx context.Context, e *Environment, device string) error {
		if device == "" {
			return fmt.Errorf("%w: -device-id is required", ErrBlocked)
		}
		var evidence struct {
			Identity, Certificate           string
			Enabled                         bool
			AuthenticatedAt, TokenUpdatedAt time.Time
		}
		if err := e.api(
			ctx,
			"GET",
			"/enrollments/device/"+url.PathEscape(device)+"/enrollment-evidence",
			nil,
			&evidence,
		); err != nil {
			return fmt.Errorf("%w: enrollment evidence unavailable", ErrBlocked)
		}
		if evidence.Identity != method || evidence.Certificate == "" || !evidence.Enabled ||
			evidence.AuthenticatedAt.IsZero() ||
			evidence.TokenUpdatedAt.IsZero() {
			return fmt.Errorf("%w: completed %s enrollment is required", ErrBlocked, method)
		}
		if err := liveMDM(ctx, e, device); err != nil {
			return err
		}
		var users struct {
			Items []struct {
				Enabled  bool
				ParentID string
			}
		}
		if err := e.api(
			ctx,
			"GET",
			"/enrollments?parent="+url.QueryEscape(device),
			nil,
			&users,
		); err != nil {
			return err
		}
		for _, user := range users.Items {
			if user.Enabled && user.ParentID == device {
				return nil
			}
		}
		return fmt.Errorf("%w: installing user's management channel has not enrolled", ErrBlocked)
	}
}
