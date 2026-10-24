package recovery

import (
	"errors"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	depsql "github.com/deploymenttheory/go-apple-dm/server/depstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// TestDEPRecoverySchemaRegistersProfileChoices verifies the compiled DEP schema
// includes the replayable choices table in every dialect's checkpoint inventory.
func TestDEPRecoverySchemaRegistersProfileChoices(t *testing.T) {
	for _, dialect := range []sqlcommon.Dialect{sqlite.Dialect, postgres.Dialect, mysql.Dialect} {
		t.Run(dialect.Name, func(t *testing.T) {
			migrations, err := depsql.MigrationSet(dialect)
			if err != nil {
				t.Fatal(err)
			}
			description, err := describe(migrations)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(description.Tables, "dep_profile_choices") || !slices.Equal(description.Versions, []int{1, 2, 3, 4}) {
				t.Fatalf("incomplete DEP recovery schema: %+v", description)
			}
		})
	}
}

// depRecoveryState captures ADE choices and operational state through the public
// store so restore must preserve both intent and the ability to resume safely.
type depRecoveryState struct {
	account         *dep.Account
	profiles        []dep.Profile
	devices         []dep.StoredDevice
	assignment      *dep.Assignment
	current, staged *dep.Keypair
	cursor          dep.Cursor
	session         string
	schedule        dep.AssignmentState
}

// seedDEPRecovery stores two caller profiles, a serial override, observed state,
// sealed credentials and keypairs, a pending request and resumable worker state.
func seedDEPRecovery(t *testing.T, source SQL, keys *crypt.Keyring, now time.Time) depRecoveryState {
	t.Helper()
	ctx := t.Context()
	store, err := depsql.Open(ctx, source.DB, source.Dialect, depsql.Options{SkipMigrate: true, Keyring: keys})
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(24 * time.Hour)
	account := &dep.Account{Name: "ade-recovery", ConsumerKey: "recovery-consumer", ConsumerSecret: "recovery-consumer-secret", AccessToken: "recovery-access-token", AccessSecret: "recovery-access-secret", AccessTokenExpiry: &expires, ServerUUID: "recovery-server", OrgID: "recovery-org", ProfileUUID: "ade-default", CreatedAt: now, UpdatedAt: now}
	if err := store.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	for _, uuid := range []string{"ade-default", "ade-explicit"} {
		if err := store.PutProfile(ctx, account.Name, &dep.Profile{ProfileUUID: uuid, ProfileName: uuid, URL: "https://mdm.example/enroll/ade", Extra: map[string]any{"caller_setting": "retained"}}); err != nil {
			t.Fatal(err)
		}
	}
	account.ProfileTargets = map[string]string{"ADE-EXPLICIT": "ade-explicit"}
	if err := store.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := store.PutDevices(ctx, account.Name, []dep.Device{{SerialNumber: "ADE-EXPLICIT", ProfileUUID: "ade-default", ProfileStatus: dep.ProfileStatusAssigned}, {SerialNumber: "ADE-DEFAULT", ProfileUUID: "ade-default", ProfileStatus: dep.ProfileStatusAssigned}}, now); err != nil {
		t.Fatal(err)
	}
	if err := store.PutAssignment(ctx, &dep.Assignment{Account: account.Name, SerialNumber: "ADE-EXPLICIT", ProfileUUID: "ade-explicit", Status: dep.AssignmentPending, Attempts: 2, AttemptedAt: now, LastError: "response unavailable"}); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []dep.Stage{dep.StageCurrent, dep.StageStaged} {
		if err := store.PutKeypair(ctx, account.Name, stage, &dep.Keypair{CertPEM: []byte("recovery-certificate-" + stage), KeyPEM: []byte("recovery-private-key-" + stage), CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetSession(ctx, account.Name, "recovery-session"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCursor(ctx, account.Name, dep.Cursor{Value: "resumable-cursor", Phase: dep.PhaseSync, Revision: 7, FetchedUntil: &now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutAssignmentState(ctx, account.Name, dep.AssignmentState{NotBefore: now.Add(time.Hour), Failures: 3}); err != nil {
		t.Fatal(err)
	}
	state := readDEPRecovery(t, source, keys)
	if state.account.DesiredProfile("ADE-EXPLICIT") != "ade-explicit" || state.account.DesiredProfile("ADE-DEFAULT") != "ade-default" || state.account.ProfileRevision == 0 || state.assignment.Status != dep.AssignmentPending {
		t.Fatal("ADE recovery fixture has no explicit/default desired choices or resumable mutation")
	}
	return state
}

// readDEPRecovery reads retained ADE state without exposing credential material
// in failure diagnostics.
func readDEPRecovery(t *testing.T, source SQL, keys *crypt.Keyring) depRecoveryState {
	t.Helper()
	ctx := t.Context()
	store, err := depsql.Open(ctx, source.DB, source.Dialect, depsql.Options{SkipMigrate: true, Keyring: keys})
	if err != nil {
		t.Fatal(err)
	}
	var out depRecoveryState
	out.account, err = store.GetAccount(ctx, "ade-recovery")
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.ListProfiles(ctx, "ade-recovery", paging.Page{})
	if err != nil {
		t.Fatal(err)
	}
	out.profiles = profiles.Items
	devices, err := store.ListDevices(ctx, "ade-recovery", dep.DeviceQuery{}, paging.Page{})
	if err != nil {
		t.Fatal(err)
	}
	out.devices = devices.Items
	out.assignment, err = store.GetAssignment(ctx, "ade-recovery", "ADE-EXPLICIT")
	if err != nil {
		t.Fatal(err)
	}
	out.current, err = store.Keypair(ctx, "ade-recovery", dep.StageCurrent)
	if err != nil {
		t.Fatal(err)
	}
	out.staged, err = store.Keypair(ctx, "ade-recovery", dep.StageStaged)
	if err != nil {
		t.Fatal(err)
	}
	out.cursor, err = store.Cursor(ctx, "ade-recovery")
	if err != nil {
		t.Fatal(err)
	}
	out.session, err = store.Session(ctx, "ade-recovery")
	if err != nil {
		t.Fatal(err)
	}
	out.schedule, err = store.AssignmentState(ctx, "ade-recovery")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRecoveryCreateTableOptionalClauseRejectsInvalidNames ensures the optional
// grammar clause does not bypass table-name validation or accept malformed SQL.
func TestRecoveryCreateTableOptionalClauseRejectsInvalidNames(t *testing.T) {
	for _, statement := range []string{
		"CREATE TABLE IF EXISTS unsafe (id INTEGER);",
		"CREATE TABLE IF NOT unsafe (id INTEGER);",
		"CREATE TABLE IF NOT EXISTS;",
		"CREATE TABLE IF NOT EXISTS ../outside (id INTEGER);",
		"CREATE TABLE IF NOT EXISTS UpperCase (id INTEGER);",
	} {
		t.Run(statement, func(t *testing.T) {
			set := sqlcommon.MigrationSet{Table: "test_schema", FS: fstest.MapFS{"0001_init.sql": &fstest.MapFile{Data: []byte("-- +up\n" + statement + "\n-- +down\nDROP TABLE unsafe;\n")}}}
			if _, err := describe(set); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid compiled table accepted: %v", err)
			}
		})
	}
}
