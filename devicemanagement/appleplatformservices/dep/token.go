package dep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// StoreTokens validates t with GET /session and GET /account and then
// writes the account: tokens, organisation name, server UUID, admin, and
// per-endpoint limits, with the state cleared and the fresh session
// persisted. Nothing is written when validation fails; ErrTermsNotSigned
// and ErrTokenInvalid are surfaced and, for an existing account, recorded
// in its state.
func (c *Client) StoreTokens(ctx context.Context, name string, t Tokens) (*AccountDetail, error) {
	acct, session, detail, err := c.validateTokens(ctx, name, t)
	if err != nil {
		return nil, err
	}
	err = c.cfg.Store.Update(ctx, func(tx Tx) error {
		if err := tx.PutAccount(ctx, acct); err != nil {
			return err
		}
		return tx.SetSession(ctx, name, session)
	})
	if err != nil {
		return nil, err
	}
	c.cacheSession(name, session)
	return detail, nil
}

// validateTokens runs the /session and /account checks and returns the
// account record to write.
func (c *Client) validateTokens(ctx context.Context, name string, t Tokens) (*Account, string, *AccountDetail, error) {
	if name == "" {
		return nil, "", nil, fmt.Errorf("%w: empty account name", ErrInvalid)
	}
	if err := t.Validate(); err != nil {
		return nil, "", nil, err
	}
	now := c.cfg.Clock.Now()
	if t.AccessTokenExpiry != nil && !now.Before(*t.AccessTokenExpiry) {
		return nil, "", nil, fmt.Errorf("%w: %s", ErrTokenExpired, t.AccessTokenExpiry.UTC().Format(time.RFC3339))
	}
	existing, err := c.cfg.Store.GetAccount(ctx, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, "", nil, err
	}
	acct := &Account{Name: name, CreatedAt: now}
	if existing != nil {
		acct = existing
	}
	session, err := c.session(ctx, t, acct.Protocol())
	if err != nil {
		if existing != nil {
			switch {
			case errors.Is(err, ErrTokenInvalid):
				err = c.markState(ctx, existing, err, AccountState{TermsExpired: existing.State.TermsExpired, TokenInvalid: true})
			case errors.Is(err, ErrTermsNotSigned):
				err = c.markState(ctx, existing, err, AccountState{TermsExpired: true, TokenInvalid: existing.State.TokenInvalid})
			}
		}
		return nil, "", nil, err
	}
	detail, err := c.accountWith(ctx, session, acct.Protocol())
	if err != nil {
		return nil, "", nil, err
	}
	acct.SetTokens(t)
	acct.OrgName, acct.OrgID, acct.ServerName, acct.ServerUUID, acct.AdminID = detail.OrgName, detail.OrgID, detail.ServerName, detail.ServerUUID, detail.AdminID
	acct.Limits = detail.Limits()
	acct.State = AccountState{}
	acct.UpdatedAt = now
	return acct, session, detail, nil
}

// accountWith calls GET /account with a session outside the store, for
// tokens that are not yet stored.
func (c *Client) accountWith(ctx context.Context, session string, protocol int) (*AccountDetail, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL(PathAccount, nil), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("%w: account request: %w", ErrInvalid, err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", contentType)
	req.Header.Set(HeaderSession, session)
	req.Header.Set(HeaderProtocolVersion, strconv.Itoa(protocol))
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dep: GET /account: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("dep: read /account: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newError(resp.StatusCode, body, resp.Header.Get("Retry-After"), c.cfg.Clock.Now())
	}
	var detail AccountDetail
	if err := Unmarshal(body, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// cacheSession records a session obtained outside sessionToken.
func (c *Client) cacheSession(name, token string) {
	s := c.sessionFor(name)
	s.mu.Lock()
	s.token = token
	s.mu.Unlock()
}

// ImportOptions tune ImportToken.
type ImportOptions struct {
	// Force accepts a token whose consumer_key differs from the stored one,
	// which happens when the portal's MDM server was recreated.
	Force bool
}

// ImportToken decrypts a server token file (.p7m) with the account's
// staged keypair, or its current keypair when nothing is staged (a token
// renewed against the same certificate), validates the tokens with
// /session and /account, and writes the account, the session, and the
// upstaged keypair in one transaction. A corrupt file, a consumer key
// mismatch without Force, or a validation failure writes nothing.
func (c *Client) ImportToken(ctx context.Context, name string, p7m []byte, o ImportOptions) (*AccountDetail, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty account name", ErrInvalid)
	}
	stage := StageStaged
	kp, err := c.cfg.Store.Keypair(ctx, name, stage)
	if errors.Is(err, ErrNotFound) {
		stage = StageCurrent
		kp, err = c.cfg.Store.Keypair(ctx, name, stage)
	}
	if err != nil {
		return nil, err
	}
	t, err := Unwrap(p7m, kp)
	if err != nil {
		return nil, err
	}
	existing, err := c.cfg.Store.GetAccount(ctx, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if existing != nil && existing.ConsumerKey != "" && existing.ConsumerKey != t.ConsumerKey && !o.Force {
		return nil, fmt.Errorf("%w: stored %s, token %s", ErrConsumerKeyMismatch, existing.ConsumerKey, t.ConsumerKey)
	}
	acct, session, detail, err := c.validateTokens(ctx, name, t)
	if err != nil {
		return nil, err
	}
	err = c.cfg.Store.Update(ctx, func(tx Tx) error {
		if err := tx.PutAccount(ctx, acct); err != nil {
			return err
		}
		if err := tx.SetSession(ctx, name, session); err != nil {
			return err
		}
		if stage == StageStaged {
			return tx.UpstageKeypair(ctx, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	c.cacheSession(name, session)
	return detail, nil
}
