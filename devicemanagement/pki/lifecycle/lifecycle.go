// Package lifecycle manages certificate setup and renewal with persistent state.
// Applications supply storage, trust policy and scheduling; no background work
// starts merely by constructing a Manager. Repository values contain secrets and
// must be encrypted by a persistent storage adapter.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

type Kind string

const (
	Vendor Kind = "vendor"
	Push   Kind = "push"
	HTTPS  Kind = "https"
	Issuer Kind = "issuer"
)

var (
	ErrInvalid  = errors.New("lifecycle: invalid input")
	ErrConflict = errors.New("lifecycle: conflicting transition")
	ErrNotFound = state.ErrNotFound
)

// Repository supplies atomic, serialized transitions and authoritative time.
type Repository = state.Store

// Request identifies a logical identity. Retrying it resumes its pending revision.
type Request struct {
	ID       string    `json:"id"`
	Kind     Kind      `json:"kind"`
	Subject  pkix.Name `json:"subject"`
	DNSNames []string  `json:"dnsNames,omitempty"`
	Account  string    `json:"account,omitempty"`
}

// Revision is safe for administrative output. Key material is kept separately.
type Revision struct {
	ID          string    `json:"id"`
	Phase       string    `json:"phase"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	NotBefore   time.Time `json:"notBefore,omitempty"`
	NotAfter    time.Time `json:"notAfter,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	ActivatedAt time.Time `json:"activatedAt,omitempty"`
}

// Identity describes active and pending revisions independently.
type Identity struct {
	Request
	Active     string     `json:"active,omitempty"`
	Pending    string     `json:"pending,omitempty"`
	Topic      string     `json:"topic,omitempty"`
	Revisions  []Revision `json:"revisions"`
	Generation int64      `json:"generation"`
	NextAction string     `json:"nextAction,omitempty"`
	RenewAt    time.Time  `json:"renewAt,omitempty"`
	Severity   string     `json:"severity,omitempty"`
}

// Material is a privileged application interface. Never serialize it in an
// administrative response. The repository adapter encrypts its encoded value.
type Material struct {
	Key, CSR, Certificate, SignedRequest []byte
}

type storedRevision struct {
	Revision
	Material
}
type record struct {
	Request
	Active, Pending, Topic string
	Generation             int64
	Revisions              []storedRevision
}

// Publish commits a runtime projection inside the same repository transaction.
// Returning an error must roll back both publication and the workflow transition.
type Publish func(context.Context, state.Tx, Identity, Material) error

type Manager struct {
	Store   Repository
	Trust   Trust
	Publish Publish
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

const prefix = "pki/lifecycle/identity/"

func key(id string) (string, error) {
	if !validID.MatchString(id) {
		return "", fmt.Errorf("%w: identity ID", ErrInvalid)
	}
	return prefix + id, nil
}

func read(ctx context.Context, s state.Reader, id string) (record, error) {
	k, err := key(id)
	if err != nil {
		return record{}, err
	}
	r, err := s.Get(ctx, k)
	if err != nil {
		return record{}, err
	}
	var out record
	if err = json.Unmarshal(r.Value, &out); err != nil {
		return record{}, fmt.Errorf("lifecycle: decode identity: %w", err)
	}
	return out, nil
}

func write(ctx context.Context, tx state.Tx, r record) error {
	k, err := key(r.ID)
	if err != nil {
		return err
	}
	r.Generation++
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := tx.Put(ctx, state.Record{Key: k, Value: b}); err != nil {
		return err
	}
	return writeActivity(ctx, tx, r)
}

func revision(r *record, id string) (*storedRevision, error) {
	for i := range r.Revisions {
		if r.Revisions[i].ID == id {
			return &r.Revisions[i], nil
		}
	}
	return nil, ErrNotFound
}

func (m *Manager) change(ctx context.Context, id string, fn func(state.Tx, *record) error) (Identity, error) {
	return m.changeLocked(ctx, id, nil, fn)
}

func (m *Manager) changeLocked(ctx context.Context, id string, locks []string, fn func(state.Tx, *record) error) (Identity, error) {
	k, err := key(id)
	if err != nil {
		return Identity{}, err
	}
	var out Identity
	err = m.Store.Update(ctx, append(locks, k), func(tx state.Tx) error {
		r, err := read(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = fn(tx, &r); err != nil {
			return err
		}
		if err = write(ctx, tx, r); err != nil {
			return err
		}
		r.Generation++
		out = view(r, tx.Now())
		return nil
	})
	return out, err
}

// Begin creates a pending key and CSR without disturbing the active identity.
func (m *Manager) Begin(ctx context.Context, req Request) (Identity, error) {
	k, err := key(req.ID)
	if err != nil {
		return Identity{}, err
	}
	if req.Kind != Vendor && req.Kind != Push && req.Kind != HTTPS && req.Kind != Issuer {
		return Identity{}, fmt.Errorf("%w: certificate kind", ErrInvalid)
	}
	if req.Subject.CommonName == "" {
		return Identity{}, fmt.Errorf("%w: subject common name", ErrInvalid)
	}
	if r, err := read(ctx, m.Store, req.ID); err == nil {
		if !reflect.DeepEqual(r.Request, req) {
			return Identity{}, ErrConflict
		}
		if r.Pending != "" {
			return m.Get(ctx, req.ID)
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Identity{}, err
	}
	material, err := generate(req)
	if err != nil {
		return Identity{}, err
	}
	var out Identity
	err = m.Store.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := read(ctx, tx, req.ID)
		if errors.Is(err, ErrNotFound) {
			r.Request = req
		} else if err != nil {
			return err
		}
		if !reflect.DeepEqual(r.Request, req) {
			return ErrConflict
		}
		if r.Pending == "" {
			r.Pending = strconv.Itoa(len(r.Revisions) + 1)
			r.Revisions = append(r.Revisions, storedRevision{Revision: Revision{ID: r.Pending, Phase: "awaiting-certificate", CreatedAt: tx.Now()}, Material: material})
			if err = write(ctx, tx, r); err != nil {
				return err
			}
			r.Generation++
		}
		out = view(r, tx.Now())
		return nil
	})
	return out, err
}

func (m *Manager) Get(ctx context.Context, id string) (Identity, error) {
	k, err := key(id)
	if err != nil {
		return Identity{}, err
	}
	var out Identity
	err = m.Store.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := read(ctx, tx, id)
		if err == nil {
			out = view(r, tx.Now())
		}
		return err
	})
	return out, err
}

func (m *Manager) List(ctx context.Context) ([]Identity, error) {
	out := []Identity{}
	after := ""
	for {
		rows, err := m.Store.List(ctx, prefix, after, 100)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			var r record
			if err = json.Unmarshal(row.Value, &r); err != nil {
				return nil, err
			}
			item, err := m.Get(ctx, r.ID)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
			after = row.Key
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
}

// LoadMaterial is for trusted runtime consumers, never a remote export endpoint.
func (m *Manager) LoadMaterial(ctx context.Context, id, rev string) (Material, error) {
	r, err := read(ctx, m.Store, id)
	if err != nil {
		return Material{}, err
	}
	if rev == "" {
		rev = r.Active
	}
	v, err := revision(&r, rev)
	if err != nil {
		return Material{}, err
	}
	return v.Material, nil
}

// Export returns only public submission artifacts or certificate chains.
func (m *Manager) Export(ctx context.Context, id, rev, artifact string) ([]byte, error) {
	r, err := read(ctx, m.Store, id)
	if err != nil {
		return nil, err
	}
	if rev == "" {
		rev = r.Pending
		if rev == "" {
			rev = r.Active
		}
	}
	v, err := revision(&r, rev)
	if err != nil {
		return nil, err
	}
	switch artifact {
	case "csr":
		return v.CSR, nil
	case "signed-request":
		if len(v.SignedRequest) > 0 {
			return v.SignedRequest, nil
		}
	case "certificate":
		if len(v.Certificate) > 0 {
			return v.Certificate, nil
		}
	}
	return nil, fmt.Errorf("%w: artifact unavailable", ErrInvalid)
}

func (m *Manager) Cancel(ctx context.Context, id, rev string) (Identity, error) {
	return m.change(ctx, id, func(_ state.Tx, r *record) error {
		v, err := revision(r, rev)
		if err != nil {
			return err
		}
		if v.Phase == "cancelled" {
			return nil
		}
		if r.Pending != rev {
			return ErrConflict
		}
		v.Phase = "cancelled"
		r.Pending = ""
		return nil
	})
}

func fingerprint(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func view(r record, now time.Time) Identity {
	v := Identity{Request: r.Request, Active: r.Active, Pending: r.Pending, Topic: r.Topic, Generation: r.Generation, Revisions: []Revision{}}
	for _, rev := range r.Revisions {
		v.Revisions = append(v.Revisions, rev.Revision)
		if rev.ID == r.Active {
			lead := 60 * 24 * time.Hour
			if r.Kind == Issuer {
				lead = 180 * 24 * time.Hour
			}
			if third := rev.NotAfter.Sub(rev.NotBefore) / 3; third < lead {
				lead = third
			}
			if r.Kind == HTTPS && lead > 30*24*time.Hour {
				lead = 30 * 24 * time.Hour
			}
			v.RenewAt = rev.NotAfter.Add(-lead)
			if !now.Before(v.RenewAt) {
				v.Severity = "renewal-due"
			}
			for _, days := range []int{30, 14, 7, 1, 0} {
				if !now.Before(rev.NotAfter.Add(-time.Duration(days) * 24 * time.Hour)) {
					v.Severity = "expires-in-" + strconv.Itoa(days) + "-days"
				}
			}
			if !now.Before(rev.NotAfter) {
				v.Severity = "expired"
			}
		}
	}
	if r.Pending != "" {
		rev, _ := revision(&r, r.Pending)
		switch rev.Phase {
		case "ready":
			v.NextAction = "activate revision " + rev.ID
		default:
			switch r.Kind {
			case Vendor:
				v.NextAction = "upload vendor CSR at https://developer.apple.com/account/resources/certificates/list; import returned certificate"
			case Push:
				v.NextAction = "obtain vendor signature for customer CSR"
				if len(rev.SignedRequest) > 0 {
					v.NextAction = "upload signed request at https://identity.apple.com/pushcert/; import returned certificate"
					if r.Active != "" {
						v.NextAction = "Renew the existing certificate at https://identity.apple.com/pushcert/ using the signed request; import the same-topic certificate"
					}
				}
			case HTTPS:
				v.NextAction = "complete public ACME issuance or import the HTTPS certificate"
			case Issuer:
				v.NextAction = "create or import the enrollment CA"
			}
		}
	} else if v.Severity != "" {
		v.NextAction = "begin renewal"
	}
	return v
}
