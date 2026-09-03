package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// certificateCols index what CertificateQuery filters on plus the two
// times an operator sorts an expiry report by. udid is the attested
// device's, not the binding's: the question the admin API answers is which
// hardware holds an identity, and the attestation is what said so.
var certificateCols = []string{"id", "account_id", "order_id", "serial", "udid", "not_after", "issued_at", "record"}

// PutCertificate implements acme.Writer.
func (t *txStore) PutCertificate(ctx context.Context, c *acme.Certificate) error {
	if c == nil {
		return fmt.Errorf("%w: nil certificate", acme.ErrInvalid)
	}
	if err := validID("certificate id", c.ID); err != nil {
		return err
	}
	raw, err := encode("certificate", c)
	if err != nil {
		return err
	}
	return t.put(ctx, "put certificate", "acme_certificates", certificateCols,
		[]any{c.ID, c.AccountID, c.OrderID, c.Device.SerialNumber, c.Device.UDID, nullTime(c.NotAfter), nullTime(c.IssuedAt), raw})
}

// GetCertificate implements acme.Reader.
func (t *txStore) GetCertificate(ctx context.Context, id string) (*acme.Certificate, error) {
	var c acme.Certificate
	if err := t.get(ctx, "get certificate", "acme_certificates", "certificate", id, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ListCertificates implements acme.Reader. Every non-empty field of the
// query must match, which is the admin API asking for one device's
// identities rather than for a page of everything.
func (t *txStore) ListCertificates(
	ctx context.Context,
	q acme.CertificateQuery,
	p storage.Page,
) (storage.Result[acme.Certificate], error) {
	var where []string
	var args []any
	for _, f := range []struct {
		column string
		value  string
	}{{"serial", q.DeviceSerial}, {"udid", q.UDID}, {"account_id", q.AccountID}} {
		if f.value != "" {
			where = append(where, f.column+" = ?")
			args = append(args, f.value)
		}
	}
	where, args = after(where, args, "id", p)
	query := "SELECT id, record FROM acme_certificates"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	return keyset(ctx, t, "list certificates", query+" ORDER BY id", args, p, func(rows *sql.Rows) (acme.Certificate, string, error) {
		var c acme.Certificate
		id, err := scanRecord(rows, "acme_certificates", &c)
		return c, id, err
	})
}

// Store methods outside Update run against the pool.

// GetCertificate implements acme.Reader.
func (s *Store) GetCertificate(ctx context.Context, id string) (*acme.Certificate, error) {
	return s.view().GetCertificate(ctx, id)
}

// ListCertificates implements acme.Reader.
func (s *Store) ListCertificates(
	ctx context.Context,
	q acme.CertificateQuery,
	p storage.Page,
) (storage.Result[acme.Certificate], error) {
	return s.view().ListCertificates(ctx, q, p)
}
