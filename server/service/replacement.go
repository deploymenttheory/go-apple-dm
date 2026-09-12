package service

import (
	"context"
	"errors"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

func (c *Core) replacement(ctx context.Context, r *mdm.Request) (*storage.Replacement, string, error) {
	if c.replacements == nil || r.Certificate == nil {
		return nil, "", nil
	}
	x, err := c.replacements.TransitionReplacement(ctx, r.ID.Device(), storage.ReplacementChange{Op: "read", At: c.clock.Now()})
	if errors.Is(err, storage.ErrNotFound) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", wrapCode(CodeInternal, err)
	}
	return x, cms.Fingerprint(r.Certificate), nil
}

func (c *Core) transitionReplacement(ctx context.Context, r *mdm.Request, ch storage.ReplacementChange) (*storage.Replacement, error) {
	ch.At = c.clock.Now()
	x, err := c.replacements.TransitionReplacement(ctx, r.ID.Device(), ch)
	if err != nil {
		return nil, wrapCode(codeForStorage(err), err)
	}
	if x != nil && x.State == storage.ReplacementCommitted {
		c.publish(ctx, event.CertRotated, r.ID.Device(), "device", x.OldHash)
	}
	return x, nil
}

func (c *Core) replacementCheckin(ctx context.Context, r *mdm.Request, ck *mdm.Checkin) (bool, error) {
	x, hash, err := c.replacement(ctx, r)
	if err != nil || x == nil {
		return false, err
	}
	_, authenticate := ck.Message.(*checkin.Authenticate)
	if x.State != storage.ReplacementPending {
		if hash == x.CandidateHash {
			if x.State != storage.ReplacementCommitted {
				return true, wrapCode(CodeForbidden, ErrCertMismatch)
			}
			if authenticate && !r.ID.Channel.IsUser() {
				return true, nil
			}
		}
		return false, nil
	}
	if hash == x.OldHash {
		// A repeated Authenticate from the working profile is not a new enrollment.
		return authenticate && !r.ID.Channel.IsUser(), nil
	}
	if x.CandidateHash == "" || hash != x.CandidateHash {
		return true, wrapCode(CodeForbidden, ErrCertMismatch)
	}
	ch := storage.ReplacementChange{ID: x.ID, Hash: hash}
	switch m := ck.Message.(type) {
	case *checkin.Authenticate:
		if r.ID.Channel.IsUser() {
			return true, wrapCode(CodeForbidden, ErrCertMismatch)
		}
		ch.Op, ch.Raw = "authenticate", ck.Raw
	case *checkin.TokenUpdate:
		if c.requireUserAuth && r.ID.Channel == mdm.ChannelUser {
			s, err := c.store.UserAuth(ctx, r.ID)
			if err != nil || s.AuthToken == "" {
				return true, wrapCode(CodeForbidden, ErrUserAuthRequired)
			}
		}
		ch.Op, ch.Token = "token", &storage.ReplacementToken{ID: r.ID, Message: m, Raw: ck.Raw}
	default:
		return true, wrapCode(CodeForbidden, ErrCertMismatch)
	}
	_, err = c.transitionReplacement(ctx, r, ch)
	return true, err
}

func (c *Core) replacementConnect(ctx context.Context, r *mdm.Request, resp *mdm.Response) (*mdm.Command, bool, error) {
	x, hash, err := c.replacement(ctx, r)
	if err != nil || x == nil || x.State != storage.ReplacementPending {
		return nil, false, err
	}
	if hash != x.OldHash && (x.CandidateHash == "" || hash != x.CandidateHash) {
		return nil, true, wrapCode(CodeForbidden, ErrCertMismatch)
	}
	if r.ID.Channel.IsUser() {
		if hash == x.CandidateHash {
			return nil, true, nil
		}
		return nil, false, nil
	}
	if !resp.IsIdle() {
		if resp.CommandUUID != x.Command.UUID {
			if hash != x.OldHash {
				return nil, true, wrapCode(CodeForbidden, ErrCertMismatch)
			}
			// Finish an unrelated command already in flight before the attempt.
			if err := c.store.StoreResult(ctx, r.ID, resp, c.clock.Now()); err != nil && !errors.Is(err, storage.ErrNotFound) {
				return nil, true, wrapCode(codeForStorage(err), err)
			}
		} else {
			x, err = c.transitionReplacement(ctx, r, storage.ReplacementChange{Op: "result", ID: x.ID, Hash: hash, Response: resp})
			if err != nil {
				return nil, true, err
			}
			c.publish(ctx, event.CommandResult, r.ID, "device", resp)
			return nil, true, nil
		}
	}
	if x.Acknowledged || hash != x.OldHash {
		return nil, true, nil
	}
	x, err = c.transitionReplacement(ctx, r, storage.ReplacementChange{Op: "deliver", ID: x.ID, Hash: hash})
	if err != nil {
		return nil, true, err
	}
	c.publish(ctx, event.CommandSent, r.ID, "server", &x.Command)
	return &x.Command, true, nil
}
