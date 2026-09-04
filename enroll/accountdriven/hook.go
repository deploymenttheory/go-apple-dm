package accountdriven

import (
	"context"
	"errors"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/hook"
	"github.com/deploymenttheory/go-apple-dm/mdm"
)

// ErrEnrollmentToken is the hook's veto: the check-in carried no valid
// enrollment token.
var ErrEnrollmentToken = errors.New("accountdriven: enrollment token required")

// CheckinHook is a hook.Hook that requires the enrollment token on the
// Authenticate and TokenUpdate of user enrollments (the token travels in
// ServerURL as a query parameter, which the HTTP layer exposes as
// Request.Params). Unlike the access token it is not consumed, so a retried
// check-in succeeds. Managed Apple Account and identity claims are attached to
// the context for later hooks through IdentityFromContext.
type CheckinHook struct {
	Tokens *Tokens
	// Channels selects which enrollment kinds the hook guards; nil means
	// the User Enrollment channels.
	Channels []mdm.Channel
}

type identityKey struct{}

// IdentityFromContext returns the identity the hook verified.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// Before implements hook.Hook.
func (h *CheckinHook) Before(ctx context.Context, c *hook.Call) (context.Context, error) {
	if c == nil || c.Request == nil {
		return ctx, nil
	}
	if c.Op != "checkin:Authenticate" && c.Op != "checkin:TokenUpdate" {
		return ctx, nil
	}
	if !h.guards(c.Request.ID.Channel) {
		return ctx, nil
	}
	tok := c.Request.Params[ParamEnrollmentToken]
	rec, err := h.Tokens.Check(ctx, KindEnrollment, tok)
	if err != nil {
		return ctx, fmt.Errorf("%w: %w", ErrEnrollmentToken, err)
	}
	return context.WithValue(ctx, identityKey{}, rec.Identity), nil
}

// After implements hook.Hook.
func (h *CheckinHook) After(context.Context, *hook.Call, error) {}

func (h *CheckinHook) guards(ch mdm.Channel) bool {
	if len(h.Channels) == 0 {
		return ch == mdm.ChannelUserEnrollmentDevice || ch == mdm.ChannelUserEnrollmentUser
	}
	for _, c := range h.Channels {
		if c == ch {
			return true
		}
	}
	return false
}
