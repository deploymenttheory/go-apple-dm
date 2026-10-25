package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// adeEvidence retains the originally validated signer and CMS, rather than
// reconstructing trust from the browser channel or from device identifiers.
// SQL protocol state seals this entire value using the configured keyring.
type adeEvidence struct {
	Raw        []byte     `json:"Raw"`
	Signer     []byte     `json:"Signer"`
	Verified   bool       `json:"Verified"`
	Origin     ade.Origin `json:"Origin"`
	ReceivedAt time.Time  `json:"ReceivedAt"`
	ExpiresAt  time.Time  `json:"ExpiresAt"`
}

func evidenceFor(p *ade.Parsed, received time.Time) (adeEvidence, error) {
	if p == nil || len(p.Raw) == 0 {
		return adeEvidence{}, ade.ErrNoMachineInfo
	}
	e := adeEvidence{Raw: p.Raw, Verified: p.Verified, Origin: p.Origin, ReceivedAt: received}
	if p.Signer != nil {
		e.Signer = p.Signer.Raw
	}
	return e, nil
}

// parsed validates the retained signature using its originally trusted signer.
// The sealed Verified bit records the original chain validation; trusting the
// signer here does not establish fresh organizational ownership or attestation.
func (e adeEvidence) parsed(ctx context.Context, serial, udid string, now time.Time) (*ade.Parsed, error) {
	if !e.Verified || len(e.Signer) == 0 || e.ReceivedAt.IsZero() || e.ExpiresAt.IsZero() || !now.Before(e.ExpiresAt) || !e.ReceivedAt.Before(e.ExpiresAt) {
		return nil, ade.ErrUnverified
	}
	if e.Origin != ade.OriginHeader && e.Origin != ade.OriginQuery && e.Origin != ade.OriginBody {
		return nil, ade.ErrMalformed
	}
	cert, err := x509.ParseCertificate(e.Signer)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://retained.invalid", bytes.NewReader(e.Raw))
	if err != nil {
		return nil, err
	}
	p, err := ade.ParseMachineInfo(req, ade.ParseOptions{Anchors: []*x509.Certificate{cert}})
	if err != nil {
		return nil, err
	}
	if err = p.Validate(false); err != nil {
		return nil, err
	}
	if p.SERIAL != serial || (udid != "" && p.UDID != udid) || !bytes.Equal(p.Signer.Raw, e.Signer) {
		return nil, ade.ErrUnverified
	}
	p.Origin = e.Origin
	return p, nil
}

// adeStateStore backs the library's latest-per-serial seam with the same shared
// protocol backend. Browser completion uses its immutable state snapshot below.
type adeStateStore struct{ backend state.Store }

func adeStateKey(serial string) string {
	sum := sha256.Sum256([]byte(serial))
	return "ade/machineinfo/" + hex.EncodeToString(sum[:])
}

func (s adeStateStore) Put(ctx context.Context, r *ade.Record) error {
	if r == nil || r.Parsed == nil || r.Parsed.SERIAL == "" {
		return ade.ErrStore
	}
	e, err := evidenceFor(r.Parsed, r.ReceivedAt)
	if err != nil {
		return err
	}
	key := adeStateKey(r.Parsed.SERIAL)
	return s.backend.Update(ctx, []string{key}, func(tx state.Tx) error {
		e.ExpiresAt = tx.Now().Add(webauth.DefaultStateTTL)
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		return tx.Put(ctx, state.Record{Key: key, Value: b, ExpiresAt: e.ExpiresAt})
	})
}

func (s adeStateStore) Get(ctx context.Context, serial string) (*ade.Record, bool, error) {
	var out *ade.Record
	key := adeStateKey(serial)
	err := s.backend.Update(ctx, []string{key}, func(tx state.Tx) error {
		r, err := tx.Get(ctx, key)
		if err != nil {
			return err
		}
		var e adeEvidence
		if err = json.Unmarshal(r.Value, &e); err != nil {
			return err
		}
		if !e.ExpiresAt.Equal(r.ExpiresAt) {
			return ade.ErrStore
		}
		p, err := e.parsed(ctx, serial, "", tx.Now())
		if err != nil {
			return err
		}
		out = &ade.Record{Parsed: p, ReceivedAt: e.ReceivedAt}
		return nil
	})
	if errors.Is(err, state.ErrNotFound) {
		return nil, false, nil
	}
	return out, err == nil, err
}

// adeBrowserStore commits the exact MachineInfo snapshot and browser grant as
// one existing shared-state record, with one expiration and one consumption.
// Later requests for the same serial cannot replace a pending browser's evidence.
type adeBrowserStore struct{ shared webauth.SharedStore }

func (s adeBrowserStore) Put(ctx context.Context, key string, st webauth.State) error {
	if st.Bound.Extra["flow"] == "ade" {
		var e adeEvidence
		if err := json.Unmarshal([]byte(st.Bound.Extra["machineinfo"]), &e); err != nil {
			return fmt.Errorf("ADE evidence: %w", err)
		}
		e.ExpiresAt = st.ExpiresAt
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		st.Bound.Extra["machineinfo"] = string(b)
	}
	return s.shared.Put(ctx, key, st)
}

func (s adeBrowserStore) Take(ctx context.Context, key, browser string) (webauth.State, error) {
	return s.shared.Take(ctx, key, browser)
}
