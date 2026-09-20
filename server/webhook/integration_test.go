//go:build integration

package webhook

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

func TestSQLContracts(t *testing.T) {
	for _, backend := range []string{"postgres", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			var db *sql.DB
			var dialect sqlcommon.Dialect
			if backend == "postgres" {
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN not set")
				}
				cfg, err := pgx.ParseConfig(dsn)
				if err != nil {
					t.Fatal(err)
				}
				db = stdlib.OpenDB(*cfg)
				dialect = postgres.Dialect
			} else {
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN not set")
				}
				dsn, err := mysql.NormalizeDSN(dsn)
				if err != nil {
					t.Fatal(err)
				}
				db, err = sql.Open("mysql", dsn)
				if err != nil {
					t.Fatal(err)
				}
				dialect = mysql.Dialect
			}
			defer func() { _ = db.Close() }()
			keys, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "test", Strict: true}, Provider: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}})
			if err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"webhook_replays", "webhook_messages", "webhook_subscriptions", "webhook_schema_migrations"} {
				if _, err := db.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table); err != nil {
					t.Fatal(err)
				}
			}
			outbox, err := eventstore.Open(t.Context(), db, dialect)
			if err != nil {
				t.Fatal(err)
			}
			s, err := Open(t.Context(), db, dialect, keys, outbox, Config{})
			if err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"webhook_replays", "webhook_messages", "webhook_subscriptions", "event_deliveries", "event_records"} {
				if _, err := db.ExecContext(t.Context(), "DELETE FROM "+table); err != nil { // #nosec G202 -- table comes only from the fixed test reset list above.
					t.Fatal(err)
				}
			}
			c := subscribe(t, s, PayloadPolicy{FullJSON: true, RawRequest: true})
			rollback := errors.New("rollback")
			if err := outbox.Run(t.Context(), func(ctx context.Context) error {
				if err := s.Capture(ctx, occurrence()); err != nil {
					return err
				}
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			if len(deliveries(t, s, "")) != 0 {
				t.Fatal("rolled back capture persisted")
			}
			if err := s.Capture(t.Context(), occurrence()); err != nil {
				t.Fatal(err)
			}
			// Competing replicas can claim a delivery only once while the lease is live.
			var wg sync.WaitGroup
			claims := make(chan eventstore.Delivery, 2)
			failures := make(chan error, 2)
			for range 2 {
				wg.Go(func() {
					d, err := outbox.Claim(t.Context(), time.Minute)
					if err == nil {
						claims <- d
					} else {
						failures <- err
					}
				})
			}
			wg.Wait()
			close(claims)
			close(failures)
			if len(claims) != 1 {
				t.Fatal("concurrent claim count", len(claims))
			}
			for err := range failures {
				if !errors.Is(err, eventstore.ErrEmpty) {
					t.Fatal(err)
				}
			}
			d := <-claims
			if err := outbox.Finish(t.Context(), d, "http-rejected", 0); err != nil {
				t.Fatal(err)
			}
			if err := s.Retry(t.Context(), d.Record.EventID, true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Update(t.Context(), c.Subscription.ID, 1, c.Subscription.Spec, true); err != nil {
				t.Fatal(err)
			}
			if got := deliveries(t, s, "")[0].State; got != "paused" {
				t.Fatal(got)
			}
			req := ReplayRequest{SubscriptionID: c.Subscription.ID, Key: "sql-replay"}
			r, err := s.Replay(t.Context(), req, true)
			if err != nil || len(r.DeliveryIDs) != 1 {
				t.Fatal(r, err)
			}
			if again, err := s.Replay(t.Context(), req, true); err != nil || again.DeliveryIDs[0] != r.DeliveryIDs[0] {
				t.Fatal(again, err)
			}
			if _, err := s.Rewrap(t.Context()); err != nil {
				t.Fatal(err)
			}
			s.cfg.Now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
			if err := s.Prune(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(deliveries(t, s, "")) != 0 {
				t.Fatal("retention failed")
			}
		})
	}
}
