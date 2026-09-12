package accountdriven

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/dmhook"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// ErrEnrollmentToken indicates that account enrollment authorization failed.
var ErrEnrollmentToken = errors.New("accountdriven: enrollment authorization required")

// Reauthentication asks the transport to restart account authentication. Only
// recognized, certificate-associated account enrollments may produce this result.
type Reauthentication struct{ Challenge Challenge }

func (e *Reauthentication) Error() string { return "accountdriven: reauthentication required" }

// CheckinHook authenticates account-driven check-in, command and DDM requests.
// It selects flows by issuer-registered certificate associations, not channel alone.
type CheckinHook struct {
	Tokens       *Tokens
	Verifier     Verifier
	Associations *Associations
	Auth         Authenticator
	// Channels is retained for source compatibility; platform and origin determine
	// requirements. Deprecated: channel-only filtering cannot identify ADDE.
	Channels []mdm.Channel
}

type identityKey struct{}

// IdentityFromContext returns the verified account identity.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}
func (h *CheckinHook) associations() *Associations {
	if h.Associations != nil {
		return h.Associations
	}
	return h.Tokens.AssociationStore()
}

// Before verifies certificate association and, where Apple sends one, bearer
// identity. It reserves an Authenticate identity atomically before any side effect.
func (h *CheckinHook) Before(ctx context.Context, c *dmhook.Call) (context.Context, error) {
	if c == nil || c.Request == nil {
		return ctx, nil
	}
	r := c.Request
	s := h.associations()
	if s == nil {
		return ctx, ErrConfig
	}
	a, err := s.ByCertificate(ctx, r.Certificate)
	if errors.Is(err, state.ErrNotFound) {
		if r.ID.Channel == mdm.ChannelUserEnrollmentDevice || r.ID.Channel == mdm.ChannelUserEnrollmentUser || r.Params[ParamEnrollmentToken] != "" || (r.Certificate != nil && strings.HasPrefix(r.Certificate.Subject.CommonName, CertificateSubjectPrefix)) {
			return ctx, ErrEnrollmentToken
		}
		return ctx, nil
	}
	if err != nil {
		return ctx, err
	}
	device := r.ID.Device()
	if err := device.Validate(); err != nil {
		return ctx, err
	}
	if (a.Origin == VersionBYOD) != (device.Channel == mdm.ChannelUserEnrollmentDevice) {
		return ctx, ErrAssociation
	}
	if a.Enrollment.ID != "" && a.Enrollment != device {
		return ctx, ErrAssociation
	}
	if a.Enrollment.ID == "" && (c.Op != "checkin:Authenticate" || r.ID.Channel.IsUser()) {
		return ctx, ErrAssociation
	}
	if a.RequiresBearer(r.ID.Channel) {
		verifier := h.Verifier
		if verifier == nil && h.Tokens != nil {
			verifier = h.Tokens
		}
		if verifier == nil {
			return ctx, ErrConfig
		}
		id, err := verifier.Verify(ctx, r.Bearer)
		if err != nil {
			if !errors.Is(err, ErrTokenNotFound) && !errors.Is(err, ErrTokenExpired) && !errors.Is(err, ErrTokenUsed) {
				return ctx, err
			}
			if h.Auth == nil {
				return ctx, ErrEnrollmentToken
			}
			challenge, err := h.Auth.Challenge(ctx, &http.Request{}, &DeviceInfo{Product: a.Product})
			if err != nil {
				return ctx, err
			}
			if _, err := challenge.Header(); err != nil {
				return ctx, err
			}
			return ctx, &Reauthentication{Challenge: challenge}
		}
		if !sameIdentity(a.Identity, id) {
			return ctx, fmt.Errorf("%w: %w", ErrEnrollmentToken, ErrAssociation)
		}
	}
	if c.Op == "checkin:Authenticate" {
		if err := s.Bind(ctx, a.Reference, device, false); err != nil {
			return ctx, err
		}
	}
	if c.Op != "checkin:Authenticate" && a.ConfirmedAt.IsZero() {
		return ctx, ErrAssociation
	}
	ctx = context.WithValue(ctx, associationContextKey{}, a)
	return context.WithValue(ctx, identityKey{}, a.Identity), nil
}

// Complete confirms a successful Authenticate; failures propagate to the transport
// so it cannot report success with an unconfirmed association.
func (h *CheckinHook) Complete(ctx context.Context, c *dmhook.Call) error {
	if c != nil && c.Request != nil && c.Op == "checkin:Authenticate" {
		if a, ok := AssociationFromContext(ctx); ok {
			return h.associations().Bind(ctx, a.Reference, c.Request.ID.Device(), true)
		}
	}
	return nil
}

// After implements dmhook.Hook.
func (h *CheckinHook) After(context.Context, *dmhook.Call, error) {}
