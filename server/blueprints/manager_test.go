package blueprints_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/ddmstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// configuration builds a blueprint containing a math settings declaration.
func configuration(t *testing.T) blueprint.Spec {
	t.Helper()
	c, err := blueprint.NewDeclaration("math", &schema.MathSettings{})
	if err != nil {
		t.Fatal(err)
	}
	return blueprint.Spec{Identifier: "engineering", Declarations: []blueprint.Declaration{c}}
}

// profileBytes encodes a configuration-profile fixture containing the supplied setting.
func profileBytes(t *testing.T, setting string) []byte {
	t.Helper()
	p := profileValue(setting)
	b, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// profileValue builds a system configuration profile containing the supplied setting.
func profileValue(setting string) *profile.Profile {
	return &profile.Profile{Identifier: "com.example.blueprint", UUID: "6C9B0C20-0000-7000-8000-000000000001", Scope: profile.ScopeSystem, Payloads: []profile.Payload{{Identifier: "com.example.blueprint.settings", UUID: "6C9B0C20-0000-7000-8000-000000000002", Content: &profile.Raw{Type: "com.example.settings", Keys: map[string]any{"Setting": setting}}}}}
}

// memoryConfig creates an in-memory DDM engine and configuration-profile manager for blueprint
// tests.
func memoryConfig(t *testing.T) blueprints.Config {
	t.Helper()
	e, err := ddm.New(ddm.Config{Store: inmem.New(), Expander: configurationprofile.Expander{BaseURL: "https://mdm.example"}})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewMemory()
	profiles, err := configurationprofile.New(configurationprofile.Config{Engine: e, State: st, BaseURL: "https://mdm.example"})
	if err != nil {
		t.Fatal(err)
	}
	return blueprints.Config{Engine: e, State: st, ConfigurationProfiles: profiles}
}

// sqlConfig creates SQL-backed DDM and state stores with a shared transaction runner for blueprint
// tests.
func sqlConfig(t *testing.T, db *sql.DB, d sqlcommon.Dialect) blueprints.Config {
	t.Helper()
	st, err := sqlstore.Open(t.Context(), db, d, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := statestore.Open(t.Context(), db, d)
	if err != nil {
		t.Fatal(err)
	}
	e, err := ddm.New(ddm.Config{Store: st, Expander: configurationprofile.Expander{BaseURL: "https://mdm.example"}})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := configurationprofile.New(configurationprofile.Config{Engine: e, State: s, BaseURL: "https://mdm.example"})
	if err != nil {
		t.Fatal(err)
	}
	return blueprints.Config{Engine: e, State: s, Run: (sqlcommon.UnitOfWork{DB: db, Dialect: d}).Run, ConfigurationProfiles: profiles}
}

// TestManager checks blueprint revision, transaction, assignment, and profile behavior with memory
// and SQLite stores.
func TestManager(t *testing.T) {
	t.Run("memory", func(t *testing.T) { exercise(t, memoryConfig(t)) })
	t.Run("sqlite", func(t *testing.T) {
		db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "blueprints.db"), sqlite.Options{}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		exercise(t, sqlConfig(t, db, sqlite.Dialect))
	})
}

// exercise checks blueprint revision preconditions, idempotence, concurrent publication, rollback,
// assignment, and deletion.
func exercise(t *testing.T, cfg blueprints.Config) {
	t.Helper()
	ctx := t.Context()
	m, err := blueprints.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	spec := configuration(t)
	first, err := m.Publish(ctx, spec, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(ctx, spec, ""); !errors.Is(err, ddm.ErrConflict) {
		t.Fatal("missing precondition", err)
	}
	id := mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}
	if changed, err := m.Assign(ctx, id, spec.Identifier, true); err != nil || !changed {
		t.Fatal(changed, err)
	}
	snapshot, err := cfg.Engine.Manifest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	same, err := m.Publish(ctx, spec, first.Revision)
	if err != nil || same.Revision != first.Revision {
		t.Fatal("not idempotent", err)
	}
	current, err := cfg.Engine.Manifest(ctx, id)
	if err != nil || current.DeclarationsToken != snapshot.DeclarationsToken {
		t.Fatal("unchanged content changed token", err)
	}
	// Two simultaneous writers based on the same revision cannot both commit.
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for i := range 2 {
		wg.Go(func() {
			next := spec
			next.Name = fmt.Sprint(i)
			_, err := m.Publish(ctx, next, first.Revision)
			out <- err
		})
	}
	wg.Wait()
	close(out)
	success, conflict := 0, 0
	for err := range out {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ddm.ErrConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent publication", success, conflict)
	}
	latest, err := m.Get(ctx, spec.Identifier)
	if err != nil {
		t.Fatal(err)
	}
	// A state failure rolls back source, declarations, membership and changes.
	badCfg := cfg
	badCfg.State = failState{Store: cfg.State}
	bad, err := blueprints.New(badCfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Publish(ctx, blueprint.Spec{Identifier: spec.Identifier}, latest.Revision); err == nil {
		t.Fatal("state failure accepted")
	}
	got, err := m.Get(ctx, spec.Identifier)
	if err != nil || got.Revision != latest.Revision {
		t.Fatal("source rollback", err)
	}
	if members, err := cfg.Engine.SetDeclarations(ctx, blueprint.SetName(spec.Identifier)); err != nil || len(members) != 2 {
		t.Fatal("wire rollback", members, err)
	}
	page, err := m.List(ctx, paging.Page{Limit: 1})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	if cfg.Run != nil {
		abort := errors.New("abort outer transaction")
		err := cfg.Run(ctx, func(ctx context.Context) error {
			if _, err := m.Publish(ctx, blueprint.Spec{Identifier: spec.Identifier}, latest.Revision); err != nil {
				return err
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Fatal(err)
		}
		got, err := m.Get(ctx, spec.Identifier)
		if err != nil || got.Revision != latest.Revision {
			t.Fatal("outer rollback", err)
		}
	}
	if err := m.Delete(ctx, spec.Identifier, "stale"); !errors.Is(err, ddm.ErrConflict) {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, spec.Identifier, latest.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, spec.Identifier); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal(err)
	}
	if assigned, err := cfg.Engine.EnrollmentSets(ctx, id); err != nil || len(assigned) != 0 {
		t.Fatal(assigned, err)
	}
	// A deleted and recreated record must not reuse its optimistic revision.
	recreated, err := m.Publish(ctx, spec, "")
	if err != nil || recreated.Revision == first.Revision {
		t.Fatal("revision ABA", err)
	}
	exerciseProfiles(t, m, cfg.ConfigurationProfiles, cfg.Engine)
}

type failState struct{ state.Store }

// Update runs the transaction callback and then injects a metadata failure to force rollback.
func (s failState) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		return errors.New("injected metadata failure")
	})
}

// exerciseProfiles checks immutable profile revisions and download access against assignment and
// advertised snapshots.
func exerciseProfiles(t *testing.T, m *blueprints.Manager, profiles *configurationprofile.Manager, e *ddm.Engine) {
	t.Helper()
	ctx := t.Context()
	one, two := profileBytes(t, "one"), profileBytes(t, "two")
	p1, err := profiles.Upload(ctx, one)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := profiles.Upload(ctx, two)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := profiles.Upload(ctx, one); err != nil || again.Revision != p1.Revision {
		t.Fatal("profile not immutable", err)
	}
	page, err := profiles.List(ctx, paging.Page{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	page, err = profiles.List(ctx, paging.Page{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(page.Items) != 1 || page.NextCursor != "" {
		t.Fatal(page, err)
	}
	id := mdm.EnrollmentID{ID: "profile-device", Channel: mdm.ChannelDevice}
	spec := blueprint.Spec{Identifier: "profiles", Declarations: []blueprint.Declaration{{Identifier: "settings", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: p1.Revision}}}}
	r, err := m.Publish(ctx, spec, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Assign(ctx, id, "profiles", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := profiles.Fetch(ctx, id, p1.Revision); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("unadvertised profile", err)
	}
	if _, err := e.Manifest(ctx, id); err != nil {
		t.Fatal(err)
	}
	data, _, err := profiles.Fetch(ctx, id, p1.Revision)
	if err != nil || !bytes.Equal(data, one) {
		t.Fatal("profile download", err)
	}
	spec.Declarations[0].ConfigurationProfile.Revision = p2.Revision
	r, err = m.Publish(ctx, spec, r.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := profiles.Fetch(ctx, id, p1.Revision); err != nil {
		t.Fatal("old snapshot revision lost", err)
	}
	if _, _, err := profiles.Fetch(ctx, id, p2.Revision); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("unadvertised revision leaked", err)
	}
	if _, err := e.Manifest(ctx, id); err != nil {
		t.Fatal(err)
	}
	data, _, err = profiles.Fetch(ctx, id, p2.Revision)
	if err != nil || !bytes.Equal(data, two) {
		t.Fatal(err)
	}
	if _, _, err := profiles.Fetch(ctx, id, p1.Revision); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("old revision after refresh", err)
	}
	if _, err := m.Assign(ctx, id, "profiles", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := profiles.Fetch(ctx, id, p2.Revision); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("unassigned profile leaked", err)
	}
	if _, err := m.Assign(ctx, id, "profiles", true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(ctx, blueprint.Spec{Identifier: "profiles"}, r.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, err := profiles.Fetch(ctx, id, p2.Revision); !errors.Is(err, ddm.ErrNotFound) {
		t.Fatal("removed declaration leaked", err)
	}
}

// TestConfigurationProfileValidation checks configuration-profile signature preservation and
// rejection of unsupported signed assets.
func TestConfigurationProfileValidation(t *testing.T) {
	cfg := memoryConfig(t)
	profiles := cfg.ConfigurationProfiles
	m, err := blueprints.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, []byte("invalid"), bytes.Repeat([]byte("x"), configurationprofile.MaxBytes+1)} {
		if _, err := profiles.Upload(t.Context(), data); !errors.Is(err, ddm.ErrInvalid) {
			t.Fatal(err)
		}
	}
	p := profileValue("signed")
	ca, err := testpki.NewCA("signer")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Sign(ca.Cert, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	info, err := profiles.Upload(t.Context(), b)
	if err != nil || !info.Signed {
		t.Fatal(info, err)
	}
	got, _, err := profiles.Data(t.Context(), info.Revision)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatal("signature bytes changed", err)
	}
	spec := blueprint.Spec{Identifier: "signed", Declarations: []blueprint.Declaration{{Identifier: "p", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: info.Revision, UseProfileAssetReference: true}}}}
	if _, err := m.Validate(t.Context(), spec, support.Target{}); err == nil {
		t.Fatal("signed asset accepted")
	}
}
