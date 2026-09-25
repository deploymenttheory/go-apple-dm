package dep

import (
	"bytes"
	"context"
	"crypto/rand"
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
// persisted. It rejects changes to an established consumer key or Apple identity.
// Validation failures preserve credentials and policy; ErrTermsNotSigned and
// ErrTokenInvalid update state only when the rejected credentials are still current.
func (c *Client) StoreTokens(ctx context.Context, name string, t Tokens) (*AccountDetail, error) {
	v, err := c.validateTokens(ctx, name, t, false)
	if err != nil {
		return nil, err
	}
	if err := c.commitTokens(ctx, v, false, "", nil); err != nil {
		return nil, err
	}
	return v.detail, nil
}

// validatedTokens separates a validation baseline from the fields it may replace.
type validatedTokens struct {
	name     string
	baseline *Account
	tokens   Tokens
	session  string
	detail   *AccountDetail
}

// validateTokens performs remote validation without replacing local account policy.
func (c *Client) validateTokens(ctx context.Context, name string, t Tokens, force bool) (*validatedTokens, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: empty account name", ErrInvalid)
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	now := c.cfg.Clock.Now()
	if t.AccessTokenExpiry != nil && !now.Before(*t.AccessTokenExpiry) {
		return nil, fmt.Errorf("%w: %s", ErrTokenExpired, t.AccessTokenExpiry.UTC().Format(time.RFC3339))
	}
	existing, err := c.cfg.Store.GetAccount(ctx, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err := checkConsumerKey(existing, t, force); err != nil {
		return nil, err
	}
	acct := &Account{Name: name, CreatedAt: now}
	if existing != nil {
		acct = existing
	}
	session, err := c.session(ctx, t, acct.Protocol())
	if err != nil {
		if existing != nil && sameCredentials(existing.Tokens(), t) {
			switch {
			case errors.Is(err, ErrTokenInvalid):
				err = c.markState(ctx, existing, err, AccountState{TermsExpired: existing.State.TermsExpired, TokenInvalid: true})
			case errors.Is(err, ErrTermsNotSigned):
				err = c.markState(ctx, existing, err, AccountState{TermsExpired: true, TokenInvalid: existing.State.TokenInvalid})
			}
		}
		return nil, err
	}
	detail, err := c.accountWith(ctx, session, acct.Protocol())
	if err != nil {
		return nil, err
	}
	return &validatedTokens{name: name, baseline: existing, tokens: t, session: session, detail: detail}, nil
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
		return nil, fmt.Errorf("GET /account: %w", err)
	}
	defer func(body io.Closer) { _ = body.Close() }(resp.Body)
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read /account: %w", err)
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
func (c *Client) cacheSession(acct *Account, token string) {
	s := c.sessionFor(acct.Name)
	s.mu.Lock()
	s.token = token
	s.binding = binding(acct)
	s.mu.Unlock()
}

// ImportOptions tune ImportToken.
type ImportOptions struct {
	// Force permits replacement of an established Apple server identity. An actual
	// identity change atomically resets its inventory, profiles and worker state.
	Force bool
}

// ImportToken decrypts a server token file (.p7m) with the account's
// staged keypair, or its current keypair when nothing is staged (a token
// renewed against the same certificate), validates the tokens with
// /session and /account, and writes the account, the session, and the
// upstaged keypair in one transaction. Without Force, established identity or
// consumer-key changes are rejected. Force resets Apple-derived state only when
// identity changes; a same-server renewal preserves it. Validation errors only
// update failure flags for rejected credentials that remain current.
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
	v, err := c.validateTokens(ctx, name, t, o.Force)
	if err != nil {
		return nil, err
	}
	if err := c.commitTokens(ctx, v, o.Force, stage, kp); err != nil {
		return nil, err
	}
	return v.detail, nil
}

// sameCredentials compares the OAuth credentials, not their expiry or metadata.
func sameCredentials(a, b Tokens) bool {
	return a.ConsumerKey == b.ConsumerKey && a.ConsumerSecret == b.ConsumerSecret && a.AccessToken == b.AccessToken && a.AccessSecret == b.AccessSecret
}

// checkConsumerKey retains the explicit opt-in required to replace a consumer key.
func checkConsumerKey(a *Account, t Tokens, force bool) error {
	if a != nil && a.ConsumerKey != "" && a.ConsumerKey != t.ConsumerKey && !force {
		return ErrConsumerKeyMismatch
	}
	return nil
}

// identityChanged uses established Apple identifiers, falling back to the consumer key
// when the old account has no server UUID. Omitted metadata is not a new identity.
func identityChanged(a *Account, t Tokens, d *AccountDetail) bool {
	if a == nil {
		return false
	}
	return (a.ServerUUID != "" && d.ServerUUID != "" && a.ServerUUID != d.ServerUUID) ||
		(a.OrgID != "" && d.OrgID != "" && a.OrgID != d.OrgID) ||
		(a.ConsumerKey != "" && a.ConsumerKey != t.ConsumerKey && (a.ServerUUID == "" || d.ServerUUID == ""))
}

// commitTokens rechecks validation and keypair baselines under the account lock,
// then merges a renewal or replaces all Apple-derived state atomically.
func (c *Client) commitTokens(ctx context.Context, v *validatedTokens, force bool, stage Stage, kp *Keypair) error {
	var committed *Account
	err := c.cfg.Store.Update(ctx, func(tx Tx) error {
		var current *Account
		var err error
		if v.baseline != nil {
			current, err = lockedAccount(ctx, tx, v.baseline)
		} else {
			// The name lock also serializes creation while the account is absent.
			if err := tx.LockAccount(ctx, v.name); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			current, err = tx.GetAccount(ctx, v.name)
			if errors.Is(err, ErrNotFound) {
				current, err = nil, nil
			} else if err == nil {
				err = fmt.Errorf("%w: account created during validation", ErrConflict)
			}
		}
		if err != nil {
			return err
		}
		if err := checkConsumerKey(current, v.tokens, force); err != nil {
			return err
		}
		reset := identityChanged(current, v.tokens, v.detail)
		if reset && !force {
			return fmt.Errorf("%w: Apple server identity changed; forced token import required", ErrConflict)
		}
		if reset && v.detail.ServerUUID == "" {
			return fmt.Errorf("%w: replacement account has no server UUID", ErrInvalid)
		}
		if kp != nil {
			stored, err := tx.Keypair(ctx, v.name, stage)
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: token keypair changed", ErrConflict)
			}
			if err != nil {
				return err
			}
			if !bytes.Equal(kp.CertPEM, stored.CertPEM) || !bytes.Equal(kp.KeyPEM, stored.KeyPEM) || !kp.CreatedAt.Equal(stored.CreatedAt) {
				return fmt.Errorf("%w: token keypair changed", ErrConflict)
			}
		}
		now := c.cfg.Clock.Now()
		acct := &Account{Name: v.name, CreatedAt: now}
		if current != nil {
			acct = current.Clone()
		}
		if reset {
			if err := resetAccount(ctx, tx, current, now); err != nil {
				return err
			}
			acct.ProfileUUID, acct.ServerUUID, acct.OrgID = "", "", ""
		}
		acct.SetTokens(v.tokens)
		acct.OrgName, acct.ServerName, acct.AdminID = v.detail.OrgName, v.detail.ServerName, v.detail.AdminID
		if v.detail.OrgID != "" {
			acct.OrgID = v.detail.OrgID
		}
		if v.detail.ServerUUID != "" {
			acct.ServerUUID = v.detail.ServerUUID
		}
		acct.Limits, acct.State, acct.UpdatedAt = v.detail.Limits(), AccountState{}, now
		if err := tx.PutAccount(ctx, acct); err != nil {
			return err
		}
		if err := tx.SetSession(ctx, v.name, v.session); err != nil {
			return err
		}
		if stage == StageStaged {
			if err := tx.UpstageKeypair(ctx, v.name); err != nil {
				return err
			}
		}
		committed = acct
		return nil
	})
	if err == nil {
		c.cacheSession(committed, v.session)
	}
	return err
}

// resetAccount removes server-owned state while preserving local configuration and keys.
// Its fresh, nonzero cursor fences responses from the previous server.
func resetAccount(ctx context.Context, tx Tx, a *Account, now time.Time) error {
	cur, err := tx.Cursor(ctx, a.Name)
	if err != nil {
		return err
	}
	keys := map[Stage]*Keypair{}
	for _, stage := range []Stage{StageCurrent, StageStaged} {
		kp, err := tx.Keypair(ctx, a.Name, stage)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		keys[stage] = kp
	}
	if err := tx.DeleteAccount(ctx, a.Name); err != nil {
		return err
	}
	// Recreate before dependent rows for stores that enforce foreign keys.
	if err := tx.PutAccount(ctx, &Account{Name: a.Name, ProtocolVersion: a.ProtocolVersion, CreatedAt: a.CreatedAt, UpdatedAt: now}); err != nil {
		return err
	}
	for stage, kp := range keys {
		if err := tx.PutKeypair(ctx, a.Name, stage, kp); err != nil {
			return err
		}
	}
	return tx.SetCursor(ctx, a.Name, Cursor{Phase: PhaseFetch, Revision: cur.Revision + 1, Generation: rand.Text(), UpdatedAt: now})
}
