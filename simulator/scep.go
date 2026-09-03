package simulator

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/cms"
	"github.com/deploymenttheory/go-apple-dm/enroll"
	"github.com/deploymenttheory/go-apple-dm/plist"
	"github.com/deploymenttheory/go-apple-dm/profile"
	"github.com/deploymenttheory/go-apple-dm/scep"
	"github.com/deploymenttheory/go-apple-dm/schema/profiles"
)

// ErrProfile is returned when an enrollment profile cannot be followed.
var ErrProfile = errors.New("simulator: enrollment profile")

// ApplyProfile configures the device from an enrollment profile the way a
// device installing it would: URLs and topic from the MDM payload and, for
// a SCEP identity, a fresh RSA key enrolled at the SCEP URL. The device is
// then ready for Enroll.
func (d *Device) ApplyProfile(ctx context.Context, data []byte, o profile.ParseOptions) error {
	p, err := enroll.Parse(data, o)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrProfile, err)
	}
	if p.PKCS12 != nil {
		return fmt.Errorf("%w: PKCS #12 identities are not supported by the simulator; use WithIdentity", ErrProfile)
	}
	d.Topic, d.ServerURL, d.CheckinURL = p.Topic, p.ServerURL, p.CheckInURL
	if d.CheckinURL == "" {
		d.CheckinURL = d.ServerURL
	}
	if p.ACME != nil {
		return d.ACMEEnroll(ctx, p.ACME, d.acme)
	}
	bits := int(p.SCEP.KeySize)
	if bits == 0 {
		bits = 2048
	}
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return fmt.Errorf("simulator: generate key: %w", err)
	}
	subject := p.SCEP.Subject
	if subject.CommonName == "" {
		subject.CommonName = d.UDID
	}
	cert, err := scep.NewClient(p.SCEP.URL, d.Client).Enroll(ctx, key, scep.EnrollOptions{Subject: subject, Challenge: p.SCEP.Challenge})
	if err != nil {
		return fmt.Errorf("%w: SCEP: %w", ErrProfile, err)
	}
	d.Identity = &Identity{Cert: cert, Key: key}
	return nil
}

// OTAEnroll runs the over-the-air profile-service flow: phase 1 signed
// with the device certificate, SCEP enrollment from the returned profile,
// phase 2 signed with the new identity, then ApplyProfile on the final
// enrollment profile. deviceID plays the Apple-issued device certificate.
func (d *Device) OTAEnroll(ctx context.Context, profileServiceURL, challenge string, deviceID *Identity, o profile.ParseOptions) error {
	attrs := map[string]any{
		"UDID": d.UDID, "VERSION": d.BuildVersion, "PRODUCT": d.ProductName, "SERIAL": d.SerialNumber, "CHALLENGE": challenge,
	}
	phase1, err := d.otaPost(ctx, profileServiceURL, attrs, deviceID)
	if err != nil {
		return err
	}
	parsed, err := profile.Parse(phase1, o)
	if err != nil {
		return fmt.Errorf("%w: phase 1 profile: %w", ErrProfile, err)
	}
	sc, ok := profile.Find[*profiles.SCEP](parsed.Profile)
	if !ok {
		return fmt.Errorf("%w: phase 1 profile carries no SCEP payload", ErrProfile)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("simulator: generate key: %w", err)
	}
	subject := enroll.NameFromSubject(sc.PayloadContent.Subject)
	if subject.CommonName == "" {
		subject.CommonName = d.UDID
	}
	scepChallenge := ""
	if sc.PayloadContent.Challenge != nil {
		scepChallenge = *sc.PayloadContent.Challenge
	}
	cert, err := scep.NewClient(sc.PayloadContent.URL, d.Client).Enroll(ctx, key, scep.EnrollOptions{Subject: subject, Challenge: scepChallenge})
	if err != nil {
		return fmt.Errorf("%w: phase 1 SCEP: %w", ErrProfile, err)
	}
	phase2, err := d.otaPost(ctx, profileServiceURL, attrs, &Identity{Cert: cert, Key: key})
	if err != nil {
		return err
	}
	// The final profile carries its own identity payload, as Apple's flow
	// describes; ApplyProfile enrolls with it.
	return d.ApplyProfile(ctx, phase2, o)
}

func (d *Device) otaPost(ctx context.Context, url string, attrs map[string]any, signer *Identity) ([]byte, error) {
	body, err := plist.Marshal(attrs)
	if err != nil {
		return nil, fmt.Errorf("simulator: %w", err)
	}
	signed, err := cms.SignAttached(body, signer.Cert, signer.Key)
	if err != nil {
		return nil, fmt.Errorf("simulator: sign OTA request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(signed))
	if err != nil {
		return nil, fmt.Errorf("simulator: %w", err)
	}
	req.Header.Set("Content-Type", "application/pkcs7-signature")
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("simulator: OTA request: %w", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, plist.DefaultMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("simulator: read OTA response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Status: resp.StatusCode, Body: out}
	}
	return out, nil
}
