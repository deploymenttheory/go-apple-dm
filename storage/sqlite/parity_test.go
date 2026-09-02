package sqlite_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
	"github.com/deploymenttheory/go-apple-mdm/secrets"
	"github.com/deploymenttheory/go-apple-mdm/storage"
	"github.com/deploymenttheory/go-apple-mdm/storage/crypt"
	"github.com/deploymenttheory/go-apple-mdm/storage/inmem"
	"github.com/deploymenttheory/go-apple-mdm/storage/sqlite"
	"github.com/deploymenttheory/go-apple-mdm/storage/storagetest"
)

var keyProvider = secrets.Static{
	"storage-key-v1": []byte("first-key-material-32-bytes-long!"),
	"storage-key-v2": []byte("second-key-material-32-bytes-long"),
}

func keyring(t *testing.T, active string, accepted ...string) *crypt.Keyring {
	t.Helper()
	k, err := crypt.NewKeyring(context.Background(), crypt.Options{Keys: crypt.Keys{Active: active, Accepted: accepted}, Provider: keyProvider})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func openWith(t *testing.T, path string, k *crypt.Keyring) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), path, sqlite.Options{Keyring: k})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestContractEncrypted runs the whole contract with the secret columns
// sealed, proving sealing is invisible to callers.
func TestContractEncrypted(t *testing.T) {
	t.Parallel()
	storagetest.RunAll(t, func(t *testing.T) storage.Store {
		return openWith(t, filepath.Join(t.TempDir(), "enc.db"), keyring(t, "storage-key-v1"))
	})
}

func seedSecrets(t *testing.T, s *sqlite.Store, id mdm.EnrollmentID) {
	t.Helper()
	ctx := context.Background()
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, &checkin.TokenUpdate{UnlockToken: []byte("unlock-secret")}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreBootstrapToken(ctx, id, []byte("bootstrap-secret"), t0); err != nil {
		t.Fatal(err)
	}
	uid := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: id.ID + ":u", ParentID: id.ID}
	if err := s.StoreUserAuthChallenge(ctx, uid, "nonce", nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreUserAuthToken(ctx, uid, "auth-token-secret", nil, t0); err != nil {
		t.Fatal(err)
	}
	ca, err := testpki.NewCA("parity")
	if err != nil {
		t.Fatal(err)
	}
	pushID, err := ca.IssuePush("com.apple.mgmt.parity", t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, _ := pushID.PEM()
	if _, err := s.StorePushCert(ctx, "", certPEM, keyPEM, t0); err != nil {
		t.Fatal(err)
	}
}

func rawColumns(t *testing.T, s *sqlite.Store, id string) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	out := map[string][]byte{}
	var unlock, bootstrap, token, key []byte
	if err := s.DB().QueryRowContext(ctx, "SELECT unlock_token, bootstrap_token FROM enrollments WHERE id = ?", id).Scan(&unlock, &bootstrap); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT auth_token FROM user_auth WHERE enrollment_id = ?", id+":u").Scan(&token); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT key_pem FROM push_certs WHERE topic = ?", "com.apple.mgmt.parity").Scan(&key); err != nil {
		t.Fatal(err)
	}
	out["unlock_token"], out["bootstrap_token"], out["auth_token"], out["key_pem"] = unlock, bootstrap, token, key
	return out
}

// TestRawColumnIsNotPlaintext proves every sealed column is unreadable in
// the database while the API still returns the secrets.
func TestRawColumnIsNotPlaintext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openWith(t, filepath.Join(t.TempDir(), "enc.db"), keyring(t, "storage-key-v1"))
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "sealed"}
	seedSecrets(t, s, id)
	for col, raw := range rawColumns(t, s, id.ID) {
		if !crypt.IsSealed(raw) {
			t.Errorf("%s is not sealed", col)
		}
		for _, secret := range []string{"unlock-secret", "bootstrap-secret", "auth-token-secret", "PRIVATE KEY"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%s contains plaintext %q", col, secret)
			}
		}
		if name, ok := crypt.KeyName(raw); !ok || name != "storage-key-v1" {
			t.Errorf("%s sealed under %q", col, name)
		}
	}
	e, err := s.Get(ctx, id)
	if err != nil || string(e.UnlockToken) != "unlock-secret" {
		t.Fatalf("Get = %+v %v", e, err)
	}
	if tok, _ := s.BootstrapToken(ctx, id); string(tok) != "bootstrap-secret" {
		t.Fatalf("bootstrap = %q", tok)
	}
	st, _ := s.UserAuth(ctx, mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "sealed:u", ParentID: "sealed"})
	if st.AuthToken != "auth-token-secret" {
		t.Fatalf("auth token = %q", st.AuthToken)
	}
	if pc, _ := s.PushCert(ctx, "com.apple.mgmt.parity"); !bytes.Contains(pc.KeyPEM, []byte("PRIVATE KEY")) {
		t.Fatal("push key not decrypted")
	}
	list, _ := s.List(ctx, storage.EnrollmentQuery{}, storage.Page{})
	if len(list.Items) != 1 || string(list.Items[0].UnlockToken) != "unlock-secret" {
		t.Fatalf("List did not open unlock tokens: %+v", list.Items)
	}
}

// TestReadsPlaintextRowsWhenKeyringAdded covers a deployment that turns
// sealing on after data exists: plaintext rows keep reading until the
// keyring is Strict, and Rewrap seals them.
func TestReadsPlaintextRowsWhenKeyringAdded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mixed.db")
	plain := openWith(t, path, nil)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "mixed"}
	seedSecrets(t, plain, id)
	if raw := rawColumns(t, plain, id.ID); crypt.IsSealed(raw["unlock_token"]) {
		t.Fatal("sealed without a keyring")
	}
	if _, err := plain.Rewrap(ctx); !errors.Is(err, crypt.ErrNoKeyring) {
		t.Fatalf("Rewrap without keyring: %v", err)
	}
	_ = plain.Close()

	sealed := openWith(t, path, keyring(t, "storage-key-v1"))
	if e, err := sealed.Get(ctx, id); err != nil || string(e.UnlockToken) != "unlock-secret" {
		t.Fatalf("plaintext row with keyring: %+v %v", e, err)
	}
	n, err := sealed.Rewrap(ctx)
	if err != nil || n != 4 {
		t.Fatalf("Rewrap = %d %v, want 4", n, err)
	}
	for col, raw := range rawColumns(t, sealed, id.ID) {
		if name, ok := crypt.KeyName(raw); !ok || name != "storage-key-v1" {
			t.Errorf("%s after rewrap sealed under %q", col, name)
		}
	}
	if n, err := sealed.Rewrap(ctx); err != nil || n != 0 {
		t.Fatalf("second Rewrap = %d %v", n, err)
	}
	_ = sealed.Close()

	// Strict mode refuses plaintext rows.
	strict, err := crypt.NewKeyring(ctx, crypt.Options{Keys: crypt.Keys{Active: "storage-key-v1", Strict: true}, Provider: keyProvider})
	if err != nil {
		t.Fatal(err)
	}
	s := openWith(t, path, strict)
	if _, err := s.DB().ExecContext(ctx, "UPDATE enrollments SET unlock_token = ? WHERE id = ?", []byte("plain-again"), id.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, crypt.ErrUnsealed) {
		t.Fatalf("strict read of plaintext: %v", err)
	}
	if _, err := s.List(ctx, storage.EnrollmentQuery{}, storage.Page{}); !errors.Is(err, crypt.ErrUnsealed) {
		t.Fatalf("strict list of plaintext: %v", err)
	}
	if _, err := s.Rewrap(ctx); !errors.Is(err, crypt.ErrUnsealed) {
		t.Fatalf("strict rewrap of plaintext: %v", err)
	}
}

// TestRewrapMovesToActiveKey rotates from v1 to v2 and proves every row
// follows, including one written under the retired key after rotation.
func TestRewrapMovesToActiveKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rotate.db")
	v1 := openWith(t, path, keyring(t, "storage-key-v1"))
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "rotate"}
	seedSecrets(t, v1, id)
	_ = v1.Close()

	v2 := openWith(t, path, keyring(t, "storage-key-v2", "storage-key-v1"))
	if tok, err := v2.BootstrapToken(ctx, id); err != nil || string(tok) != "bootstrap-secret" {
		t.Fatalf("retired key read: %q %v", tok, err)
	}
	n, err := v2.Rewrap(ctx)
	if err != nil || n != 4 {
		t.Fatalf("Rewrap = %d %v", n, err)
	}
	for col, raw := range rawColumns(t, v2, id.ID) {
		if name, _ := crypt.KeyName(raw); name != "storage-key-v2" {
			t.Errorf("%s still under %q", col, name)
		}
	}
	if e, _ := v2.Get(ctx, id); string(e.UnlockToken) != "unlock-secret" {
		t.Fatal("unlock token lost in rotation")
	}
	_ = v2.Close()

	// Without the retired key the old ciphertext is unreadable, and a row
	// sealed with an unknown key surfaces as an error, not a panic.
	only2 := openWith(t, path, keyring(t, "storage-key-v2"))
	if _, err := only2.Get(ctx, id); err != nil {
		t.Fatalf("rotated rows must read with v2 alone: %v", err)
	}
	if _, err := only2.DB().ExecContext(ctx, "UPDATE enrollments SET unlock_token = ? WHERE id = ?", sealWith(t, "storage-key-v1", "unlock-secret"), id.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := only2.Get(ctx, id); !errors.Is(err, crypt.ErrUnknownKey) {
		t.Fatalf("unknown key: %v", err)
	}
	if _, err := only2.Rewrap(ctx); !errors.Is(err, crypt.ErrUnknownKey) {
		t.Fatalf("rewrap with unknown key: %v", err)
	}
}

func sealWith(t *testing.T, name, plaintext string) []byte {
	t.Helper()
	k := keyring(t, name)
	b, err := k.Seal([]byte(plaintext), crypt.AAD("enrollments.unlock_token", "rotate"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSealedRowWithoutKeyring: a database written with a keyring must not
// be readable, or silently wrong, when opened without one.
func TestSealedRowWithoutKeyring(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "locked.db")
	s := openWith(t, path, keyring(t, "storage-key-v1"))
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "locked"}
	seedSecrets(t, s, id)
	_ = s.Close()
	plain := openWith(t, path, nil)
	uid := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "locked:u", ParentID: "locked"}
	for name, call := range map[string]func() error{
		"Get":            func() error { _, err := plain.Get(ctx, id); return err },
		"BootstrapToken": func() error { _, err := plain.BootstrapToken(ctx, id); return err },
		"UserAuth":       func() error { _, err := plain.UserAuth(ctx, uid); return err },
		"PushCert":       func() error { _, err := plain.PushCert(ctx, "com.apple.mgmt.parity"); return err },
		"Export":         func() error { _, err := plain.Export(ctx, storage.Page{}); return err },
	} {
		if err := call(); !errors.Is(err, crypt.ErrNoKeyring) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A tampered ciphertext is rejected, never decrypted to garbage.
	sealed := openWith(t, path, keyring(t, "storage-key-v1"))
	if _, err := sealed.DB().ExecContext(ctx, "UPDATE enrollments SET unlock_token = ? WHERE id = ?", sealWith(t, "storage-key-v1", "moved-from-another-row"), id.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sealed.Get(ctx, id); !errors.Is(err, crypt.ErrTampered) {
		t.Fatalf("row swap not detected: %v", err)
	}
}

// TestRewrapSurfacesWriteFailure proves a failed UPDATE stops Rewrap with
// the partial count.
func TestRewrapSurfacesWriteFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rewrap-fail.db")
	plain := openWith(t, path, nil)
	seedSecrets(t, plain, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "rf"})
	_ = plain.Close()
	s := openWith(t, path, keyring(t, "storage-key-v1"))
	if _, err := s.DB().ExecContext(ctx, "CREATE TRIGGER no_update BEFORE UPDATE ON enrollments BEGIN SELECT RAISE(FAIL, 'writes disabled'); END"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Rewrap(ctx); err == nil || !strings.Contains(err.Error(), "writes disabled") || n != 0 {
		t.Fatalf("Rewrap = %d %v", n, err)
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE user_auth"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TRIGGER no_update"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rewrap(ctx); err == nil || errors.Is(err, crypt.ErrNoKeyring) {
		t.Fatalf("Rewrap over a missing table: %v", err)
	}
}

// TestCrossBackendMigration moves records inmem -> sealed sqlite -> inmem
// and expects the two in-memory exports to match.
func TestCrossBackendMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	src := inmem.New()
	dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "X"}
	usr := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "X:u", ParentID: "X"}
	for _, id := range []mdm.EnrollmentID{dev, usr} {
		if err := src.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t", Model: "Mac"}, []byte("<a/>"), t0); err != nil {
			t.Fatal(err)
		}
		if err := src.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1, 2}, Magic: "m"}, &checkin.TokenUpdate{UnlockToken: []byte("unlock")}, []byte("<tu/>"), t0.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := src.AssociateCert(ctx, dev, "h1", t0); err != nil {
		t.Fatal(err)
	}
	if err := src.StoreBootstrapToken(ctx, dev, []byte("bst"), t0); err != nil {
		t.Fatal(err)
	}
	move := func(from, to storage.Store) {
		t.Helper()
		res, err := from.Export(ctx, storage.Page{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Items {
			if err := to.Import(ctx, r); err != nil {
				t.Fatalf("import %s: %v", r.ID.ID, err)
			}
		}
	}
	mid := openWith(t, filepath.Join(t.TempDir(), "mid.db"), keyring(t, "storage-key-v1"))
	move(src, mid)
	var rawBootstrap, rawUnlock []byte
	if err := mid.DB().QueryRowContext(ctx, "SELECT bootstrap_token, unlock_token FROM enrollments WHERE id = 'X'").Scan(&rawBootstrap, &rawUnlock); err != nil {
		t.Fatal(err)
	}
	if !crypt.IsSealed(rawBootstrap) || !crypt.IsSealed(rawUnlock) {
		t.Fatal("imported secrets not sealed")
	}
	dst := inmem.New()
	move(mid, dst)
	a, _ := src.Export(ctx, storage.Page{})
	b, _ := dst.Export(ctx, storage.Page{})
	if len(a.Items) != 2 || len(b.Items) != 2 {
		t.Fatalf("exports %d and %d", len(a.Items), len(b.Items))
	}
	for i := range a.Items {
		x, y := a.Items[i], b.Items[i]
		if x.ID != y.ID || x.Enabled != y.Enabled || !bytes.Equal(x.UnlockToken, y.UnlockToken) || !bytes.Equal(x.BootstrapToken, y.BootstrapToken) ||
			!bytes.Equal(x.TokenUpdateRaw, y.TokenUpdateRaw) || x.CertHash != y.CertHash || len(x.CertHistory) != len(y.CertHistory) ||
			!x.EnrolledAt.Equal(y.EnrolledAt) || !x.TokenUpdatedAt.Equal(y.TokenUpdatedAt) || x.Device != y.Device {
			t.Fatalf("record %d differs:\n %+v\n %+v", i, x, y)
		}
	}
}

// pushPair issues a fresh push certificate pair for a distinct topic.
func pushPair(t *testing.T, at time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	ca, err := testpki.NewCA("sqlite push")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ca.IssuePush("com.apple.mgmt."+strings.ReplaceAll(t.Name(), "/", "-"), at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err = id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	return certPEM, keyPEM
}

// TestCorruptRowsSurface proves rows the store cannot decode fail loudly
// instead of being skipped: a bad error chain, a bad timestamp, and a
// history table that vanished mid-way.
func TestCorruptRowsSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openWith(t, filepath.Join(t.TempDir(), "corrupt.db"), nil)
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "c"}
	if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(ctx, []mdm.EnrollmentID{id}, &mdm.Command{UUID: "C1", RequestType: "ProfileList"}, storage.EnqueueOptions{Now: t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, "UPDATE commands SET result_status = 'Error', result_error_chain = '{not json' WHERE command_uuid = 'C1'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commands(ctx, id, storage.CommandQuery{}, storage.Page{}); err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("bad error chain: %v", err)
	}
	// A push certificate row with an unreadable timestamp.
	if _, err := s.DB().ExecContext(ctx, "INSERT INTO push_certs (topic, cert_pem, key_pem, not_after, version, updated_at) VALUES ('com.apple.mgmt.bad', X'00', X'00', 'never', 1, 'never')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PushCerts(ctx); err == nil {
		t.Fatal("bad not_after listed")
	}
	if _, err := s.PushCert(ctx, "com.apple.mgmt.bad"); err == nil {
		t.Fatal("bad not_after read")
	}
	// A history row with an unreadable timestamp.
	if _, err := s.DB().ExecContext(ctx, "INSERT INTO cert_associations (enrollment_id, cert_hash, associated_at) VALUES ('c', 'h', 'never')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CertHistory(ctx, id); err == nil {
		t.Fatal("bad associated_at read")
	}
	if _, err := s.Export(ctx, storage.Page{}); err == nil {
		t.Fatal("export over a bad history row")
	}
	// The history table disappears under a device with a pin.
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE cert_associations"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CertHashHistory(ctx, "h"); err == nil {
		t.Fatal("hash history without the table")
	}
	if _, err := s.Export(ctx, storage.Page{}); err == nil {
		t.Fatal("export without the history table")
	}
	if err := s.Import(ctx, storage.EnrollmentExport{ID: id, EnrolledAt: t0, LastSeenAt: t0, CertHistory: []storage.CertAssociation{{ID: id, Hash: "h", At: t0}}}); err == nil {
		t.Fatal("import history without the table")
	}
}

// TestChildUpdateFailureSurfaces makes only the user-channel update fail
// so the cascade branch of Disable and UpsertAuthenticate is exercised.
func TestChildUpdateFailureSurfaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openWith(t, filepath.Join(t.TempDir(), "child.db"), nil)
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "p"}
	usr := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "p:u", ParentID: "p"}
	for _, id := range []mdm.EnrollmentID{dev, usr} {
		if err := s.UpsertAuthenticate(ctx, id, &checkin.Authenticate{Topic: "t"}, nil, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.StoreTokenUpdate(ctx, id, mdm.Push{Topic: "t", Token: []byte{1}, Magic: "m"}, nil, nil, t0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().ExecContext(ctx, "CREATE TRIGGER no_child_update BEFORE UPDATE ON enrollments WHEN NEW.parent_id <> '' BEGIN SELECT RAISE(FAIL, 'children locked'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.Disable(ctx, dev, t0); err == nil || !strings.Contains(err.Error(), "children locked") {
		t.Fatalf("Disable: %v", err)
	}
	if e, _ := s.Get(ctx, dev); !e.Enabled {
		t.Fatal("device disabled although the cascade failed")
	}
	if err := s.UpsertAuthenticate(ctx, dev, &checkin.Authenticate{Topic: "t"}, nil, t0); err == nil || !strings.Contains(err.Error(), "children locked") {
		t.Fatalf("UpsertAuthenticate: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, "DROP TABLE user_auth"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAuthenticate(ctx, dev, &checkin.Authenticate{Topic: "t"}, nil, t0); err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("UpsertAuthenticate without user_auth: %v", err)
	}
}
