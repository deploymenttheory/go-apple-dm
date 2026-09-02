package ddm

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"sort"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/canonjson"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-mdm/schema/ddm"
	"github.com/deploymenttheory/go-apple-mdm/schema/ddmproto"
	"github.com/deploymenttheory/go-apple-mdm/schema/status"
)

// StatusItemDeclarations is the status item carrying per-declaration state.
const StatusItemDeclarations = status.StatusItemTypeManagementDeclarations

// Status stores a device's status report (decision record 0021): the raw
// report, every status item as canonical JSON keyed by its nested path, the
// typed management.declarations rows, and the Errors array. A full report
// replaces the enrollment's status; declarations absent from it are removed.
func (e *Engine) Status(ctx context.Context, id mdm.EnrollmentID, body []byte) (*StatusOutcome, error) {
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if len(body) > e.maxStatus {
		return nil, fmt.Errorf("%w: %d bytes, limit %d", ErrStatusTooLarge, len(body), e.maxStatus)
	}
	update, err := e.parseStatus(ctx, id, body)
	if err != nil {
		return nil, err
	}
	var out StatusOutcome
	err = e.store.Update(ctx, func(tx Tx) error {
		var err error
		out, err = tx.PutStatus(ctx, id, *update)
		return err
	})
	if err != nil {
		return nil, err
	}
	e.publish(ctx, event.DDMStatusReceived, id, &out)
	return &out, nil
}

// parseStatus decodes strictly (duplicate names and invalid UTF-8 are
// errors) and flattens the report.
func (e *Engine) parseStatus(ctx context.Context, id mdm.EnrollmentID, body []byte) (*StatusUpdate, error) {
	var report ddmproto.StatusReport
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStatusMalformed, err)
	}
	now := e.clock.Now().UTC()
	u := &StatusUpdate{Raw: body, ReceivedAt: now, KeepReports: e.keep}
	if report.FullReport != nil {
		u.FullReport = *report.FullReport
	}
	var snapshotTokens map[string]string
	if snap, err := e.store.Snapshot(ctx, id); err == nil {
		snapshotTokens = make(map[string]string, len(snap.Items))
		for _, it := range snap.Items {
			snapshotTokens[string(it.Kind)+"/"+it.Identifier] = it.ServerToken
		}
	}
	w := &statusWalker{now: now, snapshot: snapshotTokens}
	if err := w.walk("", report.StatusItems); err != nil {
		return nil, err
	}
	u.Values, u.Declarations, u.HasDeclarations = w.values, w.declarations(), w.hasDeclarations
	for _, se := range report.Errors {
		reasons, err := canonjson.Marshal(se.Reasons)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStatusMalformed, err)
		}
		u.Errors = append(u.Errors, StatusError{StatusItem: se.StatusItem, Reasons: reasons, ReceivedAt: now})
	}
	return u, nil
}

type statusWalker struct {
	now             time.Time
	values          []StatusValue
	decls           map[string]DeclarationStatus
	order           []string
	hasDeclarations bool
	snapshot        map[string]string
}

// walk descends StatusItems. A dotted path that names a registered status
// item is a boundary: its whole value is stored there. Everything else is
// stored at the deepest path reached, so unknown items are kept, arrays keep
// their elements, and nulls survive.
func (w *statusWalker) walk(path string, v any) error {
	if path != "" && isStatusItem(path) {
		return w.item(path, v)
	}
	obj, ok := v.(map[string]any)
	if !ok || path == "" && obj == nil {
		return w.value(path, v)
	}
	if path != "" && len(obj) == 0 {
		return w.value(path, v)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child := k
		if path != "" {
			child = path + "." + k
		}
		if err := w.walk(child, obj[k]); err != nil {
			return err
		}
	}
	return nil
}

func isStatusItem(path string) bool { return len(status.ByID(path)) > 0 }

func (w *statusWalker) item(path string, v any) error {
	if err := w.value(path, v); err != nil {
		return err
	}
	if path != StatusItemDeclarations {
		return nil
	}
	raw, err := canonjson.Marshal(v)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrStatusMalformed, path, err)
	}
	var typed status.ManagementDeclarations
	if err := json.Unmarshal(raw, &typed); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrStatusMalformed, path, err)
	}
	w.hasDeclarations = true
	w.decls = map[string]DeclarationStatus{}
	groups := []struct {
		kind schemaddm.Kind
		rows []status.ManagementDeclarationsDeclaration
	}{
		{schemaddm.KindActivation, typed.Value.Activations},
		{schemaddm.KindConfiguration, typed.Value.Configurations},
		{schemaddm.KindAsset, typed.Value.Assets},
		{schemaddm.KindManagement, typed.Value.Management},
	}
	for _, g := range groups {
		for _, row := range g.rows {
			if err := w.declaration(g.kind, row); err != nil {
				return err
			}
		}
	}
	return nil
}

// declaration records one row, keeping at most one per (kind, identifier).
// When a report names the same declaration twice, the entry whose token
// matches the snapshot wins; otherwise the later entry does.
func (w *statusWalker) declaration(kind schemaddm.Kind, row status.ManagementDeclarationsDeclaration) error {
	if row.Identifier == "" {
		return fmt.Errorf("%w: %s entry without identifier", ErrStatusMalformed, kind)
	}
	var reasons []byte
	if len(row.Reasons) > 0 {
		var err error
		if reasons, err = canonjson.Marshal(row.Reasons); err != nil {
			return fmt.Errorf("%w: reasons: %w", ErrStatusMalformed, err)
		}
	}
	key := string(kind) + "/" + row.Identifier
	ds := DeclarationStatus{Kind: kind, Identifier: row.Identifier, ServerToken: row.ServerToken, Active: row.Active,
		Valid: row.Valid, Reasons: reasons, FirstSeen: w.now, LastSeen: w.now}
	if prev, ok := w.decls[key]; ok {
		if want, known := w.snapshot[key]; known && prev.ServerToken == want && row.ServerToken != want {
			return nil
		}
	} else {
		w.order = append(w.order, key)
	}
	w.decls[key] = ds
	return nil
}

func (w *statusWalker) declarations() []DeclarationStatus {
	out := make([]DeclarationStatus, 0, len(w.order))
	for _, k := range w.order {
		out = append(out, w.decls[k])
	}
	return out
}

func (w *statusWalker) value(path string, v any) error {
	if path == "" {
		return nil
	}
	b, err := canonjson.Marshal(v)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrStatusMalformed, path, err)
	}
	w.values = append(w.values, StatusValue{Path: path, Value: b, FirstSeen: w.now, LastSeen: w.now})
	return nil
}
