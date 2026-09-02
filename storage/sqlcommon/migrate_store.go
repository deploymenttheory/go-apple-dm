package sqlcommon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-mdm/storage"
)

func exportCursor(parentID, id string) string { return parentID + "\x00" + id }

// Export implements storage.MigrationStore (decision record 0017): device
// channels come before the user channels that belong to them because the
// order is (parent_id, id) and device rows have an empty parent. Each
// device row costs two extra reads (bootstrap token and history); this is
// an administrative operation, not a hot path.
func (s *Store) Export(ctx context.Context, p storage.Page) (storage.Result[storage.EnrollmentExport], error) {
	var out storage.Result[storage.EnrollmentExport]
	parent, id, hasCursor := strings.Cut(p.Cursor, "\x00")
	if p.Cursor != "" && !hasCursor {
		return out, fmt.Errorf("%w: bad cursor %q", storage.ErrInvalid, p.Cursor)
	}
	limit := pageLimit(p)
	rows, err := s.db.QueryContext(ctx, s.q(selectEnrollment+" WHERE parent_id > ? OR (parent_id = ? AND id > ?) ORDER BY parent_id, id LIMIT ?"), parent, parent, id, limit+1)
	if err != nil {
		return out, wrap("export enrollments", err)
	}
	defer rows.Close()
	var items []storage.Enrollment
	for rows.Next() {
		e, err := scanEnrollment(rows)
		if err != nil {
			return out, wrap("scan enrollment", err)
		}
		if err := s.openEnrollment(e); err != nil {
			return out, err
		}
		items = append(items, *e)
	}
	if err := rows.Err(); err != nil {
		return out, wrap("export enrollments", err)
	}
	if len(items) > limit {
		items = items[:limit]
		out.NextCursor = exportCursor(items[limit-1].ID.ParentID, items[limit-1].ID.ID)
	}
	for _, e := range items {
		x := storage.EnrollmentExport{Enrollment: e}
		if !e.ID.Channel.IsUser() {
			tok, err := s.BootstrapToken(ctx, e.ID)
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return out, err
			}
			x.BootstrapToken = tok
			if x.CertHistory, err = s.associations(ctx, "ca.enrollment_id = ?", e.ID.ID); err != nil {
				return out, err
			}
		}
		out.Items = append(out.Items, x)
	}
	return out, nil
}

// Import implements storage.MigrationStore.
func (s *Store) Import(ctx context.Context, rec storage.EnrollmentExport) error {
	id := rec.ID
	if err := validID(id); err != nil {
		return err
	}
	for _, a := range rec.CertHistory {
		if a.ID.ID != id.ID || a.Hash == "" {
			return fmt.Errorf("%w: history row for %s in record %s", storage.ErrInvalid, a.ID.ID, id.ID)
		}
	}
	if id.Channel.IsUser() && (rec.CertHash != "" || len(rec.BootstrapToken) != 0 || len(rec.CertHistory) != 0) {
		return fmt.Errorf("%w: user channel %s carries device state", storage.ErrInvalid, id.ID)
	}
	unlock, err := s.seal(purposeUnlockToken, id.ID, rec.UnlockToken)
	if err != nil {
		return err
	}
	bootstrap, err := s.seal(purposeBootstrapToken, id.ID, rec.BootstrapToken)
	if err != nil {
		return err
	}
	d := rec.Device
	return s.tx(ctx, func(q querier) error {
		if id.Channel.IsUser() {
			if err := s.exists(ctx, q, id.ParentID); err != nil {
				return fmt.Errorf("%w: parent %s of %s is absent", storage.ErrInvalid, id.ParentID, id.ID)
			}
		}
		if rec.CertHash != "" {
			var owner string
			err := q.QueryRowContext(ctx, s.q("SELECT id FROM enrollments WHERE cert_hash = ? AND id <> ?"), rec.CertHash, id.ID).Scan(&owner)
			if err == nil {
				return fmt.Errorf("%w: certificate already associated with %s", storage.ErrConflict, owner)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return wrap("lookup certificate", err)
			}
		}
		_, err := q.ExecContext(ctx, s.q(s.d.Upsert("enrollments", enrollmentCols, []string{"id"})),
			id.ID, int(id.Channel), id.ParentID, rec.Enabled, rec.Push.Topic, rec.Push.Magic, nullBytes(rec.Push.Token),
			d.SerialNumber, d.Model, d.ModelName, d.DeviceName, d.ProductName, d.OSVersion, d.BuildVersion, d.IMEI, d.MEID, d.Topic,
			rec.UserShortName, rec.UserLongName, unlock, nullBytes(rec.AuthenticateRaw), nullBytes(rec.TokenUpdateRaw),
			nullString(rec.CertHash), nullTime(rec.CertHashAt), bootstrap, nullTime(rec.BootstrapTokenAt),
			rec.EnrolledAt.UTC(), nullTime(rec.TokenUpdatedAt), rec.LastSeenAt.UTC(), nullTime(rec.DisabledAt))
		if s.d.uniqueViolation(err) {
			return fmt.Errorf("%w: certificate already associated with another enrollment", storage.ErrConflict)
		}
		if err != nil {
			return wrap("import enrollment", err)
		}
		for _, a := range rec.CertHistory {
			if err := s.recordAssociation(ctx, q, id.ID, a.Hash, a.At); err != nil {
				return err
			}
		}
		return nil
	})
}
