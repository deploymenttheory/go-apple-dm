package storage

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
)

// ReplacementStore is an optional extension. Every transition locks the device
// enrollment and its replacement together. A successful terminal transition
// commits the candidate pin, token and certificate history in the same transaction;
// it never resets the enrollment, user channels, escrow or command queue.
type ReplacementStore interface {
	TransitionReplacement(
		context.Context,
		mdm.EnrollmentID,
		ReplacementChange,
	) (*Replacement, error)
}

// Replacement contains private handshake state. Administrative APIs must expose
// a redacted view, not this record (the command contains issuance credentials).
type Replacement struct {
	// CSRHash binds SCEP retries to the exact signed request.
	CSRHash                                                       string
	ID, Method, OldHash, CandidateHash, SecretHash, PublicKeyHash string
	State                                                         string
	ExpiresAt, CompletedAt                                        time.Time
	Command                                                       mdm.Command
	Delivered, Acknowledged, Authenticated                        bool
	AuthenticateRaw                                               []byte
	Tokens                                                        []ReplacementToken
}

type ReplacementToken struct {
	ID      mdm.EnrollmentID
	Message *checkin.TokenUpdate
	Raw     []byte
}

type ReplacementChange struct {
	CSRHash                                         string
	Op, ID, Hash, Method, SecretHash, PublicKeyHash string
	At                                              time.Time
	Begin                                           *Replacement
	Raw                                             []byte
	Token                                           *ReplacementToken
	Response                                        *mdm.Response
}

const (
	ReplacementPending   = "pending"
	ReplacementCommitted = "committed"
	ReplacementFailed    = "failed"
	ReplacementCancelled = "cancelled"
	ReplacementExpired   = "expired"
)

// CloneReplacement isolates callback/transport buffers from stored state.
func CloneReplacement(r *Replacement) *Replacement {
	if r == nil {
		return nil
	}
	copy := *r
	copy.Command.Payload = nil // Raw is the canonical, durable command encoding.
	b, _ := json.Marshal(copy)
	var out Replacement
	_ = json.Unmarshal(b, &out)
	return &out
}

// AdvanceReplacement is the shared state machine used under each backend's
// transaction lock. true means the backend must atomically commit candidate state.
func AdvanceReplacement(r **Replacement, e *Enrollment, c ReplacementChange) (bool, error) {
	bad := func(reason string) (bool, error) { return false, fmt.Errorf("%w: replacement %s", ErrConflict, reason) }
	if c.At.IsZero() {
		return false, fmt.Errorf("%w: replacement time is required", ErrInvalid)
	}
	current := *r
	if current != nil && current.State == ReplacementPending && !c.At.Before(current.ExpiresAt) {
		current.State, current.CompletedAt = ReplacementExpired, c.At
	}
	if c.Op == "begin" {
		if current != nil && current.State == ReplacementPending {
			return bad("already pending")
		}
		n := CloneReplacement(c.Begin)
		if n == nil || n.ID == "" || n.Command.UUID != n.ID || n.Command.RequestType != "InstallProfile" ||
			(n.Method != "scep" && n.Method != "acme") || n.OldHash == "" || n.OldHash != e.CertHash || !e.Enabled ||
			!n.ExpiresAt.After(c.At) ||
			n.ExpiresAt.After(c.At.Add(30*time.Minute)) {
			return bad("cannot start")
		}
		n.State = ReplacementPending
		*r = n
		return false, nil
	}
	if c.Op == "read" {
		return false, nil
	}
	if current == nil || current.ID != c.ID {
		return bad("attempt does not match")
	}
	if current.State != ReplacementPending || current.OldHash != e.CertHash || !e.Enabled {
		return bad("is no longer active")
	}
	switch c.Op {
	case "cancel":
		current.State, current.CompletedAt = ReplacementCancelled, c.At
	case "claim":
		if current.Method != "scep" || c.PublicKeyHash == "" || c.CSRHash == "" ||
			(current.PublicKeyHash != "" && current.PublicKeyHash != c.PublicKeyHash) ||
			(current.CSRHash != "" && current.CSRHash != c.CSRHash) ||
			current.SecretHash == "" || subtle.ConstantTimeCompare([]byte(current.SecretHash), []byte(c.SecretHash)) != 1 {
			return bad("issuance authorization refused")
		}
		current.PublicKeyHash, current.CSRHash = c.PublicKeyHash, c.CSRHash
	case "issue":
		if current.Method != c.Method || c.Hash == "" ||
			(current.CandidateHash != "" && current.CandidateHash != c.Hash) ||
			(current.Method == "scep" && (current.PublicKeyHash == "" || current.PublicKeyHash != c.PublicKeyHash)) {
			return bad("certificate issuance refused")
		}
		current.CandidateHash = c.Hash
	case "authenticate":
		if current.CandidateHash == "" || current.CandidateHash != c.Hash {
			return bad("identity refused")
		}
		current.Authenticated = true
		current.AuthenticateRaw = append([]byte(nil), c.Raw...)
	case "token":
		if !current.Authenticated || c.Hash != current.CandidateHash || c.Token == nil ||
			c.Token.Message == nil {
			return bad("token before authentication")
		}
		if _, err := mdm.PushFromTokenUpdate(c.Token.Message); err != nil {
			return bad("invalid token")
		}
		if c.Token.ID.Device() != e.ID.Device() {
			return bad("token belongs to another device")
		}
		found := false
		for i := range current.Tokens {
			if current.Tokens[i].ID == c.Token.ID {
				current.Tokens[i] = *c.Token
				found = true
				break
			}
		}
		if !found {
			current.Tokens = append(current.Tokens, *c.Token)
		}
	case "deliver", "result":
		if c.Hash != current.OldHash &&
			(current.CandidateHash == "" || c.Hash != current.CandidateHash) {
			return bad("command identity refused")
		}
		if c.Op == "deliver" {
			// Only the current identity may receive the credential-bearing profile.
			if c.Hash != current.OldHash {
				return bad("candidate cannot fetch profile")
			}
			current.Delivered = true
		} else {
			if !current.Delivered || c.Response == nil ||
				c.Response.CommandUUID != current.Command.UUID {
				return bad("command result does not match")
			}
			switch c.Response.Status {
			case mdm.StatusAcknowledged:
				current.Acknowledged = true
			case mdm.StatusNotNow:
			case mdm.StatusError, mdm.StatusCommandFormatError:
				current.State, current.CompletedAt = ReplacementFailed, c.At
			default:
				return bad("invalid command result")
			}
		}
	default:
		return false, fmt.Errorf("%w: unknown replacement operation", ErrInvalid)
	}
	if current.State == ReplacementPending && current.Authenticated && current.Acknowledged {
		for _, token := range current.Tokens {
			if token.ID == e.ID {
				current.State, current.CompletedAt = ReplacementCommitted, c.At
				return true, nil
			}
		}
	}
	return false, nil
}

// ApplyReplacementToken updates only fields owned by TokenUpdate, retaining
// escrow and unrelated enrollment state. Call after validating the message.
func ApplyReplacementToken(e *Enrollment, t ReplacementToken, at time.Time) {
	p, _ := mdm.PushFromTokenUpdate(t.Message)
	e.Push, e.TokenUpdateRaw = p, append([]byte(nil), t.Raw...)
	e.Enabled, e.TokenUpdatedAt, e.LastSeenAt, e.DisabledAt = true, at, at, time.Time{}
	m := t.Message
	if len(m.UnlockToken) > 0 {
		e.UnlockToken = append([]byte(nil), m.UnlockToken...)
	}
	if m.UserShortName != nil {
		e.UserShortName = *m.UserShortName
	}
	if m.UserLongName != "" {
		e.UserLongName = m.UserLongName
	}
	if m.EnrollmentUserID != "" {
		e.EnrollmentUserID = m.EnrollmentUserID
	}
	e.NotOnConsole = m.NotOnConsole
}
