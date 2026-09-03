package sqlcommon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/storage"
)

// StorePushCert implements storage.PushCertStore (decision record 0015).
// The private key is sealed when a keyring is configured.
func (s *Store) StorePushCert(ctx context.Context, topic string, certPEM, keyPEM []byte, at time.Time) (storage.PushCert, error) {
	rec, err := storage.ValidatePushCert(topic, certPEM, keyPEM, at)
	if err != nil {
		return storage.PushCert{}, err
	}
	sealed, err := s.seal(purposePushKey, rec.Topic, rec.KeyPEM)
	if err != nil {
		return storage.PushCert{}, err
	}
	at = at.UTC()
	err = s.tx(ctx, func(q querier) error {
		var version int64
		err := q.QueryRowContext(ctx, s.q("SELECT version FROM push_certs WHERE topic = ? "+s.d.ForUpdate), rec.Topic).Scan(&version)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			version = 1
			if _, err := q.ExecContext(ctx, s.q("INSERT INTO push_certs (topic, cert_pem, key_pem, not_after, version, updated_at) VALUES (?, ?, ?, ?, ?, ?)"),
				rec.Topic, rec.CertPEM, sealed, rec.NotAfter, version, at); err != nil {
				return wrap("insert push certificate", err)
			}
		case err != nil:
			return wrap("lookup push certificate", err)
		default:
			version++
			if _, err := q.ExecContext(ctx, s.q("UPDATE push_certs SET cert_pem = ?, key_pem = ?, not_after = ?, version = ?, updated_at = ? WHERE topic = ?"),
				rec.CertPEM, sealed, rec.NotAfter, version, at, rec.Topic); err != nil {
				return wrap("update push certificate", err)
			}
		}
		rec.Version, rec.UpdatedAt = version, at
		return nil
	})
	if err != nil {
		return storage.PushCert{}, err
	}
	rec.KeyPEM = nil
	return rec, nil
}

// PushCert implements storage.PushCertStore.
func (s *Store) PushCert(ctx context.Context, topic string) (*storage.PushCert, error) {
	var c storage.PushCert
	var key []byte
	var notAfter, updated time.Time
	err := s.db.QueryRowContext(ctx, s.q("SELECT topic, cert_pem, key_pem, not_after, version, updated_at FROM push_certs WHERE topic = ?"), topic).
		Scan(&c.Topic, &c.CertPEM, &key, &notAfter, &c.Version, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: push certificate for %q", storage.ErrNotFound, topic)
	}
	if err != nil {
		return nil, wrap("push certificate", err)
	}
	if c.KeyPEM, err = s.open(purposePushKey, c.Topic, key); err != nil {
		return nil, err
	}
	c.NotAfter, c.UpdatedAt = notAfter.UTC(), updated.UTC()
	return &c, nil
}

// PushCerts implements storage.PushCertStore.
func (s *Store) PushCerts(ctx context.Context) ([]storage.PushCert, error) {
	rows, err := s.db.QueryContext(ctx, s.q("SELECT topic, cert_pem, not_after, version, updated_at FROM push_certs ORDER BY topic"))
	if err != nil {
		return nil, wrap("list push certificates", err)
	}
	defer rows.Close()
	out := []storage.PushCert{}
	for rows.Next() {
		var c storage.PushCert
		var notAfter, updated time.Time
		if err := rows.Scan(&c.Topic, &c.CertPEM, &notAfter, &c.Version, &updated); err != nil {
			return nil, wrap("scan push certificate", err)
		}
		c.NotAfter, c.UpdatedAt = notAfter.UTC(), updated.UTC()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list push certificates", err)
	}
	return out, nil
}

// PushCertVersion implements storage.PushCertStore.
func (s *Store) PushCertVersion(ctx context.Context, topic string) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, s.q("SELECT version FROM push_certs WHERE topic = ?"), topic).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: push certificate for %q", storage.ErrNotFound, topic)
	}
	if err != nil {
		return 0, wrap("push certificate version", err)
	}
	return v, nil
}
