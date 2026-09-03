package deptest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

// Factory returns a fresh, empty store for one test, sealing secrets with
// keyring when it is not nil.
type Factory func(t *testing.T, keyring *crypt.Keyring) dep.Store

// SecretReader is the optional interface a store implements so the suite
// can inspect how secrets rest: the raw bytes of consumer_secret,
// access_token, access_secret, session, and key_pem:<stage>.
type SecretReader interface {
	RawSecrets(ctx context.Context, name string) (map[string][]byte, error)
}

var t0 = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// KeyringName is the active key name the suite seals with.
const KeyringName = "dep-key-v1"

// Keyring builds a keyring with one active key for tests.
func Keyring(t *testing.T) *crypt.Keyring {
	t.Helper()
	k, err := crypt.NewKeyring(context.Background(), crypt.Options{
		Keys:     crypt.Keys{Active: KeyringName},
		Provider: secrets.Static{KeyringName: bytes.Repeat([]byte{0x42}, 32)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// SampleAccount returns a fully populated account.
func SampleAccount(name string) *dep.Account {
	exp := t0.Add(180 * 24 * time.Hour)
	return &dep.Account{
		Name: name, ConsumerKey: "CK_" + name, ConsumerSecret: "CS_secret_" + name, AccessToken: "AT_token_" + name, AccessSecret: "AS_secret_" + name,
		AccessTokenExpiry: &exp, ProtocolVersion: 9, OrgName: "Org " + name, OrgID: "ORG-" + name, ServerName: "srv", ServerUUID: "UUID-" + name, AdminID: "admin@example.com",
		Limits: map[string]dep.Limit{dep.PathFetchDevices: {Default: 100, Maximum: 1000}}, ProfileUUID: "PROFILE-1",
		State: dep.AccountState{TermsExpired: true}, CreatedAt: t0, UpdatedAt: t0.Add(time.Minute),
	}
}

// SampleDevice returns a device with every documented key set.
func SampleDevice(serial string) dep.Device {
	return dep.Device{
		SerialNumber: serial, Model: "MacBook Pro", Description: "MBP 14", Color: "SPACE GRAY", AssetTag: "TAG-" + serial, DeviceFamily: "Mac", OS: "OSX",
		DeviceAssignedBy: "admin@example.com", DeviceAssignedDate: dep.Time(t0), ProfileUUID: "PROFILE-1", ProfileStatus: dep.ProfileStatusAssigned,
		ProfileAssignTime: dep.Time(t0.Add(time.Minute)), ProfilePushTime: dep.Time(t0.Add(2 * time.Minute)), OpType: dep.OpAdded, OpDate: dep.Time(t0.Add(3 * time.Minute)),
		MDMMigrationDeadline: dep.Time(t0.Add(48 * time.Hour)), BluetoothMACAddress: "aa:bb:cc:dd:ee:01", EthernetMACAddress: "aa:bb:cc:dd:ee:02", WifiMACAddress: "aa:bb:cc:dd:ee:03",
		EID: "8900000000000000001", IMEI: []string{"350000000000001", "350000000000002"}, MEID: []string{"A0000000000001"}, IsReplacementDevice: true, ReleasedByReplacement: false,
		Extra: map[string]any{"future_key": "kept", "nested": map[string]any{"n": float64(1)}},
	}
}

// SampleProfile returns a profile with every documented key set.
func SampleProfile(uuid string) *dep.Profile {
	return &dep.Profile{
		ProfileUUID: uuid, ProfileName: "Corporate", URL: "https://mdm.example.com/enroll", OrgMagic: "magic-1", AllowPairing: dep.Bool(false), AnchorCerts: []string{"AAAA"},
		AutoAdvanceSetup: dep.Bool(true), AwaitDeviceConfigured: dep.Bool(true), ConfigurationWebURL: "https://mdm.example.com/webview", Department: "IT", Devices: []string{"S1"},
		DoNotUseProfileFromBackup: dep.Bool(true), IsMandatory: dep.Bool(true), IsMDMRemovable: dep.Bool(false), IsMultiUser: dep.Bool(false), IsReturnToService: dep.Bool(true),
		IsSupervised: dep.Bool(true), Language: "en", Region: "GB", SkipSetupItems: []string{"Siri", "Zoom"}, SupervisingHostCerts: []string{"BBBB"},
		SupportEmailAddress: "help@example.com", SupportPhoneNumber: "+44 20 0000 0000", Extra: map[string]any{"unknown_flag": true},
	}
}

// RunStoreSuite runs every subtest against stores from the factory.
func RunStoreSuite(t *testing.T, newStore Factory) {
	t.Helper()
	t.Run("Accounts", func(t *testing.T) { runAccounts(t, newStore) })
	t.Run("AccountsSealedAtRest", func(t *testing.T) { runSealed(t, newStore) })
	t.Run("Keypairs", func(t *testing.T) { runKeypairs(t, newStore) })
	t.Run("UpstageAtomic", func(t *testing.T) { runUpstage(t, newStore) })
	t.Run("Sessions", func(t *testing.T) { runSessions(t, newStore) })
	t.Run("Cursor", func(t *testing.T) { runCursor(t, newStore) })
	t.Run("DevicesRoundTripAllKeys", func(t *testing.T) { runDevices(t, newStore) })
	t.Run("DeletedTombstones", func(t *testing.T) { runTombstones(t, newStore) })
	t.Run("Profiles", func(t *testing.T) { runProfiles(t, newStore) })
	t.Run("Assignments", func(t *testing.T) { runAssignments(t, newStore) })
	t.Run("Update", func(t *testing.T) { runUpdate(t, newStore) })
	t.Run("InvalidArguments", func(t *testing.T) { runInvalid(t, newStore) })
	t.Run("Concurrency", func(t *testing.T) { runConcurrency(t, newStore) })
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

func must(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func runAccounts(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	_, err := s.GetAccount(ctx, "missing")
	wantErr(t, "get missing", err, dep.ErrNotFound)
	wantErr(t, "delete missing", s.DeleteAccount(ctx, "missing"), dep.ErrNotFound)
	wantErr(t, "state missing", s.SetAccountState(ctx, "missing", dep.AccountState{}), dep.ErrNotFound)

	a := SampleAccount("a")
	must(t, "put", s.PutAccount(ctx, a))
	got, err := s.GetAccount(ctx, "a")
	must(t, "get", err)
	if !reflect.DeepEqual(got, a) {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, a)
	}
	if got.Protocol() != 9 || got.Limit(dep.PathFetchDevices, 7) != 1000 || got.Limit("/x", 7) != 7 {
		t.Fatalf("helpers: %d %d", got.Protocol(), got.Limit(dep.PathFetchDevices, 7))
	}
	// Update keeps CreatedAt and takes the new UpdatedAt and fields.
	b := SampleAccount("a")
	b.CreatedAt, b.UpdatedAt, b.OrgName, b.AccessTokenExpiry, b.Limits = t0.Add(time.Hour), t0.Add(2*time.Hour), "renamed", nil, nil
	must(t, "put again", s.PutAccount(ctx, b))
	got, err = s.GetAccount(ctx, "a")
	must(t, "get again", err)
	if !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(t0.Add(2*time.Hour)) || got.OrgName != "renamed" || got.AccessTokenExpiry != nil || len(got.Limits) != 0 {
		t.Fatalf("update: %+v", got)
	}
	must(t, "state", s.SetAccountState(ctx, "a", dep.AccountState{TokenInvalid: true}))
	got, _ = s.GetAccount(ctx, "a")
	if got.State != (dep.AccountState{TokenInvalid: true}) {
		t.Fatalf("state: %+v", got.State)
	}
	must(t, "put b", s.PutAccount(ctx, SampleAccount("b")))
	must(t, "put c", s.PutAccount(ctx, SampleAccount("c")))
	r, err := s.ListAccounts(ctx, storage.Page{Limit: 2})
	must(t, "list", err)
	if len(r.Items) != 2 || r.Items[0].Name != "a" || r.Items[1].Name != "b" || r.NextCursor != "b" {
		t.Fatalf("list page 1: %+v", r)
	}
	r, err = s.ListAccounts(ctx, storage.Page{Limit: 2, Cursor: r.NextCursor})
	must(t, "list 2", err)
	if len(r.Items) != 1 || r.Items[0].Name != "c" || r.NextCursor != "" {
		t.Fatalf("list page 2: %+v", r)
	}
	// Delete cascades to everything keyed by the account.
	must(t, "session", s.SetSession(ctx, "a", "tok"))
	must(t, "cursor", s.SetCursor(ctx, "a", dep.Cursor{Value: "c", Phase: dep.PhaseSync, UpdatedAt: t0}))
	must(t, "devices", s.PutDevices(ctx, "a", []dep.Device{SampleDevice("S1")}, t0))
	must(t, "profile", s.PutProfile(ctx, "a", SampleProfile("P1")))
	must(t, "assignment", s.PutAssignment(ctx, &dep.Assignment{Account: "a", SerialNumber: "S1", Status: dep.StatusSuccess}))
	must(t, "keypair", s.PutKeypair(ctx, "a", dep.StageCurrent, &dep.Keypair{CertPEM: []byte("c"), KeyPEM: []byte("k")}))
	must(t, "delete", s.DeleteAccount(ctx, "a"))
	if _, err := s.GetAccount(ctx, "a"); !errors.Is(err, dep.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if tok, _ := s.Session(ctx, "a"); tok != "" {
		t.Fatal("session survived delete")
	}
	if c, _ := s.Cursor(ctx, "a"); !c.IsZero() {
		t.Fatal("cursor survived delete")
	}
	if _, err := s.GetDevice(ctx, "a", "S1"); !errors.Is(err, dep.ErrNotFound) {
		t.Fatal("device survived delete")
	}
	if _, err := s.GetProfile(ctx, "a", "P1"); !errors.Is(err, dep.ErrNotFound) {
		t.Fatal("profile survived delete")
	}
	if _, err := s.GetAssignment(ctx, "a", "S1"); !errors.Is(err, dep.ErrNotFound) {
		t.Fatal("assignment survived delete")
	}
	if _, err := s.Keypair(ctx, "a", dep.StageCurrent); !errors.Is(err, dep.ErrNotFound) {
		t.Fatal("keypair survived delete")
	}
	if r, _ := s.ListAccounts(ctx, storage.Page{}); len(r.Items) != 2 {
		t.Fatalf("accounts after delete: %d", len(r.Items))
	}
}

func runSealed(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, Keyring(t))
	a := SampleAccount("sealed")
	must(t, "put", s.PutAccount(ctx, a))
	must(t, "session", s.SetSession(ctx, "sealed", "SESSION-secret"))
	must(t, "keypair", s.PutKeypair(ctx, "sealed", dep.StageStaged, &dep.Keypair{CertPEM: []byte("CERT"), KeyPEM: []byte("PRIVATE KEY MATERIAL"), CreatedAt: t0}))
	got, err := s.GetAccount(ctx, "sealed")
	must(t, "get", err)
	if got.ConsumerSecret != a.ConsumerSecret || got.AccessToken != a.AccessToken || got.AccessSecret != a.AccessSecret {
		t.Fatalf("secrets: %+v", got)
	}
	if tok, err := s.Session(ctx, "sealed"); err != nil || tok != "SESSION-secret" {
		t.Fatalf("session: %q %v", tok, err)
	}
	kp, err := s.Keypair(ctx, "sealed", dep.StageStaged)
	if err != nil || string(kp.KeyPEM) != "PRIVATE KEY MATERIAL" || string(kp.CertPEM) != "CERT" {
		t.Fatalf("keypair: %+v %v", kp, err)
	}
	reader, ok := s.(SecretReader)
	if !ok {
		t.Skip("store does not expose raw secrets")
	}
	raw, err := reader.RawSecrets(ctx, "sealed")
	must(t, "raw", err)
	for _, key := range []string{"consumer_secret", "access_token", "access_secret", "session", "key_pem:staged"} {
		b, ok := raw[key]
		if !ok {
			t.Fatalf("%s not stored", key)
		}
		if !crypt.IsSealed(b) {
			t.Errorf("%s is not sealed", key)
		}
		if name, ok := crypt.KeyName(b); !ok || name != KeyringName {
			t.Errorf("%s sealed under %q", key, name)
		}
		for _, plain := range []string{a.ConsumerSecret, a.AccessToken, a.AccessSecret, "SESSION-secret", "PRIVATE KEY MATERIAL"} {
			if bytes.Contains(b, []byte(plain)) {
				t.Errorf("%s holds plaintext %q", key, plain)
			}
		}
	}
	// Upstaging re-seals under the new row identity and still opens.
	must(t, "upstage", s.UpstageKeypair(ctx, "sealed"))
	kp, err = s.Keypair(ctx, "sealed", dep.StageCurrent)
	if err != nil || string(kp.KeyPEM) != "PRIVATE KEY MATERIAL" {
		t.Fatalf("current after upstage: %+v %v", kp, err)
	}
	raw, _ = reader.RawSecrets(ctx, "sealed")
	if b, ok := raw["key_pem:current"]; !ok || !crypt.IsSealed(b) {
		t.Fatal("current key not sealed")
	}
	// A store without a keyring reads a plaintext row it wrote itself.
	plain := newStore(t, nil)
	must(t, "plain put", plain.PutAccount(ctx, a))
	if got, err := plain.GetAccount(ctx, "sealed"); err != nil || got.AccessSecret != a.AccessSecret {
		t.Fatalf("plain round trip: %+v %v", got, err)
	}
	if _, err := plain.(SecretReader).RawSecrets(ctx, ""); !errors.Is(err, dep.ErrInvalid) {
		t.Fatalf("raw empty name: %v", err)
	}
}

func runKeypairs(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	_, err := s.Keypair(ctx, "k", dep.StageStaged)
	wantErr(t, "missing staged", err, dep.ErrNotFound)
	wantErr(t, "upstage nothing", s.UpstageKeypair(ctx, "k"), dep.ErrNotFound)
	wantErr(t, "bad stage", s.PutKeypair(ctx, "k", "later", &dep.Keypair{CertPEM: []byte("c"), KeyPEM: []byte("k")}), dep.ErrInvalid)
	_, err = s.Keypair(ctx, "k", "later")
	wantErr(t, "bad stage get", err, dep.ErrInvalid)
	wantErr(t, "empty keypair", s.PutKeypair(ctx, "k", dep.StageStaged, &dep.Keypair{}), dep.ErrInvalid)
	wantErr(t, "nil keypair", s.PutKeypair(ctx, "k", dep.StageStaged, nil), dep.ErrInvalid)
	// A keypair precedes its account.
	kp := &dep.Keypair{CertPEM: []byte("CERT1"), KeyPEM: []byte("KEY1"), CreatedAt: t0}
	must(t, "put staged", s.PutKeypair(ctx, "k", dep.StageStaged, kp))
	got, err := s.Keypair(ctx, "k", dep.StageStaged)
	must(t, "get staged", err)
	if !reflect.DeepEqual(got, kp) {
		t.Fatalf("staged: %+v", got)
	}
	// Replacing the staged pair is allowed.
	kp2 := &dep.Keypair{CertPEM: []byte("CERT2"), KeyPEM: []byte("KEY2"), CreatedAt: t0.Add(time.Hour)}
	must(t, "replace staged", s.PutKeypair(ctx, "k", dep.StageStaged, kp2))
	got, _ = s.Keypair(ctx, "k", dep.StageStaged)
	if string(got.KeyPEM) != "KEY2" {
		t.Fatalf("replaced: %s", got.KeyPEM)
	}
	// The returned slices are copies.
	got.KeyPEM[0] = 'X'
	if again, _ := s.Keypair(ctx, "k", dep.StageStaged); string(again.KeyPEM) != "KEY2" {
		t.Fatal("stored key mutated through the result")
	}
}

func runUpstage(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	current := &dep.Keypair{CertPEM: []byte("CUR"), KeyPEM: []byte("CURKEY"), CreatedAt: t0}
	staged := &dep.Keypair{CertPEM: []byte("STG"), KeyPEM: []byte("STGKEY"), CreatedAt: t0.Add(time.Hour)}
	must(t, "put current", s.PutKeypair(ctx, "u", dep.StageCurrent, current))
	must(t, "put staged", s.PutKeypair(ctx, "u", dep.StageStaged, staged))
	// A failing transaction after the upstage leaves both slots as they were.
	boom := errors.New("boom")
	err := s.Update(ctx, func(tx dep.Tx) error {
		if err := tx.UpstageKeypair(ctx, "u"); err != nil {
			return err
		}
		if _, err := tx.Keypair(ctx, "u", dep.StageStaged); !errors.Is(err, dep.ErrNotFound) {
			return fmt.Errorf("staged still present inside tx: %w", err)
		}
		return boom
	})
	wantErr(t, "rolled back", err, boom)
	got, err := s.Keypair(ctx, "u", dep.StageCurrent)
	if err != nil || string(got.KeyPEM) != "CURKEY" {
		t.Fatalf("current after rollback: %+v %v", got, err)
	}
	if got, err := s.Keypair(ctx, "u", dep.StageStaged); err != nil || string(got.KeyPEM) != "STGKEY" {
		t.Fatalf("staged after rollback: %+v %v", got, err)
	}
	// The real upstage swaps and clears.
	must(t, "upstage", s.UpstageKeypair(ctx, "u"))
	got, err = s.Keypair(ctx, "u", dep.StageCurrent)
	if err != nil || !reflect.DeepEqual(got, staged) {
		t.Fatalf("current after upstage: %+v %v", got, err)
	}
	_, err = s.Keypair(ctx, "u", dep.StageStaged)
	wantErr(t, "staged cleared", err, dep.ErrNotFound)
	// Upstaging again with nothing staged fails and keeps current.
	wantErr(t, "second upstage", s.UpstageKeypair(ctx, "u"), dep.ErrNotFound)
	if got, _ := s.Keypair(ctx, "u", dep.StageCurrent); string(got.KeyPEM) != "STGKEY" {
		t.Fatal("current changed by a failed upstage")
	}
}

func runSessions(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	tok, err := s.Session(ctx, "s")
	if err != nil || tok != "" {
		t.Fatalf("empty: %q %v", tok, err)
	}
	must(t, "set", s.SetSession(ctx, "s", "one"))
	must(t, "set other", s.SetSession(ctx, "other", "two"))
	if tok, _ := s.Session(ctx, "s"); tok != "one" {
		t.Fatalf("session: %q", tok)
	}
	must(t, "rotate", s.SetSession(ctx, "s", "three"))
	if tok, _ := s.Session(ctx, "s"); tok != "three" {
		t.Fatalf("rotated: %q", tok)
	}
	must(t, "clear", s.SetSession(ctx, "s", ""))
	if tok, _ := s.Session(ctx, "s"); tok != "" {
		t.Fatalf("cleared: %q", tok)
	}
	if tok, _ := s.Session(ctx, "other"); tok != "two" {
		t.Fatalf("other: %q", tok)
	}
}

func runCursor(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	c, err := s.Cursor(ctx, "c")
	if err != nil || !c.IsZero() {
		t.Fatalf("empty: %+v %v", c, err)
	}
	local := time.FixedZone("plus2", 2*3600)
	want := dep.Cursor{Value: "abc", Phase: dep.PhaseFetch, FetchedUntil: dep.Time(t0.In(local)), UpdatedAt: t0.Add(time.Minute).In(local)}
	must(t, "set", s.SetCursor(ctx, "c", want))
	c, err = s.Cursor(ctx, "c")
	must(t, "get", err)
	if c.Value != "abc" || c.Phase != dep.PhaseFetch || !c.UpdatedAt.Equal(want.UpdatedAt) || c.FetchedUntil == nil || !c.FetchedUntil.Equal(t0) {
		t.Fatalf("cursor: %+v", c)
	}
	if c.UpdatedAt.Location() != time.UTC || c.FetchedUntil.Location() != time.UTC {
		t.Fatalf("cursor not UTC: %v %v", c.UpdatedAt.Location(), c.FetchedUntil.Location())
	}
	must(t, "replace", s.SetCursor(ctx, "c", dep.Cursor{Value: "def", Phase: dep.PhaseSync, UpdatedAt: t0.Add(time.Hour)}))
	c, _ = s.Cursor(ctx, "c")
	if c.Value != "def" || c.Phase != dep.PhaseSync || c.FetchedUntil != nil || !c.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("replaced: %+v", c)
	}
	must(t, "clear", s.SetCursor(ctx, "c", dep.Cursor{}))
	if c, _ := s.Cursor(ctx, "c"); !c.IsZero() {
		t.Fatalf("cleared: %+v", c)
	}
}

func runDevices(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	_, err := s.GetDevice(ctx, "d", "S1")
	wantErr(t, "missing", err, dep.ErrNotFound)
	d1, d2 := SampleDevice("S1"), SampleDevice("S2")
	d2.ProfileUUID, d2.ProfileStatus, d2.OpType, d2.IMEI, d2.Extra = "", dep.ProfileStatusEmpty, "", nil, nil
	must(t, "put", s.PutDevices(ctx, "d", []dep.Device{d1, d2}, t0))
	got, err := s.GetDevice(ctx, "d", "S1")
	must(t, "get", err)
	if got.Account != "d" || got.Deleted || !got.FirstSeen.Equal(t0) || !got.UpdatedAt.Equal(t0) {
		t.Fatalf("record: %+v", got)
	}
	if !reflect.DeepEqual(got.Device, d1) {
		t.Fatalf("device round trip\n got %+v\nwant %+v", got.Device, d1)
	}
	// Every timestamp comes back in UTC.
	for name, ts := range map[string]*time.Time{"assigned": got.DeviceAssignedDate, "op": got.OpDate, "deadline": got.MDMMigrationDeadline} {
		if ts.Location() != time.UTC {
			t.Errorf("%s not UTC", name)
		}
	}
	// A second put keeps FirstSeen, bumps UpdatedAt, and replaces fields.
	d1b := d1.Clone()
	d1b.ProfileStatus, d1b.OpType, d1b.Extra = dep.ProfileStatusPushed, dep.OpModified, nil
	must(t, "put again", s.PutDevices(ctx, "d", []dep.Device{d1b}, t0.Add(time.Hour)))
	got, _ = s.GetDevice(ctx, "d", "S1")
	if got.ProfileStatus != dep.ProfileStatusPushed || got.Extra != nil || !got.FirstSeen.Equal(t0) || !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("updated: %+v", got)
	}
	// The result is a copy.
	got.IMEI[0] = "changed"
	if again, _ := s.GetDevice(ctx, "d", "S1"); again.IMEI[0] != "350000000000001" {
		t.Fatal("stored device mutated through the result")
	}
	// Listing pages by serial and filters by profile.
	must(t, "put more", s.PutDevices(ctx, "d", []dep.Device{SampleDevice("S3"), {SerialNumber: "S0"}}, t0))
	must(t, "put other account", s.PutDevices(ctx, "other", []dep.Device{SampleDevice("S1")}, t0))
	r, err := s.ListDevices(ctx, "d", dep.DeviceQuery{}, storage.Page{Limit: 3})
	must(t, "list", err)
	if len(r.Items) != 3 || r.Items[0].SerialNumber != "S0" || r.Items[2].SerialNumber != "S2" || r.NextCursor != "S2" {
		t.Fatalf("page 1: %d %q", len(r.Items), r.NextCursor)
	}
	r, err = s.ListDevices(ctx, "d", dep.DeviceQuery{}, storage.Page{Limit: 3, Cursor: r.NextCursor})
	must(t, "list 2", err)
	if len(r.Items) != 1 || r.Items[0].SerialNumber != "S3" || r.NextCursor != "" {
		t.Fatalf("page 2: %+v", r)
	}
	r, err = s.ListDevices(ctx, "d", dep.DeviceQuery{ProfileUUID: "PROFILE-1"}, storage.Page{})
	must(t, "list by profile", err)
	if len(r.Items) != 2 {
		t.Fatalf("by profile: %d", len(r.Items))
	}
	if r, _ := s.ListDevices(ctx, "nobody", dep.DeviceQuery{}, storage.Page{}); len(r.Items) != 0 {
		t.Fatal("unknown account listed devices")
	}
	// An empty batch is fine; a device without a serial is not.
	must(t, "empty", s.PutDevices(ctx, "d", nil, t0))
	wantErr(t, "no serial", s.PutDevices(ctx, "d", []dep.Device{{Model: "x"}}, t0), dep.ErrInvalid)
}

func runTombstones(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	d := SampleDevice("T1")
	must(t, "add", s.PutDevices(ctx, "t", []dep.Device{d}, t0))
	del := d.Clone()
	del.OpType, del.OpDate, del.ReleasedByReplacement = dep.OpDeleted, dep.Time(t0.Add(time.Hour)), true
	must(t, "delete", s.PutDevices(ctx, "t", []dep.Device{del}, t0.Add(time.Hour)))
	got, err := s.GetDevice(ctx, "t", "T1")
	must(t, "get tombstone", err)
	if !got.Deleted || got.OpType != dep.OpDeleted || !got.ReleasedByReplacement || got.Model != d.Model || !got.FirstSeen.Equal(t0) {
		t.Fatalf("tombstone: %+v", got)
	}
	if r, _ := s.ListDevices(ctx, "t", dep.DeviceQuery{}, storage.Page{}); len(r.Items) != 0 {
		t.Fatal("tombstone listed as live")
	}
	r, err := s.ListDevices(ctx, "t", dep.DeviceQuery{IncludeDeleted: true}, storage.Page{})
	must(t, "list deleted", err)
	if len(r.Items) != 1 || !r.Items[0].Deleted {
		t.Fatalf("include deleted: %+v", r.Items)
	}
	// Seen again (re-added, or a fetch record) clears the tombstone.
	back := d.Clone()
	back.OpType = ""
	must(t, "re-add", s.PutDevices(ctx, "t", []dep.Device{back}, t0.Add(2*time.Hour)))
	got, _ = s.GetDevice(ctx, "t", "T1")
	if got.Deleted || got.OpType != "" || !got.FirstSeen.Equal(t0) || !got.UpdatedAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("revived: %+v", got)
	}
	if r, _ := s.ListDevices(ctx, "t", dep.DeviceQuery{}, storage.Page{}); len(r.Items) != 1 {
		t.Fatal("revived device not listed")
	}
}

func runProfiles(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	_, err := s.GetProfile(ctx, "p", "P1")
	wantErr(t, "missing", err, dep.ErrNotFound)
	wantErr(t, "delete missing", s.DeleteProfile(ctx, "p", "P1"), dep.ErrNotFound)
	wantErr(t, "no uuid", s.PutProfile(ctx, "p", &dep.Profile{ProfileName: "x"}), dep.ErrInvalid)
	wantErr(t, "nil", s.PutProfile(ctx, "p", nil), dep.ErrInvalid)
	p := SampleProfile("P1")
	must(t, "put", s.PutProfile(ctx, "p", p))
	got, err := s.GetProfile(ctx, "p", "P1")
	must(t, "get", err)
	if !reflect.DeepEqual(got, p) {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, p)
	}
	*got.IsSupervised = false
	if again, _ := s.GetProfile(ctx, "p", "P1"); !*again.IsSupervised {
		t.Fatal("stored profile mutated through the result")
	}
	p2 := SampleProfile("P1")
	p2.ProfileName, p2.Extra, p2.IsMultiUser = "Renamed", nil, nil
	must(t, "replace", s.PutProfile(ctx, "p", p2))
	got, _ = s.GetProfile(ctx, "p", "P1")
	if got.ProfileName != "Renamed" || got.Extra != nil || got.IsMultiUser != nil {
		t.Fatalf("replaced: %+v", got)
	}
	must(t, "put P0", s.PutProfile(ctx, "p", SampleProfile("P0")))
	must(t, "put P2", s.PutProfile(ctx, "p", SampleProfile("P2")))
	must(t, "put other", s.PutProfile(ctx, "other", SampleProfile("P1")))
	r, err := s.ListProfiles(ctx, "p", storage.Page{Limit: 2})
	must(t, "list", err)
	if len(r.Items) != 2 || r.Items[0].ProfileUUID != "P0" || r.Items[1].ProfileUUID != "P1" || r.NextCursor != "P1" {
		t.Fatalf("page 1: %+v", r)
	}
	r, err = s.ListProfiles(ctx, "p", storage.Page{Limit: 2, Cursor: "P1"})
	must(t, "list 2", err)
	if len(r.Items) != 1 || r.Items[0].ProfileUUID != "P2" || r.NextCursor != "" {
		t.Fatalf("page 2: %+v", r)
	}
	must(t, "delete", s.DeleteProfile(ctx, "p", "P1"))
	if _, err := s.GetProfile(ctx, "p", "P1"); !errors.Is(err, dep.ErrNotFound) {
		t.Fatal("profile survived delete")
	}
	if _, err := s.GetProfile(ctx, "other", "P1"); err != nil {
		t.Fatal("delete crossed accounts")
	}
}

func runAssignments(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	_, err := s.GetAssignment(ctx, "a", "S1")
	wantErr(t, "missing", err, dep.ErrNotFound)
	wantErr(t, "nil", s.PutAssignment(ctx, nil), dep.ErrInvalid)
	wantErr(t, "no serial", s.PutAssignment(ctx, &dep.Assignment{Account: "a"}), dep.ErrInvalid)
	local := time.FixedZone("minus5", -5*3600)
	a := &dep.Assignment{Account: "a", SerialNumber: "S1", ProfileUUID: "P1", Status: dep.StatusNotAccessible, Attempts: 2, LastError: "NOT_ACCESSIBLE", AttemptedAt: t0.In(local), NextAttemptAt: t0.Add(time.Hour).In(local)}
	must(t, "put", s.PutAssignment(ctx, a))
	got, err := s.GetAssignment(ctx, "a", "S1")
	must(t, "get", err)
	if got.Status != a.Status || got.Attempts != 2 || got.LastError != a.LastError || got.ProfileUUID != "P1" || !got.AttemptedAt.Equal(t0) || !got.NextAttemptAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("round trip: %+v", got)
	}
	if got.AttemptedAt.Location() != time.UTC || got.NextAttemptAt.Location() != time.UTC {
		t.Fatal("assignment times not UTC")
	}
	// Success clears the next attempt.
	must(t, "success", s.PutAssignment(ctx, &dep.Assignment{Account: "a", SerialNumber: "S1", ProfileUUID: "P1", Status: dep.StatusSuccess, AttemptedAt: t0.Add(2 * time.Hour)}))
	got, _ = s.GetAssignment(ctx, "a", "S1")
	if got.Status != dep.StatusSuccess || !got.NextAttemptAt.IsZero() || got.Attempts != 0 || got.LastError != "" {
		t.Fatalf("success: %+v", got)
	}
	must(t, "put S2", s.PutAssignment(ctx, &dep.Assignment{Account: "a", SerialNumber: "S2", Status: dep.StatusFailed, NextAttemptAt: t0}))
	must(t, "put S3", s.PutAssignment(ctx, &dep.Assignment{Account: "a", SerialNumber: "S3", Status: dep.StatusThrottled, NextAttemptAt: t0}))
	must(t, "put other", s.PutAssignment(ctx, &dep.Assignment{Account: "other", SerialNumber: "S1", Status: dep.StatusFailed}))
	r, err := s.ListAssignments(ctx, "a", dep.AssignmentQuery{}, storage.Page{Limit: 2})
	must(t, "list", err)
	if len(r.Items) != 2 || r.Items[0].SerialNumber != "S1" || r.NextCursor != "S2" {
		t.Fatalf("page 1: %+v", r)
	}
	r, err = s.ListAssignments(ctx, "a", dep.AssignmentQuery{}, storage.Page{Limit: 2, Cursor: "S2"})
	must(t, "list 2", err)
	if len(r.Items) != 1 || r.Items[0].SerialNumber != "S3" {
		t.Fatalf("page 2: %+v", r)
	}
	r, err = s.ListAssignments(ctx, "a", dep.AssignmentQuery{Status: dep.StatusFailed}, storage.Page{})
	must(t, "list failed", err)
	if len(r.Items) != 1 || r.Items[0].SerialNumber != "S2" {
		t.Fatalf("failed: %+v", r.Items)
	}
}

func runUpdate(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	wantErr(t, "nil fn", s.Update(ctx, nil), dep.ErrInvalid)
	// Nested updates are refused.
	err := s.Update(ctx, func(tx dep.Tx) error {
		inner, ok := tx.(dep.Store)
		if !ok {
			return nil
		}
		return inner.Update(ctx, func(dep.Tx) error { return nil })
	})
	wantErr(t, "nested", err, dep.ErrInvalid)
	// A page commit: devices and cursor together, or neither.
	boom := errors.New("boom")
	err = s.Update(ctx, func(tx dep.Tx) error {
		if err := tx.PutDevices(ctx, "u", []dep.Device{SampleDevice("S1")}, t0); err != nil {
			return err
		}
		if err := tx.SetCursor(ctx, "u", dep.Cursor{Value: "c1", Phase: dep.PhaseSync, UpdatedAt: t0}); err != nil {
			return err
		}
		if _, err := tx.GetDevice(ctx, "u", "S1"); err != nil {
			return fmt.Errorf("device invisible inside tx: %w", err)
		}
		return boom
	})
	wantErr(t, "rollback", err, boom)
	if _, err := s.GetDevice(ctx, "u", "S1"); !errors.Is(err, dep.ErrNotFound) {
		t.Fatalf("device survived rollback: %v", err)
	}
	if c, _ := s.Cursor(ctx, "u"); !c.IsZero() {
		t.Fatal("cursor survived rollback")
	}
	err = s.Update(ctx, func(tx dep.Tx) error {
		if err := tx.PutAccount(ctx, SampleAccount("u")); err != nil {
			return err
		}
		if err := tx.PutDevices(ctx, "u", []dep.Device{SampleDevice("S1")}, t0); err != nil {
			return err
		}
		if err := tx.SetSession(ctx, "u", "tok"); err != nil {
			return err
		}
		if err := tx.PutProfile(ctx, "u", SampleProfile("P1")); err != nil {
			return err
		}
		if err := tx.PutAssignment(ctx, &dep.Assignment{Account: "u", SerialNumber: "S1", Status: dep.StatusSuccess}); err != nil {
			return err
		}
		if err := tx.SetAccountState(ctx, "u", dep.AccountState{}); err != nil {
			return err
		}
		if _, err := tx.ListAccounts(ctx, storage.Page{}); err != nil {
			return err
		}
		if _, err := tx.ListDevices(ctx, "u", dep.DeviceQuery{}, storage.Page{}); err != nil {
			return err
		}
		if _, err := tx.ListProfiles(ctx, "u", storage.Page{}); err != nil {
			return err
		}
		if _, err := tx.ListAssignments(ctx, "u", dep.AssignmentQuery{}, storage.Page{}); err != nil {
			return err
		}
		if _, err := tx.GetProfile(ctx, "u", "P1"); err != nil {
			return err
		}
		if _, err := tx.GetAssignment(ctx, "u", "S1"); err != nil {
			return err
		}
		if _, err := tx.Session(ctx, "u"); err != nil {
			return err
		}
		if _, err := tx.Cursor(ctx, "u"); err != nil {
			return err
		}
		if err := tx.DeleteProfile(ctx, "u", "P1"); err != nil {
			return err
		}
		return tx.SetCursor(ctx, "u", dep.Cursor{Value: "c1", Phase: dep.PhaseSync, UpdatedAt: t0})
	})
	must(t, "commit", err)
	if _, err := s.GetDevice(ctx, "u", "S1"); err != nil {
		t.Fatalf("device after commit: %v", err)
	}
	if c, _ := s.Cursor(ctx, "u"); c.Value != "c1" {
		t.Fatalf("cursor after commit: %+v", c)
	}
	if a, err := s.GetAccount(ctx, "u"); err != nil || a.State != (dep.AccountState{}) {
		t.Fatalf("account after commit: %+v %v", a, err)
	}
	must(t, "delete in tx", s.Update(ctx, func(tx dep.Tx) error { return tx.DeleteAccount(ctx, "u") }))
	if _, err := s.GetAccount(ctx, "u"); !errors.Is(err, dep.ErrNotFound) {
		t.Fatal("account survived delete in tx")
	}
}

func runInvalid(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	checks := map[string]func() error{
		"PutAccount nil":   func() error { return s.PutAccount(ctx, nil) },
		"PutAccount empty": func() error { return s.PutAccount(ctx, &dep.Account{}) },
		"GetAccount":       func() error { _, err := s.GetAccount(ctx, ""); return err },
		"DeleteAccount":    func() error { return s.DeleteAccount(ctx, "") },
		"SetAccountState":  func() error { return s.SetAccountState(ctx, "", dep.AccountState{}) },
		"PutKeypair": func() error {
			return s.PutKeypair(ctx, "", dep.StageStaged, &dep.Keypair{CertPEM: []byte("c"), KeyPEM: []byte("k")})
		},
		"Keypair":              func() error { _, err := s.Keypair(ctx, "", dep.StageStaged); return err },
		"UpstageKeypair":       func() error { return s.UpstageKeypair(ctx, "") },
		"Session":              func() error { _, err := s.Session(ctx, ""); return err },
		"SetSession":           func() error { return s.SetSession(ctx, "", "x") },
		"Cursor":               func() error { _, err := s.Cursor(ctx, ""); return err },
		"SetCursor":            func() error { return s.SetCursor(ctx, "", dep.Cursor{Value: "x"}) },
		"PutDevices":           func() error { return s.PutDevices(ctx, "", nil, t0) },
		"GetDevice account":    func() error { _, err := s.GetDevice(ctx, "", "S"); return err },
		"GetDevice serial":     func() error { _, err := s.GetDevice(ctx, "a", ""); return err },
		"ListDevices":          func() error { _, err := s.ListDevices(ctx, "", dep.DeviceQuery{}, storage.Page{}); return err },
		"PutProfile":           func() error { return s.PutProfile(ctx, "", SampleProfile("P")) },
		"GetProfile account":   func() error { _, err := s.GetProfile(ctx, "", "P"); return err },
		"GetProfile uuid":      func() error { _, err := s.GetProfile(ctx, "a", ""); return err },
		"DeleteProfile":        func() error { return s.DeleteProfile(ctx, "", "P") },
		"DeleteProfile uuid":   func() error { return s.DeleteProfile(ctx, "a", "") },
		"ListProfiles":         func() error { _, err := s.ListProfiles(ctx, "", storage.Page{}); return err },
		"PutAssignment":        func() error { return s.PutAssignment(ctx, &dep.Assignment{SerialNumber: "S"}) },
		"GetAssignment":        func() error { _, err := s.GetAssignment(ctx, "", "S"); return err },
		"GetAssignment serial": func() error { _, err := s.GetAssignment(ctx, "a", ""); return err },
		"ListAssignments":      func() error { _, err := s.ListAssignments(ctx, "", dep.AssignmentQuery{}, storage.Page{}); return err },
	}
	for name, fn := range checks {
		if err := fn(); !errors.Is(err, dep.ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func runConcurrency(t *testing.T, newStore Factory) {
	ctx := context.Background()
	s := newStore(t, nil)
	must(t, "account", s.PutAccount(ctx, SampleAccount("c")))
	const n = 16
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			serial := fmt.Sprintf("S%02d", i)
			if err := s.PutDevices(ctx, "c", []dep.Device{SampleDevice(serial)}, t0); err != nil {
				t.Errorf("put %s: %v", serial, err)
			}
			err := s.Update(ctx, func(tx dep.Tx) error {
				if err := tx.PutAssignment(ctx, &dep.Assignment{Account: "c", SerialNumber: serial, Status: dep.StatusSuccess}); err != nil {
					return err
				}
				return tx.SetCursor(ctx, "c", dep.Cursor{Value: serial, Phase: dep.PhaseSync, UpdatedAt: t0})
			})
			if err != nil {
				t.Errorf("update %s: %v", serial, err)
			}
			if err := s.SetSession(ctx, "c", serial); err != nil {
				t.Errorf("session %s: %v", serial, err)
			}
			if _, err := s.GetAccount(ctx, "c"); err != nil {
				t.Errorf("get %s: %v", serial, err)
			}
		})
	}
	wg.Wait()
	r, err := s.ListDevices(ctx, "c", dep.DeviceQuery{}, storage.Page{})
	if err != nil || len(r.Items) != n {
		t.Fatalf("devices: %d %v", len(r.Items), err)
	}
	a, err := s.ListAssignments(ctx, "c", dep.AssignmentQuery{}, storage.Page{})
	if err != nil || len(a.Items) != n {
		t.Fatalf("assignments: %d %v", len(a.Items), err)
	}
	if c, _ := s.Cursor(ctx, "c"); c.Value == "" {
		t.Fatal("no cursor")
	}
}
