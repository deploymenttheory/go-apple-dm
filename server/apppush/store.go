// Package apppush persists app APNs identities separately from MDM credentials.
package apppush

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/state"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

const prefix = "apppush/v1/"

// Metadata is safe to return through the admin API; it never contains a key.
type Metadata struct {
	Topic               string
	NotBefore, NotAfter time.Time
	Version             int64
}
type record struct {
	Metadata
	Cert, Key []byte
}

// Store uses the server's transactional state backend. Persistent backends must
// supply Keys. Memory may omit it. The record key is authenticated as AAD.
type Store struct {
	State state.Store
	Keys  *crypt.Keyring
	Now   func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func key(topic string) (string, error) {
	k := prefix + topic
	if topic == "" || !state.ValidKey(k) {
		return "", state.ErrInvalid
	}
	return k, nil
}

func (s *Store) decode(r state.Record) (record, error) {
	b := r.Value
	var err error
	if s.Keys != nil {
		b, _, err = s.Keys.Open(b, []byte(r.Key))
		if err != nil {
			return record{}, wrapError(err)
		}
	}
	var v record
	err = json.Unmarshal(b, &v)
	return v, wrapError(err)
}

// Put validates before replacing a credential, incrementing its version atomically.
func (s *Store) Put(ctx context.Context, topic string, cert, privateKey []byte) (Metadata, error) {
	p, err := pushcert.ParseApp(cert, privateKey)
	if err != nil {
		return Metadata{}, fmt.Errorf("%w: %w", state.ErrInvalid, err)
	}
	if topic == "" {
		topic = p.Topic
	}
	if err := pushcert.Validate(p.TLS, topic, false, s.now()); err != nil {
		return Metadata{}, fmt.Errorf("%w: %w", state.ErrInvalid, err)
	}
	k, err := key(topic)
	if err != nil {
		return Metadata{}, wrapError(err)
	}
	cert, err = pushcert.PEM(cert)
	if err != nil {
		return Metadata{}, wrapError(err)
	}
	v := record{
		Metadata: Metadata{
			Topic:     topic,
			NotBefore: p.TLS.Leaf.NotBefore,
			NotAfter:  p.TLS.Leaf.NotAfter,
			Version:   1,
		},
		Cert: cert,
		Key:  privateKey,
	}
	err = s.State.Update(ctx, []string{k}, func(tx state.Tx) error {
		old, err := tx.Get(ctx, k)
		if err == nil {
			prev, err := s.decode(old)
			if err != nil {
				return wrapError(err)
			}
			v.Version = prev.Version + 1
		} else if !errors.Is(err, state.ErrNotFound) {
			return wrapError(err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			return wrapError(err)
		}
		if s.Keys != nil {
			b, err = s.Keys.Seal(b, []byte(k))
			if err != nil {
				return wrapError(err)
			}
		}
		return tx.Put(ctx, state.Record{Key: k, Value: b})
	})
	return v.Metadata, wrapError(err)
}

// List returns metadata in topic order, with an opaque next cursor.
func (s *Store) List(ctx context.Context, after string, limit int) ([]Metadata, string, error) {
	if limit < 1 || limit > 1000 {
		return nil, "", state.ErrInvalid
	}
	rows, err := s.State.List(ctx, prefix, after, limit+1)
	if err != nil {
		return nil, "", wrapError(err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[len(rows)-1].Key
	}
	out := make([]Metadata, 0, len(rows))
	for _, r := range rows {
		v, err := s.decode(r)
		if err != nil {
			return nil, "", wrapError(err)
		}
		out = append(out, v.Metadata)
	}
	return out, next, nil
}

// PushCertificate reloads on every send, so replicas see committed renewals.
func (s *Store) PushCertificate(ctx context.Context, topic string) (tls.Certificate, error) {
	k, err := key(topic)
	if err != nil {
		return tls.Certificate{}, wrapError(err)
	}
	r, err := s.State.Get(ctx, k)
	if err != nil {
		return tls.Certificate{}, wrapError(err)
	}
	v, err := s.decode(r)
	if err != nil {
		return tls.Certificate{}, wrapError(err)
	}
	p, err := pushcert.ParseApp(v.Cert, v.Key)
	return p.TLS, wrapError(err)
}
