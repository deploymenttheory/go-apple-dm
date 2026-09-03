package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/cms"
	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/plist"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// CheckinResult is the response to a check-in message. Most messages
// return an empty body.
type CheckinResult struct {
	Body        []byte
	ContentType string
	// Status overrides the HTTP status when non-zero.
	Status int
}

// ContentTypePlist is the content type for plist response bodies.
const ContentTypePlist = "application/xml; charset=utf-8"

// Checkin handles one check-in message.
func (c *Core) Checkin(ctx context.Context, r *mdm.Request, ck *mdm.Checkin) (*CheckinResult, error) {
	if r == nil || ck == nil || ck.Message == nil {
		return nil, wrapCode(CodeBadRequest, fmt.Errorf("%w: nil request or message", ErrInvalidMessage))
	}
	r.ID = ck.ID
	r.Enrollment = ck.Enrollment
	call := &Call{Op: "checkin:" + ck.Type, Request: r, Checkin: ck}
	ctx, after, err := c.runHooks(ctx, call)
	if err != nil {
		return nil, err
	}
	res, err := c.dispatchCheckin(ctx, r, ck)
	after(err)
	if err != nil {
		return nil, err
	}
	if res == nil {
		res = &CheckinResult{}
	}
	return res, nil
}

func (c *Core) dispatchCheckin(ctx context.Context, r *mdm.Request, ck *mdm.Checkin) (*CheckinResult, error) {
	switch m := ck.Message.(type) {
	case *checkin.Authenticate:
		return nil, c.authenticate(ctx, r, ck, m)
	case *checkin.TokenUpdate:
		return nil, c.tokenUpdate(ctx, r, ck, m)
	case *checkin.CheckOut:
		return nil, c.checkOut(ctx, r)
	case *checkin.SetBootstrapToken:
		return nil, c.setBootstrapToken(ctx, r, m)
	case *checkin.GetBootstrapToken:
		return c.getBootstrapToken(ctx, r)
	case *checkin.GetToken:
		return c.handleGetToken(ctx, r, m)
	case *checkin.UserAuthenticate:
		return c.handleUserAuthenticate(ctx, r, m)
	case *checkin.DeclarativeManagement:
		return c.handleDeclarativeManagement(ctx, r, ck, m)
	}
	return nil, wrapCode(CodeBadRequest, fmt.Errorf("%w: unsupported message %s", ErrInvalidMessage, ck.Type))
}

// authorize enforces certificate pinning for every message after
// Authenticate. Unknown enrollments are reported as such so the transport
// can answer with Apple's unrecognized-device body.
func (c *Core) authorize(ctx context.Context, r *mdm.Request) error {
	if c.pinning == PinOff {
		return nil
	}
	if r.Certificate == nil {
		if c.pinning == PinEnforce {
			return wrapCode(CodeForbidden, ErrCertRequired)
		}
		return nil
	}
	hash := cms.Fingerprint(r.Certificate)
	pinned, err := c.store.CertHash(ctx, r.ID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return wrapCode(CodeUnknownEnrollment, fmt.Errorf("%w: %s", ErrUnknownEnrollment, r.ID.ID))
		}
		return wrapCode(CodeInternal, err)
	}
	switch {
	case pinned == "":
		// Enrollment created without a certificate (migration or a
		// transport that could not extract one): pin retroactively, but
		// only a hash no other enrollment has ever presented.
		others, err := c.otherHolders(ctx, r.ID, hash)
		if err != nil {
			return wrapCode(CodeInternal, err)
		}
		if len(others) > 0 {
			if c.pinning == PinWarn {
				c.log.WarnContext(ctx, "identity certificate seen on another enrollment; not pinning", "enrollment", r.ID.ID, "previous", others[0].ID.ID)
				return nil
			}
			return wrapCode(CodeForbidden, fmt.Errorf("%w: previously pinned by %s", ErrCertReused, others[0].ID.ID))
		}
		if err := c.store.AssociateCert(ctx, r.ID, hash, c.clock.Now()); err != nil {
			if errors.Is(err, storage.ErrConflict) {
				return wrapCode(CodeForbidden, fmt.Errorf("%w: %w", ErrCertMismatch, err))
			}
			return wrapCode(CodeInternal, err)
		}
	case pinned != hash:
		if c.pinning == PinWarn {
			c.log.WarnContext(ctx, "identity certificate mismatch", "enrollment", r.ID.ID)
			return nil
		}
		return wrapCode(CodeForbidden, ErrCertMismatch)
	}
	return nil
}

// otherHolders returns the history rows of every enrollment other than the
// device channel of id that ever pinned hash.
func (c *Core) otherHolders(ctx context.Context, id mdm.EnrollmentID, hash string) ([]storage.CertAssociation, error) {
	history, err := c.store.CertHashHistory(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("certificate history: %w", err)
	}
	self := id.Device().ID
	others := make([]storage.CertAssociation, 0, len(history))
	for _, a := range history {
		if a.ID.ID != self {
			others = append(others, a)
		}
	}
	return others, nil
}

func (c *Core) authenticate(ctx context.Context, r *mdm.Request, ck *mdm.Checkin, m *checkin.Authenticate) error {
	if r.ID.Channel.IsUser() {
		return wrapCode(CodeBadRequest, fmt.Errorf("%w: Authenticate on a user channel", ErrInvalidMessage))
	}
	if r.Certificate == nil && c.pinning == PinEnforce {
		return wrapCode(CodeForbidden, ErrCertRequired)
	}
	now := c.clock.Now()
	existing, err := c.store.Get(ctx, r.ID)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return wrapCode(CodeInternal, err)
	}
	var hash string
	if r.Certificate != nil {
		hash = cms.Fingerprint(r.Certificate)
	}
	rotated := false
	if existing != nil && existing.CertHash != "" && hash != "" && existing.CertHash != hash && c.pinning != PinOff {
		if perr := c.reenroll(ctx, r, existing); perr != nil {
			return wrapCode(CodeForbidden, fmt.Errorf("%w: %w", ErrReenrollDenied, perr))
		}
		rotated = true
	}
	if hash != "" && c.pinning != PinOff {
		others, err := c.otherHolders(ctx, r.ID, hash)
		if err != nil {
			return wrapCode(CodeInternal, err)
		}
		if len(others) > 0 {
			if perr := c.reuse(ctx, r, others); perr != nil {
				c.publish(ctx, event.CertReuseDenied, r.ID, "device", others)
				if !errors.Is(perr, ErrCertReused) {
					perr = fmt.Errorf("%w: %w", ErrCertReused, perr)
				}
				return wrapCode(CodeForbidden, perr)
			}
		}
	}
	if err := c.store.UpsertAuthenticate(ctx, r.ID, m, ck.Raw, now); err != nil {
		return wrapCode(codeForStorage(err), err)
	}
	if hash != "" {
		if err := c.store.AssociateCert(ctx, r.ID, hash, now); err != nil {
			if errors.Is(err, storage.ErrConflict) {
				return wrapCode(CodeForbidden, fmt.Errorf("%w: %w", ErrCertMismatch, err))
			}
			return wrapCode(CodeInternal, err)
		}
	}
	if rotated {
		c.publish(ctx, event.CertRotated, r.ID, "device", existing.CertHash)
	}
	if existing == nil {
		c.publish(ctx, event.Enrolled, r.ID, "device", m)
	} else {
		c.publish(ctx, event.Reenrolled, r.ID, "device", m)
	}
	return nil
}

func (c *Core) tokenUpdate(ctx context.Context, r *mdm.Request, ck *mdm.Checkin, m *checkin.TokenUpdate) error {
	push, err := mdm.PushFromTokenUpdate(m)
	if err != nil {
		return wrapCode(CodeBadRequest, err)
	}
	now := c.clock.Now()
	if r.ID.Channel.IsUser() {
		// User channels enrol with TokenUpdate alone; the device channel
		// must already exist.
		if err := c.authorize(ctx, r); err != nil {
			return err
		}
		if c.requireUserAuth && r.ID.Channel == mdm.ChannelUser {
			// The user must have completed UserAuthenticate (0016) first.
			state, err := c.store.UserAuth(ctx, r.ID)
			switch {
			case errors.Is(err, storage.ErrNotFound), err == nil && state.AuthToken == "":
				return wrapCode(CodeForbidden, fmt.Errorf("%w: %s", ErrUserAuthRequired, r.ID.ID))
			case err != nil:
				return wrapCode(CodeInternal, err)
			}
		}
		if _, err := c.store.Get(ctx, r.ID); errors.Is(err, storage.ErrNotFound) {
			if err := c.store.UpsertAuthenticate(ctx, r.ID, nil, ck.Raw, now); err != nil {
				return wrapCode(codeForStorage(err), err)
			}
		} else if err != nil {
			return wrapCode(CodeInternal, err)
		}
	} else if err := c.authorize(ctx, r); err != nil {
		return err
	}
	if err := c.store.StoreTokenUpdate(ctx, r.ID, push, m, ck.Raw, now); err != nil {
		return wrapCode(codeForStorage(err), err)
	}
	c.publish(ctx, event.TokenUpdated, r.ID, "device", m)
	return nil
}

func (c *Core) checkOut(ctx context.Context, r *mdm.Request) error {
	if err := c.authorize(ctx, r); err != nil {
		return err
	}
	if err := c.store.Disable(ctx, r.ID, c.clock.Now()); err != nil {
		return wrapCode(codeForStorage(err), err)
	}
	c.publish(ctx, event.CheckedOut, r.ID, "device", nil)
	return nil
}

func (c *Core) setBootstrapToken(ctx context.Context, r *mdm.Request, m *checkin.SetBootstrapToken) error {
	if err := c.authorize(ctx, r); err != nil {
		return err
	}
	if err := c.store.StoreBootstrapToken(ctx, r.ID, m.BootstrapToken, c.clock.Now()); err != nil {
		return wrapCode(codeForStorage(err), err)
	}
	c.publish(ctx, event.BootstrapTokenSet, r.ID, "device", nil)
	return nil
}

func (c *Core) getBootstrapToken(ctx context.Context, r *mdm.Request) (*CheckinResult, error) {
	if err := c.authorize(ctx, r); err != nil {
		return nil, err
	}
	tok, err := c.store.BootstrapToken(ctx, r.ID)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, wrapCode(codeForStorage(err), err)
	}
	resp := checkin.GetBootstrapTokenResponse{}
	if len(tok) > 0 {
		resp.BootstrapToken = tok
	}
	return plistResult(resp)
}

func (c *Core) handleGetToken(ctx context.Context, r *mdm.Request, m *checkin.GetToken) (*CheckinResult, error) {
	if err := c.authorize(ctx, r); err != nil {
		return nil, err
	}
	if c.getToken == nil {
		return nil, wrapCode(CodeNotImplemented, fmt.Errorf("%w: GetToken", ErrNoHandler))
	}
	resp, err := c.getToken(ctx, r, m)
	if err != nil {
		return nil, wrapCode(CodeOf(err), err)
	}
	return plistResult(resp)
}

func (c *Core) handleUserAuthenticate(ctx context.Context, r *mdm.Request, m *checkin.UserAuthenticate) (*CheckinResult, error) {
	if err := c.authorize(ctx, r); err != nil {
		return nil, err
	}
	resp, err := c.userAuth(ctx, r, m)
	if err != nil {
		if errors.Is(err, ErrUserNotManaged) {
			// The policy declines this user: Apple's 410 (decision record 0029).
			return nil, wrapCode(CodeGone, err)
		}
		return nil, wrapCode(CodeOf(err), err)
	}
	return plistResult(resp)
}

func (c *Core) handleDeclarativeManagement(ctx context.Context, r *mdm.Request, ck *mdm.Checkin, m *checkin.DeclarativeManagement) (*CheckinResult, error) {
	if err := c.authorize(ctx, r); err != nil {
		return nil, err
	}
	if c.dm == nil {
		return nil, wrapCode(CodeNotImplemented, fmt.Errorf("%w: DeclarativeManagement", ErrNoHandler))
	}
	resp, err := c.dm(ctx, r, ck, m)
	if err != nil {
		return nil, wrapCode(CodeOf(err), err)
	}
	return &CheckinResult{Body: resp.Body, ContentType: resp.ContentType, Status: resp.Status}, nil
}

func plistResult(v any) (*CheckinResult, error) {
	body, err := plist.Marshal(v)
	if err != nil {
		return nil, wrapCode(CodeInternal, err)
	}
	return &CheckinResult{Body: body, ContentType: ContentTypePlist}, nil
}
