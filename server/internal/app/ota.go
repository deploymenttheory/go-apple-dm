package app

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"fmt"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
)

// wireOTA uses the configured enrollment issuer for both OTA phases. Bootstrap
// device trust and the admission challenge are explicit deployment settings.
func (a *App) wireOTA(mux *http.ServeMux) error {
	e := a.enroll
	if e == nil || e.cfg.OTAAnchorFile == "" {
		return nil
	}
	if e.cfg.OTAChallenge == "" || e.cfg.Identity == IdentityACME {
		return fmt.Errorf(
			"%w: OTA requires a challenge and SCEP enrollment identities",
			errOperation,
		)
	}
	certs, err := readCertsPEM(e.cfg.OTAAnchorFile)
	if err != nil {
		return wrapError(err)
	}
	deviceRoots := x509.NewCertPool()
	for _, c := range certs {
		deviceRoots.AddCert(c)
	}
	identityRoots := x509.NewCertPool()
	identityRoots.AddCert(e.caCert)
	ota := &enroll.OTAService{
		DeviceRoots: deviceRoots, IdentityRoots: identityRoots, Logger: a.cfg.Logger,
		Authorize: func(_ context.Context, r *enroll.OTARequest) error {
			if r.Phase == enroll.PhaseDevice {
				if subtle.ConstantTimeCompare(
					[]byte(r.Attributes.Challenge),
					[]byte(e.cfg.OTAChallenge),
				) != 1 {
					return fmt.Errorf("%w: invalid OTA challenge", errOperation)
				}
			} else if r.Signer.Subject.CommonName != r.Attributes.UDID {
				return fmt.Errorf("%w: OTA identity does not match device", errOperation)
			}
			return nil
		},
		Profile: func(ctx context.Context, r *enroll.OTARequest) ([]byte, error) {
			p, err := e.profile(
				ctx,
				acme.Binding{
					UDID:       r.Attributes.UDID,
					Serial:     r.Attributes.Serial,
					CommonName: r.Attributes.UDID,
				},
			)
			if err != nil {
				return nil, wrapError(err)
			}
			if r.Phase == enroll.PhaseIdentity {
				return p.Marshal()
			}
			built, err := p.Build()
			if err != nil {
				return nil, wrapError(err)
			}
			var bootstrap []profile.Payload
			for _, payload := range built.Payloads {
				if payload.Content.PayloadTypeName() != "com.apple.mdm" {
					bootstrap = append(bootstrap, payload)
				}
			}
			built.Payloads = bootstrap
			return built.Marshal()
		},
	}
	mux.Handle("/ota", ota.Handler())
	return nil
}
