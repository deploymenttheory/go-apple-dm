package adminauth

import (
	"fmt"
	"slices"
	"time"
)

// PrincipalChange describes a credential mutation committed with the store's
// last-active-root check. It contains a token digest, never the bearer token.
type PrincipalChange struct {
	// Op is update, rotate, revoke or delete.
	Op              string
	Roles           []string
	Root            bool
	Digest, TokenID string
	ExpiresAt       time.Time
}

// Apply computes the new record. Stores must atomically check whether this
// removes the last active root before committing it. Delete returns an inactive
// record for that check; the store then removes the row.
func (c PrincipalChange) Apply(p Principal, now time.Time) (Principal, error) {
	p.UpdatedAt = now
	switch c.Op {
	case "update":
		for _, role := range c.Roles {
			if !ValidName(role) {
				return Principal{}, fmt.Errorf("%w: role name", ErrInvalid)
			}
		}
		p.Roles = slices.Clone(c.Roles)
		slices.Sort(p.Roles)
		p.Roles, p.Root = slices.Compact(p.Roles), c.Root
	case "rotate":
		if c.Digest == "" || c.TokenID == "" {
			return Principal{}, fmt.Errorf("%w: missing credential digest or id", ErrInvalid)
		}
		p.TokenID, p.TokenAt, p.ExpiresAt = c.TokenID, now, c.ExpiresAt
	case "revoke", "delete":
		p.TokenID, p.TokenAt, p.ExpiresAt = "", time.Time{}, time.Time{}
	default:
		return Principal{}, fmt.Errorf("%w: unknown principal change", ErrInvalid)
	}
	return p, nil
}
