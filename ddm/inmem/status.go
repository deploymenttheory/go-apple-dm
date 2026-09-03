package inmem

import (
	"bytes"
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

func copyStatus(d ddm.DeclarationStatus) ddm.DeclarationStatus {
	d.Reasons = bytes.Clone(d.Reasons)
	return d
}

func copyValue(v ddm.StatusValue) ddm.StatusValue {
	v.Value = bytes.Clone(v.Value)
	return v
}

func copyError(e ddm.StatusError) ddm.StatusError {
	e.Reasons = bytes.Clone(e.Reasons)
	return e
}

func copyReport(r ddm.StatusReportRecord) ddm.StatusReportRecord {
	r.Raw = bytes.Clone(r.Raw)
	return r
}

func compareStatus(a, b ddm.DeclarationStatus) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Identifier, b.Identifier))
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
func (t *tx) PutStatus(_ context.Context, id mdm.EnrollmentID, u ddm.StatusUpdate) (ddm.StatusOutcome, error) {
	if err := validID(id); err != nil {
		return ddm.StatusOutcome{}, err
	}
	if err := validStatusUpdate(u); err != nil {
		return ddm.StatusOutcome{}, err
	}
	key := id.ID
	t.st.ids[key] = id
	out := ddm.StatusOutcome{Seq: t.st.nextSeq()}
	t.st.statusReports[key] = append(t.st.statusReports[key], ddm.StatusReportRecord{
		Seq: out.Seq, FullReport: u.FullReport, Raw: bytes.Clone(u.Raw), ReceivedAt: u.ReceivedAt,
	})
	out.Removed = t.upsertStatusDeclarations(key, u)
	out.RemovedValues = t.upsertStatusValues(key, u)
	for _, e := range u.Errors {
		t.st.statusErrors[key] = append(t.st.statusErrors[key], ddm.StatusError{
			Seq: t.st.nextSeq(), StatusItem: e.StatusItem, Reasons: bytes.Clone(e.Reasons), ReceivedAt: u.ReceivedAt,
		})
	}
	out.PrunedReports = t.pruneReports(key, u.KeepReports)
	return out, nil
}

// upsertStatusDeclarations applies u.Declarations to the enrollment's rows
// and, for a full report that carried the declarations item, deletes the
// rows the report no longer mentions, returning them sorted.
func (t *tx) upsertStatusDeclarations(key string, u ddm.StatusUpdate) []ddm.DeclarationRef {
	rows := t.st.statusDecls[key]
	if rows == nil {
		rows = map[statusKey]ddm.DeclarationStatus{}
		t.st.statusDecls[key] = rows
	}
	seen := map[statusKey]struct{}{}
	for _, d := range u.Declarations {
		k := statusKey{kind: d.Kind, identifier: d.Identifier}
		seen[k] = struct{}{}
		row := copyStatus(d)
		row.FirstSeen, row.LastSeen = u.ReceivedAt, u.ReceivedAt
		if cur, ok := rows[k]; ok {
			row.FirstSeen = cur.FirstSeen
		}
		rows[k] = row
	}
	if !u.FullReport || !u.HasDeclarations {
		return nil
	}
	var removed []ddm.DeclarationRef
	for k, row := range rows {
		if _, ok := seen[k]; ok {
			continue
		}
		removed = append(removed, ddm.DeclarationRef{Kind: row.Kind, Identifier: row.Identifier, ServerToken: row.ServerToken})
		delete(rows, k)
	}
	return ddm.SortRefs(removed)
}

// upsertStatusValues applies u.Values and, for a full report, deletes the
// paths the report no longer carries, returning them sorted.
func (t *tx) upsertStatusValues(key string, u ddm.StatusUpdate) []string {
	rows := t.st.statusValues[key]
	if rows == nil {
		rows = map[string]ddm.StatusValue{}
		t.st.statusValues[key] = rows
	}
	seen := map[string]struct{}{}
	for _, v := range u.Values {
		seen[v.Path] = struct{}{}
		row := copyValue(v)
		row.FirstSeen, row.LastSeen = u.ReceivedAt, u.ReceivedAt
		if cur, ok := rows[v.Path]; ok {
			row.FirstSeen = cur.FirstSeen
		}
		rows[v.Path] = row
	}
	if !u.FullReport {
		return nil
	}
	var removed []string
	for path := range rows {
		if _, ok := seen[path]; ok {
			continue
		}
		removed = append(removed, path)
		delete(rows, path)
	}
	slices.Sort(removed)
	return removed
}

// pruneReports drops the oldest raw reports beyond keep and returns how
// many went. Reports are held in ascending seq order.
func (t *tx) pruneReports(key string, keep int) int64 {
	reports := t.st.statusReports[key]
	if keep <= 0 || len(reports) <= keep {
		return 0
	}
	drop := len(reports) - keep
	t.st.statusReports[key] = slices.Clone(reports[drop:])
	return int64(drop)
}

// DeclarationStatus implements ddm.StatusStore.
func (t *tx) DeclarationStatus(_ context.Context, id mdm.EnrollmentID) ([]ddm.DeclarationStatus, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	rows := t.st.statusDecls[id.ID]
	out := make([]ddm.DeclarationStatus, 0, len(rows))
	for _, row := range rows {
		out = append(out, copyStatus(row))
	}
	slices.SortFunc(out, compareStatus)
	return out, nil
}

// DeclarationStatusByIdentifier implements ddm.StatusStore. Rows are
// ordered by (enrollment id, kind) and the cursor is the enrollment id of
// the last row served.
func (t *tx) DeclarationStatusByIdentifier(_ context.Context, identifier string, p storage.Page) (storage.Result[ddm.EnrollmentDeclarationStatus], error) {
	if err := validName("identifier", identifier); err != nil {
		return storage.Result[ddm.EnrollmentDeclarationStatus]{}, err
	}
	var rows []ddm.EnrollmentDeclarationStatus
	for key, decls := range t.st.statusDecls {
		for k, row := range decls {
			if k.identifier == identifier {
				rows = append(rows, ddm.EnrollmentDeclarationStatus{ID: t.st.ids[key], DeclarationStatus: copyStatus(row)})
			}
		}
	}
	slices.SortFunc(rows, func(a, b ddm.EnrollmentDeclarationStatus) int {
		return cmp.Or(cmp.Compare(a.ID.ID, b.ID.ID), cmp.Compare(a.Kind, b.Kind))
	})
	limit := limitOf(p)
	var out storage.Result[ddm.EnrollmentDeclarationStatus]
	for _, row := range rows {
		if p.Cursor != "" && row.ID.ID <= p.Cursor {
			continue
		}
		if len(out.Items) == limit {
			out.NextCursor = out.Items[limit-1].ID.ID
			break
		}
		out.Items = append(out.Items, row)
	}
	return out, nil
}

// StatusValues implements ddm.StatusStore. PathPrefix is a plain string
// prefix and the cursor is the last path of the previous page.
func (t *tx) StatusValues(_ context.Context, id mdm.EnrollmentID, q ddm.StatusValueQuery, p storage.Page) (storage.Result[ddm.StatusValue], error) {
	if err := validID(id); err != nil {
		return storage.Result[ddm.StatusValue]{}, err
	}
	rows := t.st.statusValues[id.ID]
	keys := make([]string, 0, len(rows))
	for path := range rows {
		if strings.HasPrefix(path, q.PathPrefix) {
			keys = append(keys, path)
		}
	}
	return pageByKey(keys, p, func(path string) ddm.StatusValue { return copyValue(rows[path]) }), nil
}

// StatusErrors implements ddm.StatusStore.
func (t *tx) StatusErrors(_ context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[ddm.StatusError], error) {
	if err := validID(id); err != nil {
		return storage.Result[ddm.StatusError]{}, err
	}
	bySeq := map[int64]ddm.StatusError{}
	for _, e := range t.st.statusErrors[id.ID] {
		bySeq[e.Seq] = e
	}
	return pageBySeq(slices.Collect(maps.Keys(bySeq)), p, func(seq int64) ddm.StatusError { return copyError(bySeq[seq]) })
}

// StatusReports implements ddm.StatusStore.
func (t *tx) StatusReports(_ context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[ddm.StatusReportRecord], error) {
	if err := validID(id); err != nil {
		return storage.Result[ddm.StatusReportRecord]{}, err
	}
	bySeq := map[int64]ddm.StatusReportRecord{}
	for _, r := range t.st.statusReports[id.ID] {
		bySeq[r.Seq] = r
	}
	return pageBySeq(slices.Collect(maps.Keys(bySeq)), p, func(seq int64) ddm.StatusReportRecord { return copyReport(bySeq[seq]) })
}

// PutStatus implements ddm.StatusStore.
func (s *Store) PutStatus(ctx context.Context, id mdm.EnrollmentID, u ddm.StatusUpdate) (ddm.StatusOutcome, error) {
	v, done := s.view()
	defer done()
	return v.PutStatus(ctx, id, u)
}

// DeclarationStatus implements ddm.StatusStore.
func (s *Store) DeclarationStatus(ctx context.Context, id mdm.EnrollmentID) ([]ddm.DeclarationStatus, error) {
	v, done := s.view()
	defer done()
	return v.DeclarationStatus(ctx, id)
}

// DeclarationStatusByIdentifier implements ddm.StatusStore.
func (s *Store) DeclarationStatusByIdentifier(ctx context.Context, identifier string, p storage.Page) (storage.Result[ddm.EnrollmentDeclarationStatus], error) {
	v, done := s.view()
	defer done()
	return v.DeclarationStatusByIdentifier(ctx, identifier, p)
}

// StatusValues implements ddm.StatusStore.
func (s *Store) StatusValues(ctx context.Context, id mdm.EnrollmentID, q ddm.StatusValueQuery, p storage.Page) (storage.Result[ddm.StatusValue], error) {
	v, done := s.view()
	defer done()
	return v.StatusValues(ctx, id, q, p)
}

// StatusErrors implements ddm.StatusStore.
func (s *Store) StatusErrors(ctx context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[ddm.StatusError], error) {
	v, done := s.view()
	defer done()
	return v.StatusErrors(ctx, id, p)
}

// StatusReports implements ddm.StatusStore.
func (s *Store) StatusReports(ctx context.Context, id mdm.EnrollmentID, p storage.Page) (storage.Result[ddm.StatusReportRecord], error) {
	v, done := s.view()
	defer done()
	return v.StatusReports(ctx, id, p)
}
