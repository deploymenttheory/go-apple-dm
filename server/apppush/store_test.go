package apppush_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/apppush"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
	"github.com/deploymenttheory/go-apple-dm/state"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/testpki"
)

func TestCredentialContract(t *testing.T) {
	ctx := context.Background()
	for _, backend := range []string{"inmem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var st state.Store = state.NewMemory()
			if backend == "sqlite" {
				db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"), sqlite.Options{})
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				st, err = statestore.Open(ctx, db.DB(), sqlite.Dialect)
				if err != nil {
					t.Fatal(err)
				}
			}
			ring, err := crypt.NewKeyring(
				ctx,
				crypt.Options{
					Keys: crypt.Keys{Active: "new", Accepted: []string{"old"}},
					Provider: secrets.Static{
						"new": bytes.Repeat([]byte{1}, 32),
						"old": bytes.Repeat([]byte{2}, 32),
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			s := &apppush.Store{State: st, Keys: ring}
			ca, err := testpki.NewCA("provider")
			if err != nil {
				t.Fatal(err)
			}
			identity, err := ca.IssueApp("com.example.app", time.Now().Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			cert, key, err := identity.PEM()
			if err != nil {
				t.Fatal(err)
			}
			first, err := s.Put(ctx, "", cert, key)
			if err != nil {
				t.Fatal(err)
			}
			if first.Version != 1 {
				t.Fatal("initial version")
			}
			raw, err := st.List(ctx, "apppush/", "", 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) != 1 || !crypt.IsSealed(raw[0].Value) || bytes.Contains(raw[0].Value, key) {
				t.Fatal("identity not sealed")
			}
			if _, err = s.Put(ctx, "com.other", cert, key); !errors.Is(err, state.ErrInvalid) {
				t.Fatalf("topic mismatch: %v", err)
			}
			mdm, err := ca.IssuePush(
				"com.apple.mgmt.External.example",
				time.Now().Add(-time.Minute),
			)
			if err != nil {
				t.Fatal(err)
			}
			mc, mk, err := mdm.PEM()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Put(ctx, "", mc, mk); !errors.Is(err, state.ErrInvalid) {
				t.Fatalf("MDM accepted: %v", err)
			}
			fresh, err := ca.IssueApp(first.Topic, time.Now().Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			fc, fk, err := fresh.PEM()
			if err != nil {
				t.Fatal(err)
			}
			second, err := s.Put(ctx, "", fc, fk)
			if err != nil || second.Version != 2 {
				t.Fatalf("renewal: %+v %v", second, err)
			}
			reopened := &apppush.Store{State: st, Keys: ring}
			pair, err := reopened.PushCertificate(ctx, first.Topic)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(pair.Certificate[0], fresh.Cert.Raw) {
				t.Fatal("stale credential after reopening")
			}
			rows, next, err := s.List(ctx, "", 1)
			if err != nil || len(rows) != 1 || next != "" {
				t.Fatalf("list: %+v %s %v", rows, next, err)
			}
		})
	}
}

func TestCredentialIntegrityAndPaging(t *testing.T) {
	ctx := t.Context()
	st := state.NewMemory()
	ring, err := crypt.NewKeyring(ctx, crypt.Options{
		Keys:     crypt.Keys{Active: "active"},
		Provider: secrets.Static{"active": bytes.Repeat([]byte{7}, 32)},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &apppush.Store{State: st, Keys: ring}
	ca, err := testpki.NewCA("app credentials")
	if err != nil {
		t.Fatal(err)
	}
	var cert, key []byte
	for _, topic := range []string{"com.example.a", "com.example.b"} {
		id, err := ca.IssueApp(topic, time.Now().Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		cert, key, err = id.PEM()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put(ctx, topic, cert, key); err != nil {
			t.Fatal(err)
		}
	}
	page, cursor, err := s.List(ctx, "", 1)
	if err != nil || len(page) != 1 || page[0].Topic != "com.example.a" || cursor == "" {
		t.Fatalf("first page: %+v %s %v", page, cursor, err)
	}
	page, cursor, err = s.List(ctx, cursor, 1)
	if err != nil || len(page) != 1 || page[0].Topic != "com.example.b" || cursor != "" {
		t.Fatalf("second page: %+v %s %v", page, cursor, err)
	}
	for _, limit := range []int{-1, 0, 1001} {
		if _, _, err := s.List(ctx, "", limit); !errors.Is(err, state.ErrInvalid) {
			t.Fatal("invalid page limit accepted")
		}
	}
	for _, topic := range []string{"", "bad topic"} {
		if _, err := s.PushCertificate(ctx, topic); !errors.Is(err, state.ErrInvalid) {
			t.Fatal("invalid topic accepted")
		}
	}
	if _, err := s.PushCertificate(ctx, "com.missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("missing identity accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := s.List(canceled, "", 1); !errors.Is(err, context.Canceled) {
		t.Fatal("list ignored cancellation")
	}
	if _, err := s.Put(canceled, "", cert, key); !errors.Is(err, context.Canceled) {
		t.Fatal("put ignored cancellation")
	}
	// Moving encrypted bytes to another topic must fail AAD authentication.
	rows, err := st.List(ctx, "apppush/", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	moved := rows[0]
	moved.Key = "apppush/v1/com.example.b"
	if err := st.Update(
		ctx,
		[]string{moved.Key},
		func(tx state.Tx) error { return tx.Put(ctx, moved) },
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PushCertificate(ctx, "com.example.b"); err == nil {
		t.Fatal("moved ciphertext accepted")
	}
	if _, _, err := s.List(ctx, "", 10); err == nil {
		t.Fatal("tampering hidden by metadata listing")
	}
	if _, err := s.Put(ctx, "com.example.b", cert, key); err == nil {
		t.Fatal("renewal silently overwrote unauthenticated state")
	}
	// Memory mode may deliberately use unencrypted transient state.
	plain := &apppush.Store{State: state.NewMemory()}
	if _, err := plain.Put(ctx, "", cert, key); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.PushCertificate(ctx, "com.example.b"); err != nil {
		t.Fatal(err)
	}
}
