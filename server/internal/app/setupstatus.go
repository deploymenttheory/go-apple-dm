package app

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
)

// SetupStatus reports certificate readiness separately from listeners running
// in this process. Bootstrap can complete before the first server start.
type SetupStatus struct {
	Role              string                      `json:"role"`
	Ready             bool                        `json:"ready"`
	EnrollmentEnabled bool                        `json:"enrollmentEnabled"`
	Issues            []string                    `json:"issues"`
	Identities        []lifecycle.Identity        `json:"identities"`
	PublicACME        *lifecycle.PublicACMEStatus `json:"publicAcme,omitempty"`
}

func (a *App) CertificateSetupStatus(ctx context.Context) (SetupStatus, error) {
	if a.Certificates == nil {
		return SetupStatus{}, ErrConfig
	}
	cfg := a.cfg.Setup
	out := SetupStatus{Role: cfg.Role, EnrollmentEnabled: a.enroll != nil, Issues: []string{}}
	var err error
	out.Identities, err = a.Certificates.List(ctx)
	if err != nil {
		return out, wrapError(err)
	}
	required := []string{cfg.HTTPSID}
	if cfg.Role != "vendor" {
		required = append(required, cfg.PushID, cfg.IssuerID)
	}
	if cfg.Role != "customer" {
		required = append(required, cfg.VendorID)
	}
	for _, id := range required {
		found := false
		for _, item := range out.Identities {
			if item.ID != id {
				continue
			}
			found = item.Active != "" && item.Severity != "expired"
		}
		if !found {
			out.Issues = append(
				out.Issues,
				fmt.Sprintf("%s requires a valid active certificate", id),
			)
		}
	}
	if status, err := a.Certificates.PublicACMEStatus(ctx, cfg.HTTPSID); err == nil {
		out.PublicACME = &status
		if cfg.HTTP01Listen == "" {
			out.Issues = append(
				out.Issues,
				"configure http01Listen or route public port 80 challenges to this server",
			)
		}
	} else if !errors.Is(err, lifecycle.ErrNotFound) {
		return out, wrapError(err)
	}
	out.Ready = len(out.Issues) == 0
	return out, nil
}

// SetupTrustProfile exports public lab HTTPS trust before enrollment is enabled.
func (a *App) SetupTrustProfile(ctx context.Context) ([]byte, error) {
	if a.Certificates == nil || a.cfg.Setup.HTTPSCAID == "" {
		return nil, lifecycle.ErrNotFound
	}
	pairs, err := a.certificatePairs(ctx, a.cfg.Setup.HTTPSCAID, false)
	if err != nil {
		return nil, wrapError(err)
	}
	certificates := make([]*x509.Certificate, 0, len(pairs))
	for _, pair := range pairs {
		certificates = append(certificates, pair.Leaf)
	}
	return trustProfile(certificates)
}
