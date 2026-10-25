package app

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// inventoryEvidence keeps history and outcomes separate from the current snapshot.
// OriginalJSON preserves the exact original bytes through JSON transport.
type inventoryEvidence struct {
	Record    inventory.ResourceRecord `json:"record"`
	Links     []inventory.SerialLink   `json:"links"`
	History   []inventory.Observation  `json:"history,omitempty"`
	Originals [][]byte                 `json:"original_json,omitempty"`
	Attempts  []inventory.Attempt      `json:"attempts,omitempty"`
	Envelopes []json.RawMessage        `json:"legacy_envelopes,omitempty"`
}

type serialInspection struct {
	Serial    string              `json:"serial"`
	Resources []inventoryEvidence `json:"resources"`
}

// inspectInventory never correlates or resolves as a side effect of reading.
func (a *App) inspectInventory(ctx context.Context, d inventory.ResourceRecord, raw bool) (inventoryEvidence, error) {
	out := inventoryEvidence{Record: inventory.PublicRecord(d)}
	var err error
	out.Links, err = a.Inventory.Links(ctx, d.Source)
	if err != nil {
		return out, err
	}
	if !raw {
		return out, nil
	}
	out.Record = d
	out.History, err = a.Inventory.History(ctx, d.Source)
	if err != nil {
		return out, err
	}
	for _, o := range out.History {
		out.Originals = append(out.Originals, append([]byte(nil), o.Raw...))
	}
	out.Attempts, err = a.Inventory.Attempts(ctx, d.Source)
	if err != nil {
		return out, err
	}
	out.Envelopes, err = a.Inventory.Envelopes(ctx, d.Source)
	return out, err
}

// inspectSerial locates source evidence by the physical key without treating
// protocol IDs as serials or authentication. An equal serial is not a checked link.
func (a *App) inspectSerial(ctx context.Context, serial string, raw bool) (serialInspection, error) {
	out := serialInspection{Serial: inventory.CanonicalSerial(serial), Resources: []inventoryEvidence{}}
	if out.Serial == "" {
		return out, inventory.ErrInvalid
	}
	err := a.Inventory.EachResource(ctx, inventory.ResourceQuery{}, func(d inventory.ResourceRecord) error {
		s := ""
		if d.Source.Kind == "axm.device" {
			s = inventory.String(d.Snapshot.Fields["attributes.serialNumber"])
		} else if d.Snapshot.Native != nil {
			s = d.Snapshot.Native.Serial
		}
		if s == "" || inventory.CanonicalSerial(s) != out.Serial {
			return nil
		}
		evidence, err := a.inspectInventory(ctx, d, raw)
		if err != nil {
			return err
		}
		out.Resources = append(out.Resources, evidence)
		return nil
	})
	if err == nil && len(out.Resources) == 0 {
		err = inventory.ErrNotFound
	}
	return out, err
}

// inventoryMapping names exact source paths with explicitly declared equivalence.
// Apple paths are attributes relative; native paths retain their protocol shape.
type inventoryMapping struct {
	Name   string `json:"name"`
	Inputs []struct {
		ResourceID string `json:"resource_id"`
		Path       string `json:"path"`
	} `json:"inputs"`
}

type inventoryResolutionValue struct {
	Source     source.Reference `json:"source"`
	Path       string           `json:"path"`
	Value      json.RawMessage  `json:"value"`
	ObservedAt time.Time        `json:"observed_at"`
	Native     bool             `json:"native"`
}

type inventoryResolution struct {
	Selected   *inventoryResolutionValue  `json:"selected"`
	Candidates []inventoryResolutionValue `json:"candidates"`
	Disagrees  bool                       `json:"disagrees"`
}

// resolveInventory consumes only current checked links. Evidence refresh invalidates
// links in persistence, so callers must explicitly correlate again after collection.
func (a *App) resolveInventory(ctx context.Context, id string, mappings []inventoryMapping) (map[string]inventoryResolution, error) {
	var out map[string]inventoryResolution
	err := a.Inventory.Backend.Update(ctx, func(tx inventory.Tx) error {
		view := &App{Inventory: &inventory.Repository{Backend: inventorySnapshot{Backend: a.Inventory.Backend, Reader: tx}}}
		var err error
		out, err = view.resolveInventorySnapshot(ctx, id, mappings)
		return err
	})
	return out, err
}

// inventorySnapshot keeps all resolver reads in the same local transaction as
// link validation, so concurrent refresh cannot introduce unchecked evidence.
type inventorySnapshot struct {
	inventory.Backend
	inventory.Reader
}

// Get reads a source document through the active local transaction.
func (s inventorySnapshot) Get(ctx context.Context, key string) (json.RawMessage, error) {
	return s.Reader.Get(ctx, key)
}

// Scan reads ordered evidence through the active local transaction.
func (s inventorySnapshot) Scan(ctx context.Context, prefix, after string, limit int) ([]inventory.Entry, error) {
	return s.Reader.Scan(ctx, prefix, after, limit)
}

// resolveInventorySnapshot checks links and resolves only their retained originals.
func (a *App) resolveInventorySnapshot(ctx context.Context, id string, mappings []inventoryMapping) (map[string]inventoryResolution, error) {
	apple, err := a.Inventory.Resource(ctx, id)
	if err != nil {
		return nil, err
	}
	if apple.Source.Kind != "axm.device" || apple.Incomplete || apple.Snapshot.Absent || apple.Snapshot.Disconnected || len(mappings) == 0 {
		return nil, inventory.ErrInvalid
	}
	links, err := a.Inventory.Links(ctx, apple.Source)
	if err != nil {
		return nil, err
	}
	refs := map[string]source.Reference{apple.ID: apple.Source}
	reports := []correlation.NativeReport{}
	for _, link := range links {
		if link.Apple != apple.Source {
			continue
		}
		refs[link.Native.Key()] = link.Native
		history, err := a.Inventory.History(ctx, link.Native)
		if err != nil {
			return nil, err
		}
		for _, o := range history {
			identity := inventory.NativeIdentity{}
			if o.Native != nil {
				identity = *o.Native
			}
			report := correlation.NativeReport{Source: o.Source, Serial: identity.Serial, Method: identity.Method, Authenticated: identity.Authenticated, ObservedAt: o.ObservedAt, Fields: o.Fields, Raw: o.Raw}
			// A retained DDM field's time belongs to that field, not the newest report.
			if len(o.FieldTimes) == 0 {
				reports = append(reports, report)
				continue
			}
			for path, value := range o.Fields {
				field := report
				field.Fields = map[string]json.RawMessage{path: value}
				if at := o.FieldTimes[path]; !at.IsZero() {
					field.ObservedAt = at
				}
				reports = append(reports, field)
			}
		}
	}
	checked, err := correlation.Correlate(correlation.AppleDevice{AccountID: apple.Source.AccountID, Resource: collection.Resource{ID: apple.Source.ResourceID, Type: "orgDevices", Raw: apple.Snapshot.Raw}, ObservedAt: apple.Snapshot.ObservedAt}, reports)
	if err != nil {
		return nil, inventory.ErrInvalid
	}
	declared := make([]correlation.Equivalent, 0, len(mappings))
	for _, m := range mappings {
		equivalent := correlation.Equivalent{Name: m.Name}
		for _, input := range m.Inputs {
			ref, ok := refs[input.ResourceID]
			if !ok {
				return nil, inventory.ErrInvalid
			}
			equivalent.Inputs = append(equivalent.Inputs, correlation.Field{Source: ref, Name: input.Path})
		}
		declared = append(declared, equivalent)
	}
	resolved, err := checked.Resolve(declared)
	if err != nil {
		return nil, inventory.ErrInvalid
	}
	out := map[string]inventoryResolution{}
	convert := func(v correlation.Value) inventoryResolutionValue {
		return inventoryResolutionValue{v.Field.Source, v.Field.Name, v.Value, v.ObservedAt, v.Native}
	}
	for name, result := range resolved {
		value := inventoryResolution{Candidates: []inventoryResolutionValue{}, Disagrees: result.Disagrees}
		if result.Selected != nil {
			selected := convert(*result.Selected)
			value.Selected = &selected
		}
		for _, candidate := range result.Candidates {
			value.Candidates = append(value.Candidates, convert(candidate))
		}
		out[name] = value
	}
	return out, nil
}

// validateInventoryParameters fails closed for retired normalized filters and
// unknown options. Parsed values are returned only when every raw pair is valid;
// dropping malformed pairs could silently broaden a fleet query.
func validateInventoryParameters(r *http.Request, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, inventory.ErrInvalid
	}
	set := map[string]bool{}
	for _, key := range allowed {
		set[key] = true
	}
	for key, entries := range values {
		if !set[key] || len(entries) != 1 {
			return nil, inventory.ErrInvalid
		}
	}
	return values, nil
}

// inventoryInspectionRoutes keeps raw evidence under separate explicit authority.
func (a *App) inventoryInspectionRoutes(add func(string, string, func(http.ResponseWriter, *http.Request) (any, error))) {
	for _, raw := range []bool{false, true} {
		prefix, action := "/devices", ActionReadInventory
		if raw {
			prefix, action = "/inventory/raw/devices", ActionReadRawInventory
		}
		serialPrefix := "/inventory/serials"
		if raw {
			serialPrefix = "/inventory/raw/serials"
		}
		add("GET "+serialPrefix+"/{serial}", action, func(_ http.ResponseWriter, r *http.Request) (any, error) {
			if _, err := validateInventoryParameters(r); err != nil {
				return nil, err
			}
			return a.inspectSerial(r.Context(), r.PathValue("serial"), raw)
		})
		add("GET "+prefix+"/{id}/evidence", action, func(_ http.ResponseWriter, r *http.Request) (any, error) {
			if _, err := validateInventoryParameters(r); err != nil {
				return nil, err
			}
			d, err := a.Inventory.Resource(r.Context(), r.PathValue("id"))
			if err != nil {
				return nil, err
			}
			return a.inspectInventory(r.Context(), d, raw)
		})
	}
	add("POST /inventory/correlations", ActionManageInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		var input struct {
			Apple  string `json:"apple"`
			Native string `json:"native"`
		}
		if err := inventoryBody(r, &input); err != nil {
			return nil, err
		}
		apple, err := a.Inventory.Resource(r.Context(), input.Apple)
		if err != nil {
			return nil, err
		}
		native, err := a.Inventory.Resource(r.Context(), input.Native)
		if err != nil {
			return nil, err
		}
		link, err := a.Inventory.Link(r.Context(), apple.Source, native.Source, time.Now().UTC())
		if errors.Is(err, correlation.ErrInvalid) {
			err = inventory.ErrInvalid
		}
		return link, err
	})
	add("POST /inventory/raw/devices/{id}/resolve", ActionReadRawInventory, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		var input struct {
			Mappings []inventoryMapping `json:"mappings"`
		}
		if err := inventoryBody(r, &input); err != nil {
			return nil, err
		}
		return a.resolveInventory(r.Context(), r.PathValue("id"), input.Mappings)
	})
}

// collectionEnrollment validates the entire stored addressing tuple. A channel
// selects protocol addressing and never supplies an enrollment method.
func collectionEnrollment(d inventory.ResourceRecord) (string, string, error) {
	if d.Source.Kind != "enrollment" || d.Snapshot.Absent || d.Snapshot.Disconnected {
		return "", "", inventory.ErrInvalid
	}
	var fields struct {
		ID      string `json:"enrollment_id"`
		Channel string `json:"enrollment_channel"`
	}
	if json.Unmarshal(d.Snapshot.Raw, &fields) != nil || fields.ID == "" || (fields.Channel != "device" && fields.Channel != "user-enrollment-device") {
		return "", "", inventory.ErrInvalid
	}
	if d.Source.ResourceID != fields.Channel+":"+fields.ID {
		return "", "", inventory.ErrInvalid
	}
	return fields.ID, fields.Channel, nil
}

// inventoryCursor binds continuation to the original source predicates. It is a
// boundary for a live ordered scan, not an Apple-side consistent snapshot.
type inventoryCursor struct {
	Version int    `json:"v"`
	After   string `json:"after"`
	Query   string `json:"query"`
}

// inventoryQueryFingerprint binds source predicates independently of page size and query time.
func inventoryQueryFingerprint(q inventory.ResourceQuery) string {
	q.Cursor = ""
	q.Limit = 0
	q.AsOf = time.Time{}
	raw, _ := json.Marshal(q)
	return issuanceHash(raw)
}

// decodeInventoryCursor rejects foreign or retired continuation representations.
func decodeInventoryCursor(q *inventory.ResourceQuery) error {
	if q.Cursor == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
	if err != nil {
		return inventory.ErrInvalid
	}
	var cursor inventoryCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || len(cursor.After) != 64 || cursor.Query != inventoryQueryFingerprint(*q) {
		return inventory.ErrInvalid
	}
	if _, err := hex.DecodeString(cursor.After); err != nil {
		return inventory.ErrInvalid
	}
	q.Cursor = cursor.After
	return nil
}

// encodeInventoryCursor returns an opaque continuation bound to the current predicates.
func encodeInventoryCursor(after string, q inventory.ResourceQuery) string {
	if after == "" {
		return ""
	}
	raw, _ := json.Marshal(inventoryCursor{Version: 1, After: after, Query: inventoryQueryFingerprint(q)})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// publicInventoryPreset excludes original-source predicates and projections from
// ordinary preset reads and writes, including literal values in saved filters.
func publicInventoryPreset(p inventory.ExportPreset) error {
	if err := publicInventoryQuery(p.Query); err != nil {
		return err
	}
	for _, column := range p.Columns {
		if !inventory.PublicField(column) {
			return inventory.ErrInvalid
		}
	}
	return nil
}

// authorizeInventoryRaw requires raw authority in addition to mutation authority
// before accepting a preset that can return source originals in its response.
func (a *App) authorizeInventoryRaw(w http.ResponseWriter, r *http.Request) bool {
	if err := a.checkPolicy(r, a.actor(r), adminRoute{Action: ActionReadRawInventory}); err != nil {
		writeError(w, http.StatusForbidden, ErrForbidden)
		return false
	}
	return true
}
