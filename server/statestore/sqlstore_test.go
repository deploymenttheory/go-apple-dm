package statestore_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/ratelimit"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

func sqliteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(
		"sqlite",
		sqlite.DSN(filepath.Join(t.TempDir(), "state.db"), sqlite.Options{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func TestSQLiteSharedState(t *testing.T) { exercise(t, sqliteDB(t), sqlite.Dialect) }

func exercise(t *testing.T, db *sql.DB, dialect sqlcommon.Dialect) {
	t.Helper()
	ctx := t.Context()
	a, err := statestore.Open(ctx, db, dialect)
	if err != nil {
		t.Fatal(err)
	}
	b, err := statestore.Open(ctx, db, dialect)
	if err != nil {
		t.Fatal(err)
	}
	exerciseSCEPGrants(t, a, b)
	key := fmt.Sprintf("test/%d/", time.Now().UnixNano())
	if err := a.Update(ctx, []string{key}, func(tx state.Tx) error {
		if time.Since(tx.Now()) > time.Second || tx.Now().After(time.Now().Add(time.Second)) {
			t.Fatal("database time", tx.Now())
		}
		for _, suffix := range []string{"a", "a%", "a_", "b"} {
			if err := tx.Put(
				ctx,
				state.Record{Key: key + suffix, Value: []byte(suffix)},
			); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := b.List(ctx, key+"a", key+"a", 2)
	if err != nil || len(rows) != 2 || rows[0].Key != key+"a%" || rows[1].Key != key+"a_" {
		t.Fatal(rows, err)
	}
	boom := errors.New("rollback")
	if err := a.Update(ctx, []string{key}, func(tx state.Tx) error {
		_ = tx.Delete(ctx, key+"a")
		_ = tx.Put(ctx, state.Record{Key: key + "new"})
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, key+"a"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, key+"new"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	if err := a.Update(ctx, []string{key}, func(tx state.Tx) error {
		return tx.Put(ctx, state.Record{Key: key + "expired", ExpiresAt: tx.Now().Add(-time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Prune(ctx, 100); err != nil || n < 1 {
		t.Fatal(n, err)
	}
	if _, err := a.Get(ctx, key+"expired"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	// Separate limiter instances see one burst, regardless of concurrent arrival.
	limits := []*ratelimit.Limiter{{Store: a, Namespace: key}, {Store: b, Namespace: key}}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			dec, err := limits[i%2].Check(
				ctx,
				[]ratelimit.Bucket{
					{Key: "global", Interval: time.Hour, Burst: 7},
					{Key: "peer", Interval: time.Hour, Burst: 20},
				},
			)
			if err != nil {
				t.Error(err)
			}
			if dec.Allowed {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 7 {
		t.Fatalf("shared burst admitted %d", accepted.Load())
	}
	// Single-use token consumption also shares a transaction across instances.
	stores := []*accountdriven.StateTokenStore{{Backend: a}, {Backend: b}}
	now := time.Now()
	token := key + "code"
	if err := stores[0].Put(
		ctx,
		token,
		accountdriven.Record{
			Kind:      accountdriven.KindCode,
			IssuedAt:  now,
			ExpiresAt: now.Add(time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	accepted.Store(0)
	for i := range 20 {
		wg.Go(func() {
			err := stores[i%2].Exchange(
				ctx,
				token,
				now,
				func(accountdriven.Record) error { return nil },
				nil,
			)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, accountdriven.ErrTokenUsed) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("single-use token accepted", accepted.Load())
	}
	exerciseCertificateState(t, a, b)
	// Rejecting metadata leaves the token available for its rightful client.
	if err := stores[0].Put(
		ctx,
		token+"2",
		accountdriven.Record{Kind: accountdriven.KindCode, ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	if err := stores[0].Exchange(
		ctx,
		token+"2",
		now,
		func(accountdriven.Record) error { return boom },
		nil,
	); !errors.Is(
		err,
		boom,
	) {
		t.Fatal(err)
	}
	if err := stores[1].MarkUsed(ctx, token+"2", now); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidAndDatabaseFailures(t *testing.T) {
	ctx := t.Context()
	db := sqliteDB(t)
	if _, err := statestore.Open(ctx, nil, sqlite.Dialect); err == nil {
		t.Fatal("nil db")
	}
	if _, err := statestore.Open(
		ctx,
		db,
		sqlcommon.Dialect{Name: "oracle", Upsert: sqlcommon.UpsertOnConflict},
	); err == nil {
		t.Fatal("unknown dialect")
	}
	s, err := statestore.Open(ctx, db, sqlite.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	for _, keys := range [][]string{nil, {""}} {
		if err := s.Update(
			ctx,
			keys,
			func(state.Tx) error { return nil },
		); !errors.Is(
			err,
			state.ErrInvalid,
		) {
			t.Fatal(err)
		}
	}
	if err := s.Update(ctx, []string{"a"}, nil); !errors.Is(err, state.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ""); !errors.Is(err, state.ErrInvalid) {
		t.Fatal(err)
	}
	for _, n := range []int{0, 10001} {
		if _, err := s.List(ctx, "", "", n); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := s.Prune(ctx, n); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := s.Update(ctx, []string{"a"}, func(tx state.Tx) error {
		if err := tx.Put(ctx, state.Record{}); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
		if err := tx.Delete(ctx, ""); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Update(cancelled, []string{"a"}, func(state.Tx) error { return nil }); err == nil {
		t.Fatal("cancelled update")
	}
	if n, err := s.Prune(ctx, 10); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := statestore.Open(ctx, db, sqlite.Dialect); err == nil {
		t.Fatal("closed open")
	}
	if _, err := s.Get(ctx, "a"); err == nil {
		t.Fatal("closed read")
	}
	if _, err := s.List(ctx, "", "", 1); err == nil {
		t.Fatal("closed list")
	}
	if _, err := s.Prune(ctx, 10); err == nil {
		t.Fatal("closed prune")
	}
	if err := s.Update(ctx, []string{"a"}, func(state.Tx) error { return nil }); err == nil {
		t.Fatal("closed update")
	}
}

func TestMalformedRowsAndFailedPruning(t *testing.T) {
	t.Run("malformed expiry", func(t *testing.T) {
		ctx := t.Context()
		db := sqliteDB(t)
		s, err := statestore.Open(ctx, db, sqlite.Dialect)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(
			ctx,
			"INSERT INTO protocol_state VALUES ('corrupt', X'00', 'not a timestamp')",
		); err != nil {
			t.Fatal(err)
		}
		if _, err := s.List(ctx, "", "", 10); err == nil {
			t.Fatal("malformed expiry accepted")
		}
	})
	t.Run("malformed key", func(t *testing.T) {
		ctx := t.Context()
		db := sqliteDB(t)
		s, err := statestore.Open(ctx, db, sqlite.Dialect)
		if err != nil {
			t.Fatal(err)
		}
		// SQLite permits NULL in a non-integer PRIMARY KEY. State writers never
		// create one, but an out-of-band damaged row must be reported by pruning.
		if _, err := db.ExecContext(
			ctx,
			"INSERT INTO protocol_state VALUES (NULL, X'00', 1)",
		); err != nil {
			t.Fatal(err)
		}
		if n, err := s.Prune(ctx, 10); err == nil || n != 0 {
			t.Fatal(n, err)
		}
	})
	t.Run("delete rollback", func(t *testing.T) {
		ctx := t.Context()
		db := sqliteDB(t)
		s, err := statestore.Open(ctx, db, sqlite.Dialect)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Update(ctx, []string{"a", "b"}, func(tx state.Tx) error {
			for _, key := range []string{"a", "b"} {
				if err := tx.Put(
					ctx,
					state.Record{Key: key, ExpiresAt: tx.Now().Add(-time.Hour)},
				); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(
			ctx,
			"CREATE TRIGGER prevent_delete BEFORE DELETE ON protocol_state WHEN OLD.record_key = 'b' BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END",
		); err != nil {
			t.Fatal(err)
		}
		if n, err := s.Prune(ctx, 10); err == nil || n != 0 {
			t.Fatal("partial prune reported", n, err)
		}
		rows, err := s.List(ctx, "", "", 10)
		if err != nil || len(rows) != 2 || rows[0].ExpiresAt.IsZero() {
			t.Fatal("partial prune committed", rows, err)
		}
	})
	for _, table := range []string{"protocol_state", "protocol_state_locks"} {
		t.Run("missing "+table, func(t *testing.T) {
			ctx := t.Context()
			db := sqliteDB(t)
			s, err := statestore.Open(ctx, db, sqlite.Dialect)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "DROP TABLE "+table); err != nil {
				t.Fatal(err)
			}
			if table == "protocol_state" {
				if _, err := s.Prune(ctx, 10); err == nil {
					t.Fatal("missing data table concealed")
				}
			} else if err := s.Update(ctx, []string{"a"}, func(state.Tx) error { t.Error("ran without lock"); return nil }); err == nil {
				t.Fatal("missing locks concealed")
			}
		})
	}
}

// Run the same certificate publication and device-binding races on every backend.
func exerciseCertificateState(t *testing.T, a, b state.Store) {
	t.Helper()
	ctx := t.Context()
	assocs := []*accountdriven.Associations{{Store: a}, {Store: b}}
	record, err := assocs[0].Create(
		ctx,
		accountdriven.Identity{
			ManagedAppleAccount: "alice@example.com",
			Subject:             "alice",
			Issuer:              "idp",
		},
		accountdriven.VersionBYOD,
		"iPhone17,2",
	)
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int64
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			id := mdm.EnrollmentID{
				Channel: mdm.ChannelUserEnrollmentDevice,
				ID:      fmt.Sprintf("device-%d", i),
			}
			err := assocs[i%2].Bind(ctx, record.Reference, id, false)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, accountdriven.ErrAssociation) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("multiple device bindings", winners.Load())
	}
	bound, err := assocs[1].Get(ctx, record.Reference)
	if err != nil {
		t.Fatal(err)
	}
	if err := assocs[1].Bind(ctx, record.Reference, bound.Enrollment, true); err != nil {
		t.Fatal(err)
	}
	if err := assocs[0].Bind(ctx, record.Reference, bound.Enrollment, true); err != nil {
		t.Fatal("retry not idempotent", err)
	}
	root, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	issuer := revocation.Issuer{
		Certificate: root,
		Signer:      key,
		CRLTTL:      time.Hour,
		CRLRefresh:  time.Minute,
		OCSPTTL:     time.Minute,
	}
	r1, err := revocation.New(a, issuer)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := revocation.New(b, issuer)
	if err != nil {
		t.Fatal(err)
	}
	registries := []*revocation.Registry{r1, r2}
	id := cms.Fingerprint(root)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, root, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if err := r1.Register(ctx, id, leaf, revocation.Provenance{Source: "import"}); err != nil {
		t.Fatal(err)
	}
	first, err := r1.CRL(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		wg.Go(func() {
			der, err := registries[i%2].CRL(ctx, id)
			if err != nil || !bytes.Equal(first, der) {
				t.Error("replicas published conflicting CRLs", err)
			}
		})
	}
	wg.Wait()
	winners.Store(0)
	for i := range 12 {
		wg.Go(func() {
			err := registries[i%2].Revoke(ctx, id, leaf.SerialNumber, 1)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, revocation.ErrRevoked) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("multiple revocations", winners.Load())
	}
	for _, r := range registries {
		if err := r.Check(ctx, leaf); !errors.Is(err, revocation.ErrRevoked) {
			t.Fatal("replica missed revocation", err)
		}
		der, err := r.CRL(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		crl, err := x509.ParseRevocationList(der)
		if err != nil || crl.CheckSignatureFrom(root) != nil || crl.Number.Int64() != 2 ||
			len(crl.RevokedCertificateEntries) != 1 {
			t.Fatal(crl, err)
		}
	}
}
