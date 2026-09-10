package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
)

// AdmissionRequest contains authenticated device or account properties.
type AdmissionRequest struct {
	Serial        string   `json:"serial,omitempty"`
	UDID          string   `json:"udid,omitempty"`
	Issuer        string   `json:"issuer,omitempty"`
	Subject       string   `json:"subject,omitempty"`
	Email         string   `json:"email,omitempty"`
	EmailVerified bool     `json:"emailVerified,omitempty"`
	Groups        []string `json:"groups,omitempty"`
}

// AdmissionGrant authorizes one enrollment attempt. Account is the managed Apple
// account selected by policy. The reference admission gate records the verified
// Identity and enrollment Mode before issuing a credential.
type AdmissionGrant struct {
	Account   string           `json:"account,omitempty"`
	ExpiresAt time.Time        `json:"expiresAt"`
	Identity  AdmissionRequest `json:"identity"`
	Mode      string           `json:"mode"`
}

// EnrollmentAdmission decides eligibility before credential issuance.
type EnrollmentAdmission func(context.Context, AdmissionRequest) (AdmissionGrant, error)

// AdmissionPolicy is the JSON format of DM_ENROLLMENT_POLICY_FILE. Each device
// rule requires every nonempty identifier to match. Accounts require an exact
// issuer and subject or verified email; Groups, when supplied, requires membership
// in at least one listed group. An empty policy admits nobody.
type AdmissionPolicy struct {
	Devices     []DeviceAdmissionRule  `json:"devices"`
	DEPAccounts []string               `json:"depAccounts"`
	Accounts    []AccountAdmissionRule `json:"accounts"`
}

// DeviceAdmissionRule requires every configured device identifier to match.
type DeviceAdmissionRule struct {
	Serial string `json:"serial"`
	UDID   string `json:"udid"`
}

// AccountAdmissionRule maps an authenticated IdP identity to a managed account.
type AccountAdmissionRule struct {
	Issuer              string   `json:"issuer"`
	Subject             string   `json:"subject"`
	Email               string   `json:"email"`
	ManagedAppleAccount string   `json:"managedAppleAccount"`
	Groups              []string `json:"groups"`
}

var errDEPAdmissionUnavailable = errors.New("app: DEP admission store unavailable")

func (rule AccountAdmissionRule) matches(r AdmissionRequest) bool {
	if rule.Issuer == "" || rule.Issuer != r.Issuer || rule.ManagedAppleAccount == "" {
		return false
	}
	if rule.Subject == "" && rule.Email == "" {
		return false
	}
	if rule.Subject != "" && rule.Subject != r.Subject {
		return false
	}
	if rule.Email != "" && (!r.EmailVerified || rule.Email != r.Email) {
		return false
	}
	return len(rule.Groups) == 0 ||
		slices.ContainsFunc(
			rule.Groups,
			func(g string) bool { return slices.Contains(r.Groups, g) },
		)
}

func (a *App) enrollmentAdmission(e *enrollment) (EnrollmentAdmission, error) {
	if e.cfg.Admission != nil {
		return e.cfg.Admission, nil
	}
	var policy AdmissionPolicy
	if e.cfg.AdmissionFile != "" {
		b, err := os.ReadFile(e.cfg.AdmissionFile)
		if err != nil {
			return nil, fmt.Errorf("app: read admission policy: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&policy); err != nil {
			return nil, fmt.Errorf("app: decode admission policy: %w", err)
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, fmt.Errorf("%w: admission policy must be one JSON document", ErrConfig)
		}
	}
	return func(ctx context.Context, r AdmissionRequest) (AdmissionGrant, error) {
		grant := AdmissionGrant{
			ExpiresAt: a.cfg.Clock.Now().Add(time.Hour),
			Identity:  r,
			Mode:      "device",
		}
		if r.Issuer != "" || r.Subject != "" {
			for _, rule := range policy.Accounts {
				if !rule.matches(r) {
					continue
				}
				grant.Mode = "account"
				grant.Account = rule.ManagedAppleAccount
				return grant, nil
			}
			return AdmissionGrant{}, webauth.ErrDenied
		}
		for _, rule := range policy.Devices {
			if rule.Serial == "" && rule.UDID == "" {
				continue
			}
			if (rule.Serial == "" || rule.Serial == r.Serial) &&
				(rule.UDID == "" || rule.UDID == r.UDID) {
				return grant, nil
			}
		}
		if len(policy.DEPAccounts) > 0 && r.Serial != "" {
			if a.dep == nil {
				return AdmissionGrant{}, errDEPAdmissionUnavailable
			}
			for _, name := range policy.DEPAccounts {
				account, err := a.dep.store.GetAccount(ctx, name)
				if errors.Is(err, dep.ErrNotFound) {
					continue
				}
				if err != nil {
					return AdmissionGrant{}, fmt.Errorf("app: DEP admission account: %w", err)
				}
				d, err := a.dep.store.GetDevice(ctx, name, r.Serial)
				if errors.Is(err, dep.ErrNotFound) {
					continue
				}
				if err != nil {
					return AdmissionGrant{}, fmt.Errorf("app: enrollment admission: %w", err)
				}
				if admittedDEPDevice(d, account) {
					return grant, nil
				}
			}
		}
		return AdmissionGrant{}, webauth.ErrDenied
	}, nil
}

func accountAdmission(id accountdriven.Identity) AdmissionRequest {
	c := id.Claims
	email, _ := c["email"].(string)
	verified, _ := c["email_verified"].(bool)
	var groups []string
	switch v := c["groups"].(type) {
	case []string:
		groups = v
	case []any:
		for _, g := range v {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
	}
	return AdmissionRequest{
		Issuer:        id.Issuer,
		Subject:       id.Subject,
		Email:         email,
		EmailVerified: verified,
		Groups:        groups,
	}
}

func (e *enrollment) admit(
	ctx context.Context,
	b acme.Binding,
) (grant AdmissionGrant, admissionErr error) {
	defer func() {
		if admissionErr != nil && e.app != nil {
			e.app.securityEvent(ctx, event.EnrollmentDenied)
		}
	}()
	r := AdmissionRequest{Serial: b.Serial, UDID: b.EnrollmentUDID()}
	assoc, account := accountdriven.AssociationFromContext(ctx)
	if ref, ok := strings.CutPrefix(
		b.CommonName,
		accountdriven.CertificateSubjectPrefix,
	); ok &&
		!account {
		if e.tokens == nil {
			return AdmissionGrant{}, webauth.ErrDenied
		}
		var err error
		assoc, err = e.tokens.AssociationStore().Get(ctx, ref)
		if err != nil {
			return AdmissionGrant{}, fmt.Errorf("app: enrollment admission: %w", err)
		}
		account = true
	}
	if account {
		r = accountAdmission(assoc.Identity)
	}
	if e.admission == nil {
		return AdmissionGrant{}, webauth.ErrDenied
	}
	g, err := e.admission(ctx, r)
	if err != nil {
		return AdmissionGrant{}, fmt.Errorf("app: enrollment admission: %w", err)
	}
	if account && (g.Account == "" || g.Account != assoc.Identity.ManagedAppleAccount) {
		return AdmissionGrant{}, webauth.ErrDenied
	}
	if g.ExpiresAt.IsZero() || !e.now().Before(g.ExpiresAt) {
		return AdmissionGrant{}, webauth.ErrDenied
	}
	g.Identity, g.Mode = r, "device"
	if account {
		g.Mode = assoc.Origin
	}
	return g, nil
}

func (a *App) securityEvent(ctx context.Context, kind event.Type) {
	if a.cfg.Bus == nil {
		return
	}
	if err := a.cfg.Bus.Publish(
		ctx,
		event.Event{Type: kind, At: a.cfg.Clock.Now(), Actor: "security"},
	); err != nil &&
		a.cfg.Logger != nil {
		a.cfg.Logger.WarnContext(ctx, "security event delivery failed", "type", kind)
	}
}

func admittedDEPDevice(d *dep.StoredDevice, account *dep.Account) bool {
	return d != nil && account != nil && !d.Deleted && account.ProfileUUID != "" &&
		d.ProfileUUID == account.ProfileUUID &&
		(d.ProfileStatus == dep.ProfileStatusAssigned || d.ProfileStatus == dep.ProfileStatusPushed)
}
