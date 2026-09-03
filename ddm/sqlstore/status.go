package sqlstore

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

var (
	statusDeclarationCols = []string{"enrollment_id", "kind", "identifier", "channel", "parent_id", "server_token", "active", "valid", "reasons", "first_seen", "last_seen"}
	statusValueCols       = []string{"enrollment_id", "path", "value", "first_seen", "last_seen"}
)

// statusCols is the column list scanStatus reads, positionally.
const statusCols = "kind, identifier, server_token, active, valid, reasons, first_seen, last_seen"

func scanStatus(row scanner, d *ddm.DeclarationStatus) error {
	if err := row.Scan(&d.Kind, &d.Identifier, &d.ServerToken, &d.Active, &d.Valid, &d.Reasons, &d.FirstSeen, &d.LastSeen); err != nil {
		return wrap("scan declaration status", err)
	}
	d.FirstSeen, d.LastSeen = d.FirstSeen.UTC(), d.LastSeen.UTC()
	return nil
}

// validStatusUpdate rejects rows the store cannot key.
func validStatusUpdate(u ddm.StatusUpdate) error {
	for _, d := range u.Declarations {
		if err := validName("status declaration identifier", d.Identifier); err != nil {
			return err
		}
	}
	for _, v := range u.Values {
		if err := validName("status value path", v.Path); err != nil {
			return err
		}
	}
	return nil
}

// PutStatus implements ddm.StatusStore. Every error row takes the report's
// ReceivedAt.
func (t *txStore) PutStatus(ctx context.Context, id mdm.EnrollmentID, u ddm.StatusUpdate) (ddm.StatusOutcome, error) {
	if err := validID(id); err != nil {
		return ddm.StatusOutcome{}, err
	}
	if err := validStatusUpdate(u); err != nil {
		return ddm.StatusOutcome{}, err
	}
	at := utc(u.ReceivedAt)
	seq, err := t.insertSeq(ctx, "insert status report", "INSERT INTO ddm_status_reports (enrollment_id, full_report, raw, received_at) VALUES (?, ?, ?, ?)",
		id.ID, u.FullReport, nullBytes(u.Raw), at)
	if err != nil {
		return ddm.StatusOutcome{}, err
	}
	out := ddm.StatusOutcome{Seq: seq}
	if out.Removed, err = t.putStatusDeclarations(ctx, id, u, at); err != nil {
		return ddm.StatusOutcome{}, err
	}
	if out.RemovedValues, err = t.putStatusValues(ctx, id.ID, u, at); err != nil {
		return ddm.StatusOutcome{}, err
	}
	for _, e := range u.Errors {
		if _, err := t.exec(ctx, "insert status error", "INSERT INTO ddm_status_errors (enrollment_id, status_item, reasons, received_at) VALUES (?, ?, ?, ?)",
			id.ID, e.StatusItem, nullBytes(e.Reasons), at); err != nil {
			return ddm.StatusOutcome{}, err
		}
	}
	if out.PrunedReports, err = t.pruneReports(ctx, id.ID, u.KeepReports); err != nil {
		return ddm.StatusOutcome{}, err
	}
	return out, nil
}

// putStatusDeclarations upserts u.Declarations (FirstSeen kept, LastSeen
// bumped) and, for a full report that carried the declarations item,
// deletes the rows the report no longer mentions, returning them sorted.
func (t *txStore) putStatusDeclarations(ctx context.Context, id mdm.EnrollmentID, u ddm.StatusUpdate, at time.Time) ([]ddm.DeclarationRef, error) {
	key := id.ID
	var stale []ddm.DeclarationRef
	if u.FullReport && u.HasDeclarations {
		seen := map[ddm.DeclarationRef]bool{}
		for _, d := range u.Declarations {
			seen[ddm.DeclarationRef{Kind: d.Kind, Identifier: d.Identifier}] = true
		}
		err := t.each(ctx, "list declaration status", "SELECT kind, identifier, server_token FROM ddm_status_declarations WHERE enrollment_id = ?", []any{key}, func(rows *sql.Rows) error {
			var ref ddm.DeclarationRef
			if err := rows.Scan(&ref.Kind, &ref.Identifier, &ref.ServerToken); err != nil {
				return wrap("scan declaration status", err)
			}
			if !seen[ddm.DeclarationRef{Kind: ref.Kind, Identifier: ref.Identifier}] {
				stale = append(stale, ref)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	// The identity columns and first_seen are written on insert only.
	query := t.upsert("ddm_status_declarations", statusDeclarationCols, statusDeclarationCols[:3], []string{"channel", "parent_id", "first_seen"})
	for _, d := range u.Declarations {
		if _, err := t.exec(ctx, "upsert declaration status", query, key, string(d.Kind), d.Identifier, int(id.Channel), id.ParentID, d.ServerToken, d.Active, d.Valid, nullBytes(d.Reasons), at, at); err != nil {
			return nil, err
		}
	}
	for _, ref := range stale {
		if _, err := t.exec(ctx, "delete declaration status", "DELETE FROM ddm_status_declarations WHERE enrollment_id = ? AND kind = ? AND identifier = ?", key, string(ref.Kind), ref.Identifier); err != nil {
			return nil, err
		}
	}
	return ddm.SortRefs(stale), nil
}

// putStatusValues upserts u.Values and, for a full report, deletes the
// paths the report no longer carries, returning them sorted.
func (t *txStore) putStatusValues(ctx context.Context, key string, u ddm.StatusUpdate, at time.Time) ([]string, error) {
	var stale []string
	if u.FullReport {
		seen := map[string]bool{}
		for _, v := range u.Values {
			seen[v.Path] = true
		}
		paths, err := t.column(ctx, "list status values", "SELECT path FROM ddm_status_values WHERE enrollment_id = ?", key)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			if !seen[path] {
				stale = append(stale, path)
			}
		}
	}
	query := t.upsert("ddm_status_values", statusValueCols, statusValueCols[:2], statusValueCols[3:4])
	for _, v := range u.Values {
		if _, err := t.exec(ctx, "upsert status value", query, key, v.Path, nonNil(v.Value), at, at); err != nil {
			return nil, err
		}
	}
	for _, path := range stale {
		if _, err := t.exec(ctx, "delete status value", "DELETE FROM ddm_status_values WHERE enrollment_id = ? AND path = ?", key, path); err != nil {
			return nil, err
		}
	}
	slices.Sort(stale)
	return stale, nil
}

// pruneReports drops the oldest raw reports beyond keep and returns how
// many went.
func (t *txStore) pruneReports(ctx context.Context, key string, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	var oldestKept int64
	found, err := t.row(ctx, "find retained report", "SELECT seq FROM ddm_status_reports WHERE enrollment_id = ? ORDER BY seq DESC LIMIT 1 OFFSET ?", []any{key, keep - 1}, &oldestKept)
	if err != nil || !found {
		return 0, err
	}
	res, err := t.exec(ctx, "prune reports", "DELETE FROM ddm_status_reports WHERE enrollment_id = ? AND seq < ?", key, oldestKept)
	if err != nil {
		return 0, err
	}
	return affected("prune reports", res)
}

// DeclarationStatus implements ddm.StatusStore.
func (t *txStore) DeclarationStatus(ctx context.Context, id mdm.EnrollmentID) ([]ddm.DeclarationStatus, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	out := make([]ddm.DeclarationStatus, 0)
	err := t.each(ctx, "declaration status", "SELECT "+statusCols+" FROM ddm_status_declarations WHERE enrollment_id = ? ORDER BY kind, identifier", []any{id.ID}, func(rows *sql.Rows) error {
		var d ddm.DeclarationStatus
		if err := scanStatus(rows, &d); err != nil {
			return err
		}
		out = append(out, d)
		return nil
	})
	return out, err
}

// DeclarationStatusByIdentifier implements ddm.StatusStore. Rows are
// ordered by (enrollment id, kind) and the cursor is the enrollment id of
// the last row served.
func (t *txStore) DeclarationStatusByIdentifier(ctx context.Context, identifier string, p storage.Page) (storage.Result[ddm.EnrollmentDeclarationStatus], error) {
	if err := validName("identifier", identifier); err != nil {
		return storage.Result[ddm.EnrollmentDeclarationStatus]{}, err
	}
	where, args := after([]string{"identifier = ?"}, []any{identifier}, "enrollment_id", p)
	return keyset(ctx, t, "declaration status by identifier",
		"SELECT "+enrollmentIDCols+", "+statusCols+" FROM ddm_status_declarations WHERE "+strings.Join(where, " AND ")+" ORDER BY enrollment_id, kind", args, p,
		func(rows *sql.Rows) (ddm.EnrollmentDeclarationStatus, string, error) {
			var row ddm.EnrollmentDeclarationStatus
			var channel int
			if err := rows.Scan(&row.ID.ID, &channel, &row.ID.ParentID, &row.Kind, &row.Identifier, &row.ServerToken, &row.Active, &row.Valid, &row.Reasons, &row.FirstSeen, &row.LastSeen); err != nil {
				return ddm.EnrollmentDeclarationStatus{}, "", wrap("scan declaration status", err)
			}
			row.ID.Channel = mdm.Channel(channel) // #nosec G115 -- stored from a uint8
			row.FirstSeen, row.LastSeen = row.FirstSeen.UTC(), row.LastSeen.UTC()
			return row, row.ID.ID, nil
		})
}

// StatusValues implements ddm.StatusStore. PathPrefix is a plain string
// prefix (compared by SUBSTR, so pattern characters mean nothing) and the
// cursor is the last path of the previous page.
func (t *txStore) StatusValues(ctx context.Context, id mdm.EnrollmentID, q ddm.StatusValueQuery, p storage.Page) (storage.Result[ddm.StatusValue], error) {
	if err := validID(id); err != nil {
		return storage.Result[ddm.StatusValue]{}, err
	}
	where, args := []string{"enrollment_id = ?"}, []any{id.ID}
	if q.PathPrefix != "" {
		where, args = append(where, "SUBSTR(path, 1, ?) = ?"), append(args, utf8.RuneCountInString(q.PathPrefix), q.PathPrefix)
	}
	where, args = after(where, args, "path", p)
	return keyset(ctx, t, "status values", "SELECT path, value, first_seen, last_seen FROM ddm_status_values WHERE "+strings.Join(where, " AND ")+" ORDER BY path", args, p,
		func(rows *sql.Rows) (ddm.StatusValue, string, error) {
			var v ddm.StatusValue
			if err := rows.Scan(&v.Path, &v.Value, &v.FirstSeen, &v.LastSeen); err != nil {
				return ddm.StatusValue{}, "", wrap("scan status value", err)
			}
			v.FirstSeen, v.LastSeen = v.FirstSeen.UTC(), v.LastSeen.UTC()
			return v, v.Path, nil
		})
}

// bySeq pages an enrollment's rows newest first.
func bySeq[T any](ctx context.Context, t *txStore, op, table, cols string, id mdm.EnrollmentID, p storage.Page, scan func(*sql.Rows) (T, int64, error)) (storage.Result[T], error) {
	if err := validID(id); err != nil {
		return storage.Result[T]{}, err
	}
	where, args := []string{"enrollment_id = ?"}, []any{id.ID}
	before, ok, err := seqCursor(p)
	if err != nil {
		return storage.Result[T]{}, err
	}
	if ok {
		where, args = append(where, "seq < ?"), append(args, before)
	}
	return keyset(ctx, t, op, "SELECT "+cols+" FROM "+table+" WHERE "+strings.Join(where, " AND ")+" ORDER BY seq DESC", args, p, // #nosec G202 -- table and column names are literals
		func(rows *sql.Rows) (T, string, error) {
			item, seq, err := scan(rows)
			return item, strconv.FormatInt(seq, 10), err
		})
}

// StatusErrors implements ddm.StatusStore.
func (t *txStore) StatusErrors(ctx context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[ddm.StatusError], error) {
	return bySeq(ctx, t, "status errors", "ddm_status_errors", "seq, status_item, reasons, received_at", id, p, func(rows *sql.Rows) (ddm.StatusError, int64, error) {
		var e ddm.StatusError
		if err := rows.Scan(&e.Seq, &e.StatusItem, &e.Reasons, &e.ReceivedAt); err != nil {
			return ddm.StatusError{}, 0, wrap("scan status error", err)
		}
		e.ReceivedAt = e.ReceivedAt.UTC()
		return e, e.Seq, nil
	})
}

// StatusReports implements ddm.StatusStore.
func (t *txStore) StatusReports(ctx context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[ddm.StatusReportRecord], error) {
	return bySeq(ctx, t, "status reports", "ddm_status_reports", "seq, full_report, raw, received_at", id, p, func(rows *sql.Rows) (ddm.StatusReportRecord, int64, error) {
		var r ddm.StatusReportRecord
		if err := rows.Scan(&r.Seq, &r.FullReport, &r.Raw, &r.ReceivedAt); err != nil {
			return ddm.StatusReportRecord{}, 0, wrap("scan status report", err)
		}
		r.ReceivedAt = r.ReceivedAt.UTC()
		return r, r.Seq, nil
	})
}

// PutStatus implements ddm.StatusStore.
func (s *Store) PutStatus(ctx context.Context, id mdm.EnrollmentID, u ddm.StatusUpdate) (out ddm.StatusOutcome, err error) {
	err = s.write(ctx, func(t *txStore) error {
		out, err = t.PutStatus(ctx, id, u)
		return err
	})
	return out, err
}

// DeclarationStatus implements ddm.StatusStore.
func (s *Store) DeclarationStatus(ctx context.Context, id mdm.EnrollmentID) ([]ddm.DeclarationStatus, error) {
	return s.view().DeclarationStatus(ctx, id)
}

// DeclarationStatusByIdentifier implements ddm.StatusStore.
func (s *Store) DeclarationStatusByIdentifier(ctx context.Context, identifier string, p storage.Page) (storage.Result[ddm.EnrollmentDeclarationStatus], error) {
	return s.view().DeclarationStatusByIdentifier(ctx, identifier, p)
}

// StatusValues implements ddm.StatusStore.
func (s *Store) StatusValues(ctx context.Context, id mdm.EnrollmentID, q ddm.StatusValueQuery, p storage.Page) (storage.Result[ddm.StatusValue], error) {
	return s.view().StatusValues(ctx, id, q, p)
}

// StatusErrors implements ddm.StatusStore.
func (s *Store) StatusErrors(ctx context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[ddm.StatusError], error) {
	return s.view().StatusErrors(ctx, id, p)
}

// StatusReports implements ddm.StatusStore.
func (s *Store) StatusReports(ctx context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[ddm.StatusReportRecord], error) {
	return s.view().StatusReports(ctx, id, p)
}
