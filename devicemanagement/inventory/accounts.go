package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
)

// Account is a named AxM connection. Private keys are stored separately and are
// never part of the account's public JSON representation.
type Account struct {
	ID                   string        `json:"id"`
	Name                 string        `json:"name"`
	ClientID             string        `json:"client_id"`
	KeyID                string        `json:"key_id"`
	Scope                string        `json:"scope,omitempty"`
	BaseURL              string        `json:"base_url,omitempty"`
	TokenURL             string        `json:"token_url,omitempty"`
	Enabled              bool          `json:"enabled"`
	Revision             int64         `json:"revision"`
	CreatedAt            time.Time     `json:"created_at"`
	UpdatedAt            time.Time     `json:"updated_at"`
	VerifiedAt           *time.Time    `json:"verified_at,omitempty"`
	DEPAccounts          []string      `json:"dep_accounts,omitempty"`
	AppleOrgID           string        `json:"apple_org_id,omitempty"`
	DeviceTTL            time.Duration `json:"device_ttl"`
	CoverageTTL          time.Duration `json:"coverage_ttl"`
	CoverageBudget       int           `json:"coverage_budget,omitempty"`
	NeverRefetchCoverage bool          `json:"never_refetch_coverage,omitempty"`
}

// SaveAccount compares revisions, preserving account identity across key rotation.
// A replacement key is mandatory for a changed client identity.
func (r *Repository) SaveAccount(ctx context.Context, a Account, key []byte, now time.Time) (Account, error) {
	if a.Name == "" || a.ClientID == "" || a.KeyID == "" || a.DeviceTTL < 0 || a.CoverageTTL < 0 || a.CoverageBudget < 0 {
		return a, ErrInvalid
	}
	if len(key) > 0 {
		if _, e := axm.ParseKey(key); e != nil {
			return a, e
		}
	}
	if a.ID != "" && !validID(a.ID) {
		return a, ErrInvalid
	}
	if a.ID == "" {
		a.ID = ID()
	}
	if a.DeviceTTL == 0 {
		a.DeviceTTL = 24 * time.Hour
	}
	if a.CoverageTTL == 0 {
		a.CoverageTTL = 7 * 24 * time.Hour
	}
	err := r.Backend.Update(ctx, func(tx Tx) error {
		old, e := get[Account](ctx, tx, "account/"+a.ID)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
		if a.Revision != old.Revision {
			return ErrConflict
		}
		changed := old.ClientID != a.ClientID || old.Scope != a.Scope || old.BaseURL != a.BaseURL || old.TokenURL != a.TokenURL || old.KeyID != a.KeyID
		if changed && len(key) == 0 {
			return ErrInvalid
		}
		if changed || len(key) > 0 {
			a.VerifiedAt = nil
		} else {
			a.VerifiedAt = old.VerifiedAt
		}
		a.CreatedAt = old.CreatedAt
		if a.CreatedAt.IsZero() {
			a.CreatedAt = now
		}
		a.UpdatedAt = now
		a.Revision++
		if len(key) > 0 {
			if e := put(ctx, tx, "credential/"+a.ID, key); e != nil {
				return e
			}
		}
		return put(ctx, tx, "account/"+a.ID, a)
	})
	return a, err
}

// Account returns nonsecret connection metadata.
func (r *Repository) Account(ctx context.Context, id string) (Account, error) {
	return get[Account](ctx, r.Backend, "account/"+id)
}

// PrivateKey is for the authenticated connector, never an administrative read response.
func (r *Repository) PrivateKey(ctx context.Context, id string) ([]byte, error) {
	return get[[]byte](ctx, r.Backend, "credential/"+id)
}

// Accounts lists configured connections without their keys.
func (r *Repository) Accounts(ctx context.Context) ([]Account, error) {
	out := []Account{}
	err := walk(ctx, r.Backend, "account/", func(e Entry) error {
		var a Account
		if err := json.Unmarshal(e.Value, &a); err != nil {
			return err
		}
		out = append(out, a)
		return nil
	})
	return out, err
}

// VerifyAccount stamps only the revision whose credentials were successfully verified.
func (r *Repository) VerifyAccount(ctx context.Context, id string, revision int64, at time.Time) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		a, e := get[Account](ctx, tx, "account/"+id)
		if e != nil {
			return e
		}
		if a.Revision != revision {
			return ErrConflict
		}
		a.VerifiedAt = &at
		return put(ctx, tx, "account/"+id, a)
	})
}

// validID keeps account and preset keys distinct from internal document namespaces.
func validID(id string) bool {
	return len(id) > 0 && len(id) <= 128 && strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) < 0
}

// MarshalJSON presents durations in human-readable Go duration syntax.
func (a Account) MarshalJSON() ([]byte, error) {
	type plain Account
	raw, e := json.Marshal(plain(a))
	if e != nil {
		return nil, e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		return nil, e
	}
	fields["device_ttl"], _ = json.Marshal(a.DeviceTTL.String())
	fields["coverage_ttl"], _ = json.Marshal(a.CoverageTTL.String())
	return json.Marshal(fields)
}

// UnmarshalJSON accepts duration strings and the original nanosecond numeric representation.
func (a *Account) UnmarshalJSON(raw []byte) error {
	type plain Account
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		return e
	}
	for _, name := range []string{"device_ttl", "coverage_ttl"} {
		value, ok := fields[name]
		if !ok {
			continue
		}
		var text string
		if json.Unmarshal(value, &text) == nil {
			duration, e := time.ParseDuration(text)
			if e != nil {
				return ErrInvalid
			}
			fields[name], _ = json.Marshal(int64(duration))
		}
	}
	normalized, e := json.Marshal(fields)
	if e != nil {
		return e
	}
	return json.Unmarshal(normalized, (*plain)(a))
}
