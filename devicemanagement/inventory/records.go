package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/collection"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// Repository retains independent source evidence and checked serial links.
type Repository struct{ Backend Backend }

// New validates the persistence dependency. Migrate must run before workers start.
func New(b Backend) (*Repository, error) {
	if b == nil {
		return nil, ErrInvalid
	}
	return &Repository{Backend: b}, nil
}

// Resource loads one independently addressed source record.
func (r *Repository) Resource(ctx context.Context, id string) (ResourceRecord, error) {
	return get[ResourceRecord](ctx, r.Backend, "resource/"+id)
}

// SourceResource loads the record for its original account-scoped source identity.
func (r *Repository) SourceResource(ctx context.Context, ref source.Reference) (ResourceRecord, error) {
	return r.Resource(ctx, ref.Key())
}

// Observe commits a successful observation and its original history atomically.
// Serial is the host's authenticated serial context for native reports only;
// Apple serial evidence is always read directly from the original resource.
func (r *Repository) Observe(ctx context.Context, serial string, o Observation) (ResourceRecord, error) {
	var out ResourceRecord
	err := r.Backend.Update(ctx, func(tx Tx) error { var err error; out, err = r.ObserveTx(ctx, tx, serial, o); return err })
	return out, err
}

// ObserveTx allows evidence and a worker checkpoint to share one transaction.
func (r *Repository) ObserveTx(ctx context.Context, tx Tx, serial string, o Observation) (ResourceRecord, error) {
	if o.Source.Kind == "" || o.Source.ResourceID == "" || o.ObservedAt.IsZero() || !json.Valid(o.Raw) {
		return ResourceRecord{}, ErrInvalid
	}
	for _, v := range o.Fields {
		if !json.Valid(v) {
			return ResourceRecord{}, ErrInvalid
		}
	}
	if o.Native != nil {
		n := *o.Native
		if n.Serial == "" {
			n.Serial = serial
		}
		o.Native = &n
	}
	if o.Fields == nil {
		o.Fields = Flatten(o.Raw)
	}
	record, err := get[ResourceRecord](ctx, tx, "resource/"+o.Source.Key())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return record, err
	}
	if record.ID == "" {
		record = ResourceRecord{ID: o.Source.Key(), Source: o.Source, CreatedAt: o.ObservedAt}
	}
	// The content digest makes retrying an acknowledged page idempotent while
	// retaining distinct originals even when a source reuses an observation time.
	raw, err := json.Marshal(struct {
		Observation
		OriginalJSON []byte `json:"original_json,omitempty"`
	}{o, append([]byte(nil), o.Raw...)})
	if err != nil {
		return record, err
	}
	historyKey := "history/" + record.ID + "/" + digest(string(raw))
	_, historyErr := tx.Get(ctx, historyKey)
	if historyErr != nil && !errors.Is(historyErr, ErrNotFound) {
		return record, historyErr
	}
	if errors.Is(historyErr, ErrNotFound) {
		if err := tx.Put(ctx, historyKey, raw); err != nil {
			return record, err
		}
		// A delayed original can contradict the checked relationship even though it
		// must not replace the latest successful snapshot. Replays add no evidence.
		if err := invalidateLinks(ctx, tx, o.Source); err != nil {
			return record, err
		}
	}

	if record.Snapshot.ObservedAt.After(o.ObservedAt) {
		return record, nil
	}
	record.Snapshot = o
	record.UpdatedAt = o.ObservedAt
	record.Incomplete = !completeOriginal(o.Source, o.Raw)
	if !strings.HasPrefix(o.Source.Kind, "axm.") {
		record.Incomplete = record.Incomplete || !knownNativeProvenance(o.Native)
	}
	if err := putResource(ctx, tx, record); err != nil {
		return record, err
	}

	return record, nil
}

// History returns the retained successful originals, without failed outcomes.
func (r *Repository) History(ctx context.Context, ref source.Reference) ([]Observation, error) {
	out := []Observation{}
	err := walk(ctx, r.Backend, "history/"+ref.Key()+"/", func(e Entry) error {
		var o Observation
		if err := json.Unmarshal(e.Value, &o); err != nil {
			return err
		}
		out = append(out, o)
		return nil
	})
	return out, err
}

// Attempt records failure separately, including failures before the first success.
func (r *Repository) Attempt(ctx context.Context, ref source.Reference, at time.Time, code string) error {
	return r.Backend.Update(ctx, func(tx Tx) error { return attemptTx(ctx, tx, ref, at, code) })
}

// attemptTx stages a classified failed outcome inside the evidence transaction.
func attemptTx(ctx context.Context, tx Tx, ref source.Reference, at time.Time, code string) error {
	if ref.Kind == "" || ref.ResourceID == "" || at.IsZero() || code == "" {
		return ErrInvalid
	}
	a := Attempt{ref, at, code}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return tx.Put(ctx, "attempt/"+ref.Key()+"/"+digest(string(raw)), raw)
}

// Attempts returns failed refresh outcomes independently of successful history.
func (r *Repository) Attempts(ctx context.Context, ref source.Reference) ([]Attempt, error) {
	out := []Attempt{}
	err := walk(ctx, r.Backend, "attempt/"+ref.Key()+"/", func(e Entry) error {
		var a Attempt
		if err := json.Unmarshal(e.Value, &a); err != nil {
			return err
		}
		out = append(out, a)
		return nil
	})
	return out, err
}

// ChangeSource changes lifecycle metadata while preserving original evidence.
func (r *Repository) ChangeSource(ctx context.Context, ref source.Reference, absent, disconnected bool) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		d, err := get[ResourceRecord](ctx, tx, "resource/"+ref.Key())
		if err != nil {
			return err
		}
		d.Snapshot.Absent = absent
		d.Snapshot.Disconnected = disconnected
		if err := putResource(ctx, tx, d); err != nil {
			return err
		}
		if absent || disconnected {
			return invalidateLinks(ctx, tx, ref)
		}
		return nil
	})
}

// Link explicitly checks successful originals through the standalone correlator.
// Missing authentication or enrollment provenance never creates a hardware link.
func (r *Repository) Link(ctx context.Context, apple, native source.Reference, at time.Time) (SerialLink, error) {
	var out SerialLink
	err := r.Backend.Update(ctx, func(tx Tx) error {
		a, err := get[ResourceRecord](ctx, tx, "resource/"+apple.Key())
		if err != nil {
			return err
		}
		n, err := get[ResourceRecord](ctx, tx, "resource/"+native.Key())
		if err != nil {
			return err
		}
		if apple.Kind != "axm.device" || a.Incomplete || n.Incomplete || a.Snapshot.Absent || a.Snapshot.Disconnected || n.Snapshot.Absent || n.Snapshot.Disconnected || at.IsZero() {
			return ErrInvalid
		}
		reports := []correlation.NativeReport{}
		if err := walk(ctx, tx, "history/"+native.Key()+"/", func(e Entry) error {
			var o Observation
			if err := json.Unmarshal(e.Value, &o); err != nil {
				return err
			}
			identity := NativeIdentity{}
			if o.Native != nil {
				identity = *o.Native
			}
			reports = append(reports, correlation.NativeReport{Source: o.Source, Serial: identity.Serial, Method: identity.Method, Authenticated: identity.Authenticated, ObservedAt: o.ObservedAt, Fields: o.Fields, Raw: o.Raw})
			return nil
		}); err != nil {
			return err
		}
		c, err := correlation.Correlate(correlation.AppleDevice{AccountID: apple.AccountID, Resource: collection.Resource{ID: apple.ResourceID, Type: "orgDevices", Raw: a.Snapshot.Raw}, ObservedAt: a.Snapshot.ObservedAt}, reports)
		if err != nil {
			return err
		}
		if len(c.Matched()) == 0 {
			return ErrConflict
		}
		out = SerialLink{Apple: apple, Native: native, Serial: CanonicalSerial(c.Matched()[0].Serial), LinkedAt: at}
		return put(ctx, tx, "link/"+apple.Key()+"/"+native.Key(), out)
	})
	return out, err
}

// Links lists checked relationships for one source without combining its data.
func (r *Repository) Links(ctx context.Context, ref source.Reference) ([]SerialLink, error) {
	out := []SerialLink{}
	err := walk(ctx, r.Backend, "link/", func(e Entry) error {
		var link SerialLink
		if err := json.Unmarshal(e.Value, &link); err != nil {
			return err
		}
		if link.Apple == ref || link.Native == ref {
			out = append(out, link)
		}
		return nil
	})
	return out, err
}

// Flatten retains containers and leaves as dotted paths without coercing values.
func Flatten(raw json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	var visit func(string, json.RawMessage)
	visit = func(path string, value json.RawMessage) {
		if path != "" {
			out[path] = append(json.RawMessage(nil), value...)
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) != nil {
			return
		}
		for k, v := range object {
			escaped := strings.ReplaceAll(strings.ReplaceAll(k, `\`, `\\`), ".", `\.`)
			next := escaped
			if path != "" {
				next = path + "." + escaped
			}
			visit(next, v)
		}
	}
	visit("", raw)
	return out
}

// String reads a scalar string without coercing null or other types.
func String(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }

// Time reads an RFC3339 Apple timestamp.
func Time(raw json.RawMessage) (time.Time, bool) {
	t, e := time.Parse(time.RFC3339Nano, String(raw))
	return t, e == nil
}

// ResourceObservation constructs a lossless snapshot from a JSON resource.
func ResourceObservation(ref source.Reference, raw json.RawMessage, at time.Time, ttl time.Duration) (Observation, error) {
	if !json.Valid(raw) {
		return Observation{}, fmt.Errorf("%w: source JSON", ErrInvalid)
	}
	return Observation{Source: ref, Raw: append(json.RawMessage(nil), raw...), Fields: Flatten(raw), ObservedAt: at, ExpiresAt: at.Add(ttl)}, nil
}

// invalidateLinks makes changed or disconnected source evidence require an
// explicit fresh correlation. It runs in the same evidence transaction.
func invalidateLinks(ctx context.Context, tx Tx, ref source.Reference) error {
	return walk(ctx, tx, "link/", func(e Entry) error {
		var link SerialLink
		if err := json.Unmarshal(e.Value, &link); err != nil {
			return err
		}
		if link.Apple == ref || link.Native == ref {
			return tx.Delete(ctx, e.Key)
		}
		return nil
	})
}

// Envelopes returns original legacy source envelopes retained during migration.
// Unknown metadata, source fields and observation timestamps remain opaque
// provenance; they are never interpreted as normalized facts or authentication.
func (r *Repository) Envelopes(ctx context.Context, ref source.Reference) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	err := walk(ctx, r.Backend, "envelope/"+ref.Key()+"/", func(entry Entry) error { out = append(out, append(json.RawMessage(nil), entry.Value...)); return nil })
	return out, err
}

// putResource retains exact source bytes internally without adding duplicate
// bookkeeping to the exported resource API representation.
func putResource(ctx context.Context, tx Tx, record ResourceRecord) error {
	return put(ctx, tx, "resource/"+record.ID, struct {
		ResourceRecord
		OriginalJSON []byte `json:"original_json,omitempty"`
	}{record, append([]byte(nil), record.Snapshot.Raw...)})
}

// completeOriginal distinguishes a retained resource from missing original data.
// A successful null assignment is a value; null is not an organization device.
func completeOriginal(ref source.Reference, raw json.RawMessage) bool {
	if !json.Valid(raw) {
		return false
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return ref.Kind == "axm.assignment"
	}
	if ref.Kind == "axm.assignment" {
		var identity struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		return json.Unmarshal(raw, &identity) == nil && identity.ID != "" && identity.Type == "mdmServers"
	}
	if ref.Kind != "axm.device" {
		return true
	}
	var resource struct {
		ID         string                     `json:"id"`
		Type       string                     `json:"type"`
		Attributes map[string]json.RawMessage `json:"attributes"`
	}
	return json.Unmarshal(raw, &resource) == nil && resource.ID == ref.ResourceID && resource.Type == "orgDevices" && resource.Attributes != nil
}

// knownNativeProvenance requires an authenticated, explicitly recorded enrollment
// method. Link eligibility remains a separate correlator decision.
func knownNativeProvenance(identity *NativeIdentity) bool {
	if identity == nil || !identity.Authenticated {
		return false
	}
	switch identity.Method {
	case correlation.AutomatedDeviceEnrollment, correlation.AccountDrivenDeviceEnrollment, correlation.ProfileBasedDeviceEnrollment, correlation.UserEnrollment:
		return true
	default:
		return false
	}
}
