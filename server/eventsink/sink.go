// Package sink is in event/sink; see doc.go for the package comment.
package eventsink

import (
	"sort"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
)

// Projection reduces an event payload to the fields that may leave the
// process. It returns nil when the payload is not the type it expects, so a
// mismatch degrades to a metadata-only record rather than leaking a value
// nobody projected.
type Projection func(data any) map[string]any

// Record is one event reduced to what may leave the process: the metadata
// every event has, plus whatever its projection allowed through. Event.Data
// itself never appears here.
type Record struct {
	Type    string         `json:"type"`
	At      time.Time      `json:"at"`
	Actor   string         `json:"actor,omitempty"`
	Channel string         `json:"channel,omitempty"`
	ID      string         `json:"id,omitempty"`
	Parent  string         `json:"parent_id,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// Registry decides what each event type may publish.
//
// It is default-deny: an event type with no entry projects nothing but
// metadata, so adding an event without thinking about its payload cannot leak
// one. Registering nil is how a type says "metadata is all there is",
// deliberately rather than by omission, and Known reports the difference so a
// test can insist every declared type was considered.
type Registry struct {
	mu      sync.RWMutex
	entries map[event.Type]Projection
	known   map[event.Type]bool
}

// NewRegistry returns an empty registry. Default is the populated one.
func NewRegistry() *Registry {
	return &Registry{entries: map[event.Type]Projection{}, known: map[event.Type]bool{}}
}

// Register records how one event type projects. A nil Projection marks the
// type metadata-only: considered, and carrying nothing further.
func (r *Registry) Register(t event.Type, p Projection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.known[t] = true
	if p != nil {
		r.entries[t] = p
	}
}

// Known reports whether the type was registered at all, projection or not.
// An unknown type still yields a valid metadata-only Record; this is how a
// test tells "deliberately bare" from "forgotten".
func (r *Registry) Known(t event.Type) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.known[t]
}

// Types lists every registered type, sorted, for tests and diagnostics.
func (r *Registry) Types() []event.Type {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]event.Type, 0, len(r.known))
	for t := range r.known {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Project reduces an event to its publishable Record.
func (r *Registry) Project(e event.Event) Record {
	rec := Record{
		Type:   string(e.Type),
		At:     e.At,
		Actor:  e.Actor,
		ID:     e.Enrollment.ID,
		Parent: e.Enrollment.ParentID,
	}
	if e.Enrollment.ID != "" || e.Enrollment.Channel != 0 {
		rec.Channel = e.Enrollment.Channel.String()
	}
	r.mu.RLock()
	p := r.entries[e.Type]
	r.mu.RUnlock()
	if p == nil || e.Data == nil {
		return rec
	}
	if f := p(e.Data); len(f) > 0 {
		rec.Fields = f
	}
	return rec
}

// enrollmentIDs projects a slice of enrollment identifiers, which is all the
// certificate-reuse event carries.
func enrollmentIDs(data any) map[string]any {
	ids, ok := data.([]mdm.EnrollmentID)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.ID)
	}
	return map[string]any{"others": out}
}

// passthrough forwards a map payload's listed keys. The ACME and admin events
// already publish a small map of safe values; this keeps that explicit rather
// than trusting whatever the publisher happened to put there.
func passthrough(keys ...string) Projection {
	return func(data any) map[string]any {
		m, ok := data.(map[string]any)
		if !ok {
			return nil
		}
		out := map[string]any{}
		for _, k := range keys {
			if v, ok := m[k]; ok {
				out[k] = v
			}
		}
		return out
	}
}

// str projects a bare string payload under one key.
func str(key string) Projection {
	return func(data any) map[string]any {
		s, ok := data.(string)
		if !ok || s == "" {
			return nil
		}
		return map[string]any{key: s}
	}
}
