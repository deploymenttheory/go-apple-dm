package sqlstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/acme/acmetest"
	"github.com/deploymenttheory/go-apple-dm/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/acme/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/mysql"
	"github.com/deploymenttheory/go-apple-dm/storage/postgres"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "acme.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// openStore is a migrated store on a fresh SQLite database.
func openStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	s, err := sqlstore.Open(context.Background(), openDB(t), sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestStore runs the contract every acme.Store must satisfy against
// SQLite; the integration suite runs the same one on PostgreSQL and MySQL.
func TestStore(t *testing.T) {
	acmetest.RunAll(t, func(t *testing.T) acme.Store { return openStore(t) })
}

func TestOpenAndMigrations(t *testing.T) {
	ctx := context.Background()
	if _, err := sqlstore.Open(ctx, nil, sqlite.Dialect, sqlstore.Options{}); !errors.Is(err, acme.ErrInvalid) {
		t.Fatalf("nil db = %v", err)
	}
	if _, err := sqlstore.MigrationSet(sqlcommon.Dialect{Name: "oracle"}); !errors.Is(err, sqlstore.ErrUnsupportedDialect) {
		t.Fatalf("unknown dialect = %v", err)
	}
	db := openDB(t)
	s, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.DB() != db {
		t.Fatal("DB accessor")
	}
	v, err := sqlstore.Version(ctx, db, sqlite.Dialect)
	if err != nil || v != 1 {
		t.Fatalf("version = %d %v", v, err)
	}
	// Idempotent: a second Open applies nothing; SkipMigrate leaves the schema alone.
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{SkipMigrate: true}); err != nil {
		t.Fatal(err)
	}
	reverted, err := sqlstore.Rollback(ctx, db, sqlite.Dialect, 0)
	if err != nil || len(reverted) != 1 {
		t.Fatalf("rollback = %v %v", reverted, err)
	}
	if v, err := sqlstore.Version(ctx, db, sqlite.Dialect); err != nil || v != 0 {
		t.Fatalf("version after rollback = %d %v", v, err)
	}
	// Without the schema the store fails on use.
	bare, err := sqlstore.Open(ctx, db, sqlite.Dialect, sqlstore.Options{SkipMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bare.GetAccount(ctx, "x"); err == nil {
		t.Fatal("query without schema succeeded")
	}
}

// TestMigrationsAgreeAcrossDialects pins the three embedded migration
// directories to the same version and name sequence with a down section
// each, and to the same tables with the same columns in the same order, so
// a schema change cannot land in one engine only.
func TestMigrationsAgreeAcrossDialects(t *testing.T) {
	load := func(d sqlcommon.Dialect) []sqlcommon.Migration {
		t.Helper()
		set, err := sqlstore.MigrationSet(d)
		if err != nil {
			t.Fatal(err)
		}
		ms, err := sqlcommon.LoadMigrations(set.FS)
		if err != nil || len(ms) == 0 {
			t.Fatalf("%s migrations: %v %v", d.Name, ms, err)
		}
		return ms
	}
	ref := load(sqlite.Dialect)
	refTables := tables(t, ref)
	if len(refTables) != 7 {
		t.Fatalf("sqlite declares %d tables, want 7", len(refTables))
	}
	for _, d := range []sqlcommon.Dialect{postgres.Dialect, mysql.Dialect} {
		ms := load(d)
		if len(ms) != len(ref) {
			t.Fatalf("%s has %d migrations, sqlite has %d", d.Name, len(ms), len(ref))
		}
		for i := range ref {
			if ms[i].Version != ref[i].Version || ms[i].Name != ref[i].Name {
				t.Fatalf("%s migration %d is %d_%s, sqlite has %d_%s", d.Name, i, ms[i].Version, ms[i].Name, ref[i].Version, ref[i].Name)
			}
			if len(ms[i].Down) == 0 || len(ref[i].Down) == 0 {
				t.Fatalf("%s %d_%s has no down section", d.Name, ms[i].Version, ms[i].Name)
			}
		}
		got := tables(t, ms)
		if len(got) != len(refTables) {
			t.Fatalf("%s declares %d tables, sqlite %d", d.Name, len(got), len(refTables))
		}
		for name, cols := range refTables {
			if strings.Join(got[name], ",") != strings.Join(cols, ",") {
				t.Errorf("%s %s columns %v, sqlite %v", d.Name, name, got[name], cols)
			}
		}
	}
}

// tables reads the column names of every CREATE TABLE in a migration set,
// so the three dialects can be compared on what they declare rather than
// on how they spell a timestamp.
func tables(t *testing.T, ms []sqlcommon.Migration) map[string][]string {
	t.Helper()
	// A line inside a CREATE TABLE that begins with one of these defines a
	// key or an index rather than a column.
	constraints := []string{"PRIMARY", "UNIQUE", "INDEX", "KEY", "CONSTRAINT", "FOREIGN"}
	out := map[string][]string{}
	for _, m := range ms {
		for _, stmt := range m.Up {
			if !strings.HasPrefix(stmt, "CREATE TABLE ") {
				continue
			}
			head, body, ok := strings.Cut(stmt, "(")
			if !ok {
				t.Fatalf("no column list in %q", stmt)
			}
			name := strings.TrimSpace(strings.TrimPrefix(head, "CREATE TABLE "))
			var cols []string
			for line := range strings.SplitSeq(body, "\n") {
				field, _, _ := strings.Cut(strings.TrimSpace(line), " ")
				if field == "" || field == ")" || strings.HasPrefix(field, ")") {
					continue
				}
				if slices.Contains(constraints, field) {
					continue
				}
				cols = append(cols, field)
			}
			out[name] = cols
		}
	}
	return out
}

// t0 is a fixed instant every fixture is dated from.
var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// seedOrder writes an account, an order, its authorization, its challenge,
// and the claim on its identifier, the way the server creates them: in one
// transaction.
func seedOrder(t *testing.T, s *sqlstore.Store, account, order string) {
	t.Helper()
	ctx := context.Background()
	err := s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutAccount(ctx, &acme.Account{ID: account, Thumbprint: "tp-" + account, Status: acme.StatusValid, CreatedAt: t0}); err != nil {
			return err
		}
		if err := tx.PutOrder(ctx, &acme.Order{
			ID: order, AccountID: account, AuthzID: "az-" + order, Status: acme.StatusPending,
			Identifier: acme.Identifier{Type: acme.IdentifierPermanent, Value: "ci-" + order},
			Binding:    acme.Binding{Serial: "S" + order, Organization: []string{"Acme"}},
			Expires:    t0.Add(time.Hour), CreatedAt: t0,
		}); err != nil {
			return err
		}
		if err := tx.PutAuthorization(ctx, &acme.Authorization{
			ID: "az-" + order, OrderID: order, AccountID: account, Status: acme.StatusPending,
			ChallengeID: "ch-" + order, Expires: t0.Add(time.Hour),
		}); err != nil {
			return err
		}
		if err := tx.PutChallenge(ctx, &acme.Challenge{
			ID: "ch-" + order, AuthzID: "az-" + order, AccountID: account,
			Type: acme.ChallengeDeviceAttest, Token: "tok-" + order, Status: acme.StatusPending,
		}); err != nil {
			return err
		}
		return tx.ClaimIdentifier(ctx, "ci-"+order, order)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRecordsRoundTrip: every record comes back as it went in, through the
// store and through a transaction.
func TestRecordsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedOrder(t, s, "acct", "o1")
	cert := &acme.Certificate{
		ID: "c1", OrderID: "o1", AccountID: "acct", Serial: "0A0B", ChainPEM: []byte("-----BEGIN CERTIFICATE-----"),
		Device:   attestedDevice(),
		Binding:  acme.Binding{Serial: "So1", CommonName: "device", Organization: []string{"Acme"}},
		NotAfter: t0.Add(24 * time.Hour), IssuedAt: t0,
	}
	if err := s.Update(ctx, func(tx acme.Tx) error { return tx.PutCertificate(ctx, cert) }); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAccount(ctx, "acct")
	if err != nil || a.Thumbprint != "tp-acct" || !a.CreatedAt.Equal(t0) {
		t.Fatalf("account = %+v %v", a, err)
	}
	byKey, err := s.AccountByThumbprint(ctx, "tp-acct")
	if err != nil || byKey.ID != "acct" {
		t.Fatalf("account by thumbprint = %+v %v", byKey, err)
	}
	o, err := s.GetOrder(ctx, "o1")
	if err != nil || o.Identifier.Value != "ci-o1" || o.Binding.Organization[0] != "Acme" || !o.Expires.Equal(t0.Add(time.Hour)) {
		t.Fatalf("order = %+v %v", o, err)
	}
	az, err := s.GetAuthorization(ctx, "az-o1")
	if err != nil || az.ChallengeID != "ch-o1" || az.OrderID != "o1" {
		t.Fatalf("authorization = %+v %v", az, err)
	}
	ch, err := s.GetChallenge(ctx, "ch-o1")
	if err != nil || ch.Token != "tok-o1" || ch.Type != acme.ChallengeDeviceAttest {
		t.Fatalf("challenge = %+v %v", ch, err)
	}
	got, err := s.GetCertificate(ctx, "c1")
	if err != nil || got.Serial != "0A0B" || string(got.ChainPEM) != string(cert.ChainPEM) || got.Device.UDID != cert.Device.UDID {
		t.Fatalf("certificate = %+v %v", got, err)
	}
	if got.Device.SIPEnabled == nil || !*got.Device.SIPEnabled || string(got.Device.Freshness) != "fresh" {
		t.Fatalf("device properties = %+v", got.Device)
	}
	// The same reads inside a transaction see the same records.
	err = s.Update(ctx, func(tx acme.Tx) error {
		for _, read := range []func() error{
			func() error { _, err := tx.GetAccount(ctx, "acct"); return err },
			func() error { _, err := tx.AccountByThumbprint(ctx, "tp-acct"); return err },
			func() error { _, err := tx.GetOrder(ctx, "o1"); return err },
			func() error { _, err := tx.GetAuthorization(ctx, "az-o1"); return err },
			func() error { _, err := tx.GetChallenge(ctx, "ch-o1"); return err },
			func() error { _, err := tx.GetCertificate(ctx, "c1"); return err },
		} {
			if err := read(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A record replaced whole keeps its row and its indexed copies.
	o.Status = acme.StatusValid
	o.CertificateID = "c1"
	if err := s.Update(ctx, func(tx acme.Tx) error { return tx.PutOrder(ctx, o) }); err != nil {
		t.Fatal(err)
	}
	if again, err := s.GetOrder(ctx, "o1"); err != nil || again.Status != acme.StatusValid || again.CertificateID != "c1" {
		t.Fatalf("replaced order = %+v %v", again, err)
	}
}

// TestMissingRecords: every read of a record that is not there is
// ErrNotFound, and an empty id is ErrInvalid.
func TestMissingRecords(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	missing := map[string]func(string) error{
		"account":       func(id string) error { _, err := s.GetAccount(ctx, id); return err },
		"thumbprint":    func(id string) error { _, err := s.AccountByThumbprint(ctx, id); return err },
		"order":         func(id string) error { _, err := s.GetOrder(ctx, id); return err },
		"authorization": func(id string) error { _, err := s.GetAuthorization(ctx, id); return err },
		"challenge":     func(id string) error { _, err := s.GetChallenge(ctx, id); return err },
		"certificate":   func(id string) error { _, err := s.GetCertificate(ctx, id); return err },
		"nonce":         func(id string) error { _, err := s.TakeNonce(ctx, id); return err },
	}
	for name, read := range missing {
		if err := read("nope"); !errors.Is(err, acme.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
		if err := read(""); !errors.Is(err, acme.ErrInvalid) {
			t.Errorf("%s: empty id err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := s.ListOrders(ctx, "", storage.Page{}); !errors.Is(err, acme.ErrInvalid) {
		t.Errorf("ListOrders with no account = %v", err)
	}
}

// TestInvalidWrites: a nil or unidentified record is refused before it
// reaches the database.
func TestInvalidWrites(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	writes := map[string]func(acme.Tx) error{
		"nil account":           func(tx acme.Tx) error { return tx.PutAccount(ctx, nil) },
		"account without id":    func(tx acme.Tx) error { return tx.PutAccount(ctx, &acme.Account{Thumbprint: "t"}) },
		"account without key":   func(tx acme.Tx) error { return tx.PutAccount(ctx, &acme.Account{ID: "a"}) },
		"nil order":             func(tx acme.Tx) error { return tx.PutOrder(ctx, nil) },
		"order without id":      func(tx acme.Tx) error { return tx.PutOrder(ctx, &acme.Order{AccountID: "a"}) },
		"order without account": func(tx acme.Tx) error { return tx.PutOrder(ctx, &acme.Order{ID: "o"}) },
		"nil authorization":     func(tx acme.Tx) error { return tx.PutAuthorization(ctx, nil) },
		"authorization without id": func(tx acme.Tx) error {
			return tx.PutAuthorization(ctx, &acme.Authorization{OrderID: "o"})
		},
		"nil challenge":          func(tx acme.Tx) error { return tx.PutChallenge(ctx, nil) },
		"challenge without id":   func(tx acme.Tx) error { return tx.PutChallenge(ctx, &acme.Challenge{AuthzID: "az"}) },
		"nil certificate":        func(tx acme.Tx) error { return tx.PutCertificate(ctx, nil) },
		"certificate without id": func(tx acme.Tx) error { return tx.PutCertificate(ctx, &acme.Certificate{Serial: "1"}) },
		"claim without identifier": func(tx acme.Tx) error {
			return tx.ClaimIdentifier(ctx, "", "o")
		},
		"claim without order": func(tx acme.Tx) error { return tx.ClaimIdentifier(ctx, "ci", "") },
	}
	for name, write := range writes {
		err := s.Update(ctx, func(tx acme.Tx) error { return write(tx) })
		if !errors.Is(err, acme.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if err := s.PutNonce(ctx, acme.Nonce{}); !errors.Is(err, acme.ErrInvalid) {
		t.Errorf("nonce without a value = %v", err)
	}
	if err := s.Update(ctx, nil); !errors.Is(err, acme.ErrInvalid) {
		t.Errorf("nil Update callback = %v", err)
	}
}

// attestedDevice is what Apple's attestation said about the hardware, with
// the pointer and slice members set so they have to survive the record
// column rather than defaulting.
func attestedDevice() attest.Properties {
	yes, no := true, false
	return attest.Properties{
		SerialNumber: "C02XY", UDID: "11111111-2222-3333-4444-555555555555",
		SoftwareUpdateDeviceID: "J413", OSVersion: "15.2", SEPOSVersion: "15.2",
		LLBVersion: "11881.61.3", SecureBoot: "full",
		SIPEnabled: &yes, KextsAllowed: &no, Freshness: []byte("fresh"),
	}
}

// TestAccountKeyIsUnique: the unique index on the thumbprint is what makes
// a second registration of one key a conflict, and it holds however the
// second account arrives.
func TestAccountKeyIsUnique(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedOrder(t, s, "acct", "o1")
	put := func(a *acme.Account) error {
		return s.Update(ctx, func(tx acme.Tx) error { return tx.PutAccount(ctx, a) })
	}
	// A new account claiming the key of an existing one loses.
	if err := put(&acme.Account{ID: "other", Thumbprint: "tp-acct", Status: acme.StatusValid}); !errors.Is(err, acme.ErrConflict) {
		t.Fatalf("duplicate thumbprint = %v, want ErrConflict", err)
	}
	// So does an existing account moved onto another's key.
	if err := put(&acme.Account{ID: "second", Thumbprint: "tp-second"}); err != nil {
		t.Fatal(err)
	}
	if err := put(&acme.Account{ID: "second", Thumbprint: "tp-acct"}); !errors.Is(err, acme.ErrConflict) {
		t.Fatalf("moved thumbprint = %v, want ErrConflict", err)
	}
	// The loser changed nothing.
	if a, err := s.AccountByThumbprint(ctx, "tp-acct"); err != nil || a.ID != "acct" {
		t.Fatalf("account for the contested key = %+v %v", a, err)
	}
	// Re-registering the same key as the same account is not a conflict:
	// RFC 8555 makes that request return the account it already is.
	if err := put(&acme.Account{ID: "acct", Thumbprint: "tp-acct", Status: acme.StatusValid, CreatedAt: t0, Contact: []string{"mailto:it@example.com"}}); err != nil {
		t.Fatal(err)
	}
	if a, err := s.GetAccount(ctx, "acct"); err != nil || len(a.Contact) != 1 {
		t.Fatalf("re-registered account = %+v %v", a, err)
	}
}

// TestIdentifierIsClaimedOnce: Apple's client identifier is an anti-replay
// code, so the second order to present one is refused by the primary key
// rather than by a read.
func TestIdentifierIsClaimedOnce(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedOrder(t, s, "acct", "o1")
	err := s.Update(ctx, func(tx acme.Tx) error { return tx.ClaimIdentifier(ctx, "ci-o1", "o2") })
	if !errors.Is(err, acme.ErrConflict) {
		t.Fatalf("second claim = %v, want ErrConflict", err)
	}
	// Even the order that holds the claim cannot take it twice.
	err = s.Update(ctx, func(tx acme.Tx) error { return tx.ClaimIdentifier(ctx, "ci-o1", "o1") })
	if !errors.Is(err, acme.ErrConflict) {
		t.Fatalf("repeated claim = %v, want ErrConflict", err)
	}
	// The transaction that lost left nothing behind.
	if _, err := s.GetOrder(ctx, "o2"); !errors.Is(err, acme.ErrNotFound) {
		t.Fatalf("losing order = %v", err)
	}
}

// TestUpdateRollsBack: a callback that fails leaves no record of what it
// wrote, which is what lets the server create an order, its authorization,
// its challenge, and its claim as one thing.
func TestUpdateRollsBack(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedOrder(t, s, "acct", "o1")
	boom := errors.New("policy said no")
	err := s.Update(ctx, func(tx acme.Tx) error {
		if err := tx.PutOrder(ctx, &acme.Order{ID: "o2", AccountID: "acct", Status: acme.StatusPending}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update = %v, want the callback's error", err)
	}
	if _, err := s.GetOrder(ctx, "o2"); !errors.Is(err, acme.ErrNotFound) {
		t.Fatalf("rolled-back order = %v", err)
	}
}

// TestNonces: a nonce is single use, and two takers of one nonce yield
// exactly one success.
func TestNonces(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if err := s.PutNonce(ctx, acme.Nonce{Value: "n1", IssuedAt: t0}); err != nil {
		t.Fatal(err)
	}
	// Minting the same value again replaces the row rather than failing.
	if err := s.PutNonce(ctx, acme.Nonce{Value: "n1", IssuedAt: t0.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	n, err := s.TakeNonce(ctx, "n1")
	if err != nil || n.Value != "n1" || !n.IssuedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("take = %+v %v", n, err)
	}
	if _, err := s.TakeNonce(ctx, "n1"); !errors.Is(err, acme.ErrNotFound) {
		t.Fatalf("replay = %v, want ErrNotFound", err)
	}
	// A nonce with no issue time is stored as NULL and read back as zero.
	if err := s.PutNonce(ctx, acme.Nonce{Value: "n2"}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.TakeNonce(ctx, "n2"); err != nil || !n.IssuedAt.IsZero() {
		t.Fatalf("undated nonce = %+v %v", n, err)
	}
	t.Run("OnlyOneTakerWins", func(t *testing.T) {
		for i := range 20 {
			value := "race" + strconv.Itoa(i)
			if err := s.PutNonce(ctx, acme.Nonce{Value: value, IssuedAt: t0}); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			var mu sync.Mutex
			var wins int
			var errs []error
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := s.TakeNonce(ctx, value)
					mu.Lock()
					defer mu.Unlock()
					if err == nil {
						wins++
						return
					}
					errs = append(errs, err)
				}()
			}
			wg.Wait()
			if wins != 1 {
				t.Fatalf("%s: %d takers won, want 1 (%v)", value, wins, errs)
			}
			for _, err := range errs {
				if !errors.Is(err, acme.ErrNotFound) {
					t.Fatalf("%s: loser saw %v, want ErrNotFound", value, err)
				}
			}
		}
	})
}

// TestAttestationRoundTripsExactly: the attestation object comes back
// byte for byte, because finalize verifies the stored bytes again against
// a certificate request the challenge never saw.
func TestAttestationRoundTripsExactly(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	// Bytes no text encoding would survive: a NUL, invalid UTF-8, and the
	// whole range.
	raw := make([]byte, 256)
	for i := range raw {
		raw[i] = byte(i)
	}
	raw = append(raw, 0xC3, 0x28, 0x00, 0xFF)
	ch := &acme.Challenge{
		ID: "ch1", AuthzID: "az1", AccountID: "acct", Type: acme.ChallengeDeviceAttest,
		Token: "tok", Status: acme.StatusValid, Attestation: raw, ValidatedAt: t0,
	}
	if err := s.Update(ctx, func(tx acme.Tx) error { return tx.PutChallenge(ctx, ch) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetChallenge(ctx, "ch1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Attestation, raw) {
		t.Fatalf("attestation came back as % x, want % x", got.Attestation, raw)
	}
	if !got.ValidatedAt.Equal(t0) {
		t.Fatalf("validated at %v, want %v", got.ValidatedAt, t0)
	}
	// A challenge that was never answered keeps no attestation.
	ch.Attestation = nil
	if err := s.Update(ctx, func(tx acme.Tx) error { return tx.PutChallenge(ctx, ch) }); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetChallenge(ctx, "ch1"); err != nil || len(got.Attestation) != 0 {
		t.Fatalf("unanswered challenge = %+v %v", got, err)
	}
}

// TestPaging: both listings page by keyset on the record id and stop
// without a trailing empty read.
func TestPaging(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	for i := range 5 {
		seedOrder(t, s, "acct", "o"+strconv.Itoa(i))
	}
	seedOrder(t, s, "other", "z9")
	var seen []string
	page := storage.Page{Limit: 2}
	for {
		res, err := s.ListOrders(ctx, "acct", page)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range res.Items {
			seen = append(seen, o.ID)
		}
		if res.NextCursor == "" {
			break
		}
		if len(res.Items) != 2 {
			t.Fatalf("page of %d items carries a cursor", len(res.Items))
		}
		page.Cursor = res.NextCursor
	}
	if strings.Join(seen, ",") != "o0,o1,o2,o3,o4" {
		t.Fatalf("orders = %v", seen)
	}
	// The default page size applies when the caller asks for none.
	if res, err := s.ListOrders(ctx, "acct", storage.Page{}); err != nil || len(res.Items) != 5 || res.NextCursor != "" {
		t.Fatalf("unpaged orders = %d %q %v", len(res.Items), res.NextCursor, err)
	}
	if res, err := s.ListOrders(ctx, "nobody", storage.Page{}); err != nil || len(res.Items) != 0 {
		t.Fatalf("orders of an unknown account = %d %v", len(res.Items), err)
	}
}

// TestCertificateQuery: every non-empty field of the query narrows the
// listing, and the serial number and UDID are the attested device's rather
// than the certificate's own serial. The two are deliberately different
// here, because making them the same would hide which one is filtered.
func TestCertificateQuery(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	dev := attestedDevice()
	other := attest.Properties{SerialNumber: "BB", UDID: "another-udid"}
	err := s.Update(ctx, func(tx acme.Tx) error {
		for _, c := range []*acme.Certificate{
			{ID: "c1", AccountID: "a1", OrderID: "o1", Serial: "cert-serial-1", Device: dev, IssuedAt: t0, NotAfter: t0.Add(time.Hour)},
			{ID: "c2", AccountID: "a1", OrderID: "o2", Serial: "cert-serial-2", Device: other, IssuedAt: t0, NotAfter: t0.Add(time.Hour)},
			{ID: "c3", AccountID: "a2", OrderID: "o3", Serial: "cert-serial-3", Device: attest.Properties{SerialNumber: dev.SerialNumber}, IssuedAt: t0, NotAfter: t0.Add(time.Hour)},
		} {
			if err := tx.PutCertificate(ctx, c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		q    acme.CertificateQuery
		want string
	}{
		{"all", acme.CertificateQuery{}, "c1,c2,c3"},
		{"serial", acme.CertificateQuery{DeviceSerial: dev.SerialNumber}, "c1,c3"},
		{"udid", acme.CertificateQuery{UDID: dev.UDID}, "c1"},
		{"account", acme.CertificateQuery{AccountID: "a1"}, "c1,c2"},
		{"serial and account", acme.CertificateQuery{DeviceSerial: dev.SerialNumber, AccountID: "a2"}, "c3"},
		{"no match", acme.CertificateQuery{DeviceSerial: "ZZ"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ids []string
			page := storage.Page{Limit: 1}
			for {
				res, err := s.ListCertificates(ctx, tc.q, page)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range res.Items {
					ids = append(ids, c.ID)
				}
				if res.NextCursor == "" {
					break
				}
				page.Cursor = res.NextCursor
			}
			if strings.Join(ids, ",") != tc.want {
				t.Fatalf("certificates = %v, want %s", ids, tc.want)
			}
		})
	}
}

// TestPrune removes what has expired and keeps the record of what was
// given out.
func TestPrune(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	seedOrder(t, s, "acct", "live")
	seedOrder(t, s, "acct", "dead")
	// Age the second order and its authorization past the cutoff, and add a
	// certificate, an old nonce, and a fresh one.
	err := s.Update(ctx, func(tx acme.Tx) error {
		o, err := tx.GetOrder(ctx, "dead")
		if err != nil {
			return err
		}
		o.Expires = t0.Add(-time.Hour)
		if err := tx.PutOrder(ctx, o); err != nil {
			return err
		}
		az, err := tx.GetAuthorization(ctx, "az-dead")
		if err != nil {
			return err
		}
		az.Expires = t0.Add(-time.Hour)
		if err := tx.PutAuthorization(ctx, az); err != nil {
			return err
		}
		return tx.PutCertificate(ctx, &acme.Certificate{ID: "c1", AccountID: "acct", OrderID: "dead", Serial: "AA", IssuedAt: t0.Add(-time.Hour), NotAfter: t0})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []acme.Nonce{{Value: "old", IssuedAt: t0.Add(-time.Hour)}, {Value: "new", IssuedAt: t0.Add(time.Hour)}} {
		if err := s.PutNonce(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	// The expired order, its authorization, its challenge, and the stale
	// nonce: four rows.
	removed, err := s.Prune(ctx, t0)
	if err != nil || removed != 4 {
		t.Fatalf("prune removed %d rows (%v), want 4", removed, err)
	}
	if _, err := s.GetOrder(ctx, "dead"); !errors.Is(err, acme.ErrNotFound) {
		t.Errorf("expired order survived: %v", err)
	}
	if _, err := s.GetAuthorization(ctx, "az-dead"); !errors.Is(err, acme.ErrNotFound) {
		t.Errorf("authorization survived: %v", err)
	}
	if _, err := s.GetChallenge(ctx, "ch-dead"); !errors.Is(err, acme.ErrNotFound) {
		t.Errorf("challenge survived: %v", err)
	}
	if _, err := s.TakeNonce(ctx, "old"); !errors.Is(err, acme.ErrNotFound) {
		t.Errorf("stale nonce survived: %v", err)
	}
	// The account, the certificate, the live order, and the fresh nonce are
	// all still there: an issued identity is the record of what was given
	// out and is never pruned.
	if _, err := s.GetAccount(ctx, "acct"); err != nil {
		t.Errorf("account: %v", err)
	}
	if _, err := s.GetCertificate(ctx, "c1"); err != nil {
		t.Errorf("certificate: %v", err)
	}
	if _, err := s.GetOrder(ctx, "live"); err != nil {
		t.Errorf("live order: %v", err)
	}
	if _, err := s.GetChallenge(ctx, "ch-live"); err != nil {
		t.Errorf("live challenge: %v", err)
	}
	if _, err := s.TakeNonce(ctx, "new"); err != nil {
		t.Errorf("fresh nonce: %v", err)
	}
	// A second sweep finds nothing, and the claim on the pruned order's
	// identifier is kept: Apple calls it a one-time code, so it is never
	// released.
	if removed, err := s.Prune(ctx, t0); err != nil || removed != 0 {
		t.Fatalf("second sweep removed %d rows (%v)", removed, err)
	}
	err = s.Update(ctx, func(tx acme.Tx) error { return tx.ClaimIdentifier(ctx, "ci-dead", "again") })
	if !errors.Is(err, acme.ErrConflict) {
		t.Fatalf("claim of a pruned order's identifier = %v, want ErrConflict", err)
	}
}
