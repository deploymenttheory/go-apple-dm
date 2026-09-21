package blueprints_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

// newManager constructs a blueprint manager, failing the test on invalid configuration.
func newManager(t *testing.T, cfg blueprints.Config) *blueprints.Manager {
	t.Helper()
	m, err := blueprints.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestManagerRejectsInvalidOperations checks that manager rejects invalid operations.
func TestManagerRejectsInvalidOperations(t *testing.T) {
	cfg := memoryConfig(t)
	for _, bad := range []blueprints.Config{{}, {Engine: cfg.Engine}, {State: cfg.State}} {
		if m, err := blueprints.New(bad); m != nil || !errors.Is(err, ddm.ErrInvalid) {
			t.Fatalf("New = %v, %v", m, err)
		}
	}
	m := newManager(t, cfg)
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	for _, tc := range []struct {
		name string
		run  func() error
		want error
	}{
		{"get invalid identifier", func() error { _, err := m.Get(t.Context(), "../invalid"); return err }, ddm.ErrInvalid},
		{"delete invalid identifier", func() error { return m.Delete(t.Context(), "../invalid", "revision") }, ddm.ErrInvalid},
		{"publish invalid source", func() error { _, err := m.Publish(t.Context(), blueprint.Spec{}, ""); return err }, ddm.ErrInvalidDeclaration},
		{"assign invalid enrollment", func() error { _, err := m.Assign(t.Context(), mdm.EnrollmentID{}, "missing", true); return err }, ddm.ErrInvalid},
		{"delete missing source", func() error { return m.Delete(t.Context(), "missing", "revision") }, ddm.ErrNotFound},
		{"assign missing source", func() error { _, err := m.Assign(t.Context(), id, "missing", true); return err }, ddm.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestValidateConfigurationProfileRequirements checks validate configuration profile requirements.
func TestValidateConfigurationProfileRequirements(t *testing.T) {
	for _, mode := range []string{"unconfigured", "missing revision", "missing public URL"} {
		t.Run(mode, func(t *testing.T) {
			cfg := memoryConfig(t)
			revision := strings.Repeat("a", 64)
			want := ddm.ErrInvalid
			switch mode {
			case "unconfigured":
				cfg.ConfigurationProfiles = nil
			case "missing revision":
				want = ddm.ErrNotFound
			case "missing public URL":
				var err error
				cfg.ConfigurationProfiles, err = configurationprofile.New(configurationprofile.Config{State: cfg.State, Engine: cfg.Engine})
				if err != nil {
					t.Fatal(err)
				}
				info, err := cfg.ConfigurationProfiles.Upload(t.Context(), profileBytes(t, "test"))
				if err != nil {
					t.Fatal(err)
				}
				revision = info.Revision
			}
			m := newManager(t, cfg)
			spec := blueprint.Spec{Identifier: "profiles", Declarations: []blueprint.Declaration{{Identifier: "settings", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: revision}}}}
			if compiled, err := m.Validate(t.Context(), spec, support.Target{}); compiled != nil || !errors.Is(err, want) {
				t.Fatalf("Validate = %v, %v; want %v", compiled, err, want)
			}
			if _, err := m.Get(t.Context(), spec.Identifier); !errors.Is(err, ddm.ErrNotFound) {
				t.Fatal("validation wrote source", err)
			}
		})
	}
}

// TestPublicationNormalizationAndPagination checks publication normalization and pagination.
func TestPublicationNormalizationAndPagination(t *testing.T) {
	ctx := t.Context()
	cfg := memoryConfig(t)
	m := newManager(t, cfg)
	spec := configuration(t)
	spec.Declarations[0].Payload = jsontext.Value(`{"SystemBehavior":{"MathNotes":false,"KeyboardSuggestions":false}}`)
	spec.Declarations = append(spec.Declarations, blueprint.Declaration{Identifier: "disk", Type: schema.DeclarationTypeDiskManagementSettings, Payload: jsontext.Value(`{}`)})
	spec.Activations = []blueprint.Activation{
		{Identifier: "second", StandardConfigurations: []string{"math", "disk", "math"}},
		{Identifier: "first", StandardConfigurations: []string{"math"}, Predicate: "TRUEPREDICATE"},
	}
	before, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Publish(ctx, spec, "")
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(spec)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("publication mutated its input", err)
	}
	if first.Spec.Declarations[0].Identifier != "disk" || first.Spec.Activations[0].Identifier != "first" || !slices.Equal(first.Spec.Activations[1].StandardConfigurations, []string{"disk", "math"}) {
		t.Fatal("source was not normalized", first.Spec)
	}
	slices.Reverse(spec.Declarations)
	slices.Reverse(spec.Activations)
	spec.Declarations[1].Payload = jsontext.Value(`{ "SystemBehavior": { "KeyboardSuggestions": false, "MathNotes": false } }`)
	spec.Activations[1].StandardConfigurations = []string{"disk", "math"}
	again, err := m.Publish(ctx, spec, first.Revision)
	if err != nil || !reflect.DeepEqual(again, first) {
		t.Fatal("equivalent publication changed revision, timestamps or declarations", again, err)
	}
	other := configuration(t)
	other.Identifier = "other"
	if _, err := m.Publish(ctx, other, ""); err != nil {
		t.Fatal(err)
	}
	page, err := m.List(ctx, paging.Page{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Spec.Identifier != spec.Identifier || page.NextCursor != spec.Identifier {
		t.Fatal("first page", page, err)
	}
	page, err = m.List(ctx, paging.Page{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(page.Items) != 1 || page.Items[0].Spec.Identifier != "other" || page.NextCursor != "" {
		t.Fatal("second page", page, err)
	}
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	for _, step := range []struct{ assigned, changed bool }{{true, true}, {true, false}, {false, true}, {false, false}} {
		if changed, err := m.Assign(ctx, id, spec.Identifier, step.assigned); err != nil || changed != step.changed {
			t.Fatalf("Assign(%v) = %v, %v; want %v", step.assigned, changed, err, step.changed)
		}
	}
}

type failingMetadataStore struct {
	state.Store
	getErr, listErr, putErr, deleteErr error
}

// Get injects a metadata-read failure or delegates to the state store.
func (s failingMetadataStore) Get(ctx context.Context, key string) (state.Record, error) {
	if s.getErr != nil {
		return state.Record{}, s.getErr
	}
	return s.Store.Get(ctx, key)
}

// List injects a metadata-list failure or delegates to the state store.
func (s failingMetadataStore) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.Store.List(ctx, prefix, after, limit)
}

// Update wraps metadata transactions with configured failures.
func (s failingMetadataStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(failingMetadataTx{Tx: tx, faults: s}) })
}

type failingMetadataTx struct {
	state.Tx
	faults failingMetadataStore
}

// Get injects a metadata-read failure or delegates to the transaction.
func (tx failingMetadataTx) Get(ctx context.Context, key string) (state.Record, error) {
	if tx.faults.getErr != nil {
		return state.Record{}, tx.faults.getErr
	}
	return tx.Tx.Get(ctx, key)
}

// Put injects a metadata-write failure or delegates to the transaction.
func (tx failingMetadataTx) Put(ctx context.Context, r state.Record) error {
	if tx.faults.putErr != nil {
		return tx.faults.putErr
	}
	return tx.Tx.Put(ctx, r)
}

// Delete injects a metadata-delete failure or delegates to the transaction.
func (tx failingMetadataTx) Delete(ctx context.Context, key string) error {
	if tx.faults.deleteErr != nil {
		return tx.faults.deleteErr
	}
	return tx.Tx.Delete(ctx, key)
}

// TestStoredSourceReadFailures checks stored source read failures.
func TestStoredSourceReadFailures(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "storage error", true: "corrupt JSON"}[corrupt], func(t *testing.T) {
			cfg := memoryConfig(t)
			spec := configuration(t)
			failure := errors.New("metadata unavailable")
			if corrupt {
				err := cfg.State.Update(t.Context(), []string{"blueprint/spec/engineering"}, func(tx state.Tx) error {
					return tx.Put(t.Context(), state.Record{Key: "blueprint/spec/engineering", Value: []byte(`{`)})
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				cfg.State = failingMetadataStore{Store: cfg.State, getErr: failure, listErr: failure}
			}
			m := newManager(t, cfg)
			for _, op := range []struct {
				name string
				run  func() error
			}{
				{"get", func() error { _, err := m.Get(t.Context(), spec.Identifier); return err }},
				{"list", func() error { _, err := m.List(t.Context(), paging.Page{}); return err }},
				{"publish", func() error { _, err := m.Publish(t.Context(), spec, ""); return err }},
				{"delete", func() error { return m.Delete(t.Context(), spec.Identifier, "revision") }},
				{"assign", func() error {
					_, err := m.Assign(t.Context(), mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}, spec.Identifier, true)
					return err
				}},
			} {
				t.Run(op.name, func(t *testing.T) {
					err := op.run()
					if err == nil || (!corrupt && !errors.Is(err, failure)) {
						t.Fatal("stored source error was ignored", err)
					}
				})
			}
			sets, err := cfg.Engine.ListSets(t.Context(), paging.Page{})
			if err != nil || len(sets.Items) != 0 {
				t.Fatal("failed operation changed declaration sets", sets, err)
			}
		})
	}
}

type wrappedPublicationStore struct {
	ddm.Store
	wrap func(ddm.Tx) ddm.Tx
}

// Update wraps the publication transaction before invoking the callback.
func (s wrappedPublicationStore) Update(ctx context.Context, fn func(ddm.Tx) error) error {
	return s.Store.Update(ctx, func(tx ddm.Tx) error { return fn(s.wrap(tx)) })
}

type failingPublicationTx struct {
	ddm.Tx
	operation string
	err       error
}

// LockPublication injects a publication-lock failure or delegates to a transaction implementing
// PublicationLocker.
func (tx failingPublicationTx) LockPublication(ctx context.Context, name string) error {
	if tx.operation == "lock" {
		return tx.err
	}
	locker, ok := tx.Tx.(ddm.PublicationLocker)
	if !ok {
		return ddm.ErrInvalid
	}
	return locker.LockPublication(ctx, name)
}

// PutSet injects a set-publication failure or delegates to the transaction.
func (tx failingPublicationTx) PutSet(ctx context.Context, name string, at time.Time) (bool, error) {
	if tx.operation == "publish" {
		return false, tx.err
	}
	return tx.Tx.PutSet(ctx, name, at)
}

// DeleteSet injects a set-deletion failure or delegates to the transaction.
func (tx failingPublicationTx) DeleteSet(ctx context.Context, name string) error {
	if tx.operation == "delete set" {
		return tx.err
	}
	return tx.Tx.DeleteSet(ctx, name)
}

// AssignSet injects a set-assignment failure or delegates to the transaction.
func (tx failingPublicationTx) AssignSet(ctx context.Context, id mdm.EnrollmentID, name string, at time.Time) (bool, error) {
	if tx.operation == "assign" {
		return false, tx.err
	}
	return tx.Tx.AssignSet(ctx, id, name, at)
}

// UnassignSet injects a set-unassignment failure or delegates to the transaction.
func (tx failingPublicationTx) UnassignSet(ctx context.Context, id mdm.EnrollmentID, name string) (bool, error) {
	if tx.operation == "unassign" {
		return false, tx.err
	}
	return tx.Tx.UnassignSet(ctx, id, name)
}

// RecordChanges injects a change-recording failure or delegates to the transaction.
func (tx failingPublicationTx) RecordChanges(ctx context.Context, ids []mdm.EnrollmentID, reason string, at time.Time) error {
	if tx.operation == "notify" {
		return tx.err
	}
	return tx.Tx.RecordChanges(ctx, ids, reason, at)
}

// TestMutationFailuresRollBackSourceDeclarationsAndAssignments checks mutation failures roll back
// source declarations and assignments.
func TestMutationFailuresRollBackSourceDeclarationsAndAssignments(t *testing.T) {
	for _, tc := range []struct{ operation, mutation string }{
		{"missing lock", "publish"},
		{"lock", "publish"},
		{"run", "publish"},
		{"publish", "publish"},
		{"put source", "publish"},
		{"publish", "delete"},
		{"delete set", "delete"},
		{"delete source", "delete"},
		{"assign", "assign"},
		{"unassign", "unassign"},
		{"notify", "unassign"},
	} {
		t.Run(tc.operation+" during "+tc.mutation, func(t *testing.T) {
			ctx := t.Context()
			cfg := memoryConfig(t)
			m := newManager(t, cfg)
			spec := configuration(t)
			r, err := m.Publish(ctx, spec, "")
			if err != nil {
				t.Fatal(err)
			}
			id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
			if _, err := m.Assign(ctx, id, spec.Identifier, true); err != nil {
				t.Fatal(err)
			}
			before, err := cfg.Engine.Manifest(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			changes, err := cfg.Engine.Store().PendingChanges(ctx, time.Now().Add(time.Hour), 100)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("injected " + tc.operation + " failure")
			badCfg := cfg
			badCfg.Engine, err = ddm.New(ddm.Config{Store: wrappedPublicationStore{Store: cfg.Engine.Store(), wrap: func(tx ddm.Tx) ddm.Tx {
				if tc.operation == "missing lock" {
					return struct{ ddm.Tx }{tx}
				}
				return failingPublicationTx{Tx: tx, operation: tc.operation, err: failure}
			}}})
			if err != nil {
				t.Fatal(err)
			}
			switch tc.operation {
			case "run":
				badCfg.Run = func(context.Context, func(context.Context) error) error { return failure }
			case "put source":
				badCfg.State = failingMetadataStore{Store: cfg.State, putErr: failure}
			case "delete source":
				badCfg.State = failingMetadataStore{Store: cfg.State, deleteErr: failure}
			}
			bad := newManager(t, badCfg)
			switch tc.mutation {
			case "publish":
				_, err = bad.Publish(ctx, blueprint.Spec{Identifier: spec.Identifier}, r.Revision)
			case "delete":
				err = bad.Delete(ctx, spec.Identifier, r.Revision)
			case "assign":
				_, err = bad.Assign(ctx, mdm.EnrollmentID{ID: "other", Channel: mdm.ChannelDevice}, spec.Identifier, true)
			case "unassign":
				_, err = bad.Assign(ctx, id, spec.Identifier, false)
			}
			want := failure
			if tc.operation == "missing lock" {
				want = ddm.ErrInvalid
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			saved, err := m.Get(ctx, spec.Identifier)
			if err != nil || !reflect.DeepEqual(saved, r) {
				t.Fatal("failed mutation changed source", saved, err)
			}
			after, err := cfg.Engine.Manifest(ctx, id)
			if err != nil || before.DeclarationsToken != after.DeclarationsToken {
				t.Fatal("failed mutation changed manifest", err)
			}
			members, err := cfg.Engine.SetDeclarations(ctx, r.Compiled.Publication.Name)
			if err != nil || len(members) != len(r.Compiled.Publication.Declarations) {
				t.Fatal("failed mutation changed set membership", members, err)
			}
			assignments, err := cfg.Engine.Store().SetEnrollments(ctx, r.Compiled.Publication.Name, paging.Page{})
			if err != nil || !slices.Equal(assignments.Items, []mdm.EnrollmentID{id}) {
				t.Fatal("failed mutation changed assignments", assignments, err)
			}
			afterChanges, err := cfg.Engine.Store().PendingChanges(ctx, time.Now().Add(time.Hour), 100)
			if err != nil || !reflect.DeepEqual(changes, afterChanges) {
				t.Fatal("failed mutation changed notifications", err)
			}
		})
	}
}
