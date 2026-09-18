package blueprints

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

const specPrefix = "blueprint/spec/"

// Config uses the application's existing encrypted state store. For persistent
// stores Run must join both stores to the same SQL unit of work. With the
// reference memory stores it may be nil: the DDM transaction encloses the state
// transaction, and has no fallible operations after the state callback commits.
type Config struct {
	Engine                *ddm.Engine
	State                 state.Store
	Run                   func(context.Context, func(context.Context) error) error
	ConfigurationProfiles *configurationprofile.Manager
}

type Manager struct{ cfg Config }

// Record retains authoring source separately from Apple's wire declarations.
type Record struct {
	Spec                 blueprint.Spec
	Revision             string
	CreatedAt, UpdatedAt time.Time
	Compiled             *blueprint.Compiled
}

func New(cfg Config) (*Manager, error) {
	if cfg.Engine == nil || cfg.State == nil {
		return nil, ddm.ErrInvalid
	}
	if cfg.Run == nil {
		cfg.Run = func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
	}
	return &Manager{cfg: cfg}, nil
}

func hash(b []byte) string     { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func specKey(id string) string { return specPrefix + id }

func read[T any](ctx context.Context, reader state.Reader, key string) (T, error) {
	var out T
	r, err := reader.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return out, ddm.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(r.Value, &out); err != nil {
		return out, fmt.Errorf("blueprints: stored record: %w", err)
	}
	return out, nil
}

func put(ctx context.Context, tx state.Tx, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: key, Value: b})
}

// Get reads the current authoring revision.
func (m *Manager) Get(ctx context.Context, id string) (Record, error) {
	if !blueprint.ValidIdentifier(id) {
		return Record{}, ddm.ErrInvalid
	}
	return read[Record](ctx, m.cfg.State, specKey(id))
}

func list[T any](ctx context.Context, st state.Store, prefix string, page paging.Page) (paging.Result[T], error) {
	var out paging.Result[T]
	rows, err := st.List(ctx, prefix, prefix+page.Cursor, page.Size()+1)
	if err != nil {
		return out, err
	}
	for _, row := range rows[:min(len(rows), page.Size())] {
		var item T
		if err := json.Unmarshal(row.Value, &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if len(rows) > page.Size() {
		out.NextCursor = strings.TrimPrefix(rows[page.Size()-1].Key, prefix)
	}
	return out, nil
}

func (m *Manager) List(ctx context.Context, page paging.Page) (paging.Result[Record], error) {
	return list[Record](ctx, m.cfg.State, specPrefix, page)
}

// Validate compiles without publishing, resolving only immutable uploaded profiles.
func (m *Manager) Validate(ctx context.Context, spec blueprint.Spec, target support.Target) (*blueprint.Compiled, error) {
	opts := blueprint.Options{Target: target, ConfigurationProfiles: map[string]blueprint.ConfigurationProfileDescriptor{}}
	for _, c := range spec.Declarations {
		if c.ConfigurationProfile == nil {
			continue
		}
		if m.cfg.ConfigurationProfiles == nil {
			return nil, fmt.Errorf("%w: configuration profile storage is not configured", ddm.ErrInvalid)
		}
		p, err := m.cfg.ConfigurationProfiles.Get(ctx, c.ConfigurationProfile.Revision)
		if err != nil {
			return nil, err
		}
		profileURL := m.cfg.ConfigurationProfiles.ProfileURL(p.Revision)
		if profileURL == "" {
			return nil, fmt.Errorf("%w: hosted profiles require the public HTTPS enrollment URL", ddm.ErrInvalid)
		}
		opts.ConfigurationProfiles[p.Revision] = blueprint.ConfigurationProfileDescriptor{URL: profileURL, Size: p.Size, SHA256: p.Revision, Signed: p.Signed, ContentType: p.ContentType}
	}
	return blueprint.Compile(spec, opts)
}

func normalized(spec blueprint.Spec) (blueprint.Spec, []byte, error) {
	// Round-trip first so sorting and canonicalization cannot mutate the caller.
	b, err := json.Marshal(spec)
	if err != nil {
		return spec, nil, err
	}
	var cloned blueprint.Spec
	if err := json.Unmarshal(b, &cloned); err != nil {
		return spec, nil, err
	}
	spec = cloned
	slices.SortFunc(spec.Declarations, func(a, b blueprint.Declaration) int { return strings.Compare(a.Identifier, b.Identifier) })
	slices.SortFunc(spec.Activations, func(a, b blueprint.Activation) int { return strings.Compare(a.Identifier, b.Identifier) })
	for i := range spec.Declarations {
		p := &spec.Declarations[i].Payload
		if len(*p) > 0 {
			if err := p.Canonicalize(); err != nil {
				return spec, nil, err
			}
		}
	}
	for i := range spec.Activations {
		slices.Sort(spec.Activations[i].StandardConfigurations)
		spec.Activations[i].StandardConfigurations = slices.Compact(spec.Activations[i].StandardConfigurations)
	}
	b, err = json.Marshal(spec)
	return spec, b, err
}

// mutate orders locks consistently: publication, then authoring state. SQL stores
// join Run; memory stores commit state last inside the DDM private write set.
func (m *Manager) mutate(ctx context.Context, id string, fn func(context.Context, ddm.Tx, state.Tx) error) error {
	if !blueprint.ValidIdentifier(id) {
		return ddm.ErrInvalid
	}
	return m.cfg.Run(ctx, func(ctx context.Context) error {
		return m.cfg.Engine.Store().Update(ctx, func(tx ddm.Tx) error {
			locker, ok := tx.(ddm.PublicationLocker)
			if !ok {
				return ddm.ErrInvalid
			}
			if err := locker.LockPublication(ctx, blueprint.SetName(id)); err != nil {
				return err
			}
			return m.cfg.State.Update(ctx, []string{specKey(id)}, func(st state.Tx) error { return fn(ctx, tx, st) })
		})
	})
}

// Publish replaces all declarations while preserving assignments. Empty expected
// creates a Blueprint; updates require the current revision. Identical retries
// with the current revision are idempotent and enqueue no DDM changes.
func (m *Manager) Publish(ctx context.Context, spec blueprint.Spec, expected string) (Record, error) {
	compiled, err := m.Validate(ctx, spec, support.Target{})
	if err != nil {
		return Record{}, err
	}
	spec, canonical, err := normalized(spec)
	if err != nil {
		return Record{}, err
	}
	var result Record
	err = m.mutate(ctx, spec.Identifier, func(ctx context.Context, tx ddm.Tx, st state.Tx) error {
		old, err := read[Record](ctx, st, specKey(spec.Identifier))
		if err != nil && !errors.Is(err, ddm.ErrNotFound) {
			return err
		}
		if old.Revision != expected {
			return ddm.ErrConflict
		}
		_, prior, err := normalized(old.Spec)
		if err != nil {
			return err
		}
		result = Record{Spec: spec, Revision: hash(append([]byte(expected+rand.Text()), canonical...)), CreatedAt: old.CreatedAt, UpdatedAt: st.Now(), Compiled: compiled}
		if result.CreatedAt.IsZero() {
			result.CreatedAt = st.Now()
		}
		if bytes.Equal(prior, canonical) {
			result = old
		}
		if _, err := m.cfg.Engine.PublishSetTx(ctx, tx, compiled.Publication); err != nil {
			return err
		}
		return put(ctx, st, specKey(spec.Identifier), result)
	})
	if err != nil {
		return Record{}, err
	}
	return result, nil
}

func (m *Manager) Delete(ctx context.Context, id, expected string) error {
	return m.mutate(ctx, id, func(ctx context.Context, tx ddm.Tx, st state.Tx) error {
		old, err := read[Record](ctx, st, specKey(id))
		if err != nil {
			return err
		}
		if expected == "" || old.Revision != expected {
			return ddm.ErrConflict
		}
		// Empty publication records removal notifications before deleting bindings.
		if _, err := m.cfg.Engine.PublishSetTx(ctx, tx, ddm.SetPublication{Name: blueprint.SetName(id)}); err != nil {
			return err
		}
		if err := tx.DeleteSet(ctx, blueprint.SetName(id)); err != nil {
			return err
		}
		return st.Delete(ctx, specKey(id))
	})
}

func (m *Manager) Assign(ctx context.Context, id mdm.EnrollmentID, name string, assigned bool) (bool, error) {
	if err := id.Validate(); err != nil {
		return false, ddm.ErrInvalid
	}
	var changed bool
	err := m.mutate(ctx, name, func(ctx context.Context, tx ddm.Tx, st state.Tx) error {
		if _, err := read[Record](ctx, st, specKey(name)); err != nil {
			return err
		}
		var err error
		if assigned {
			changed, err = tx.AssignSet(ctx, id, blueprint.SetName(name), st.Now())
		} else {
			changed, err = tx.UnassignSet(ctx, id, blueprint.SetName(name))
		}
		if err != nil || !changed {
			return err
		}
		return tx.RecordChanges(ctx, []mdm.EnrollmentID{id}, ddm.ReasonAssignment, st.Now())
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}
