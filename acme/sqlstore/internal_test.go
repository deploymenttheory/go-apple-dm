package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/storage/sqlite"
)

// t0 is a fixed instant every fixture is dated from.
var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// openWithoutReturning is a store on SQLite that takes nonces the way
// MySQL has to, with a read and a delete rather than DELETE ... RETURNING.
// The engine underneath is not the one the path exists for, but the path
// is the store's own code and the unit run is where it is held to the
// contract; the integration suite then runs the same assertions on MySQL
// itself.
func openWithoutReturning(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "acme.db"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := Open(context.Background(), db, sqlite.Dialect, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.mysql = true
	return s
}

// TestTakeNonceWithoutReturning holds the no-RETURNING path to the same
// rule as the one-statement path: single use, and exactly one winner.
func TestTakeNonceWithoutReturning(t *testing.T) {
	ctx := context.Background()
	s := openWithoutReturning(t)
	if err := s.PutNonce(ctx, acme.Nonce{Value: "n1", IssuedAt: t0}); err != nil {
		t.Fatal(err)
	}
	n, err := s.TakeNonce(ctx, "n1")
	if err != nil || n.Value != "n1" || !n.IssuedAt.Equal(t0) {
		t.Fatalf("take = %+v %v", n, err)
	}
	if _, err := s.TakeNonce(ctx, "n1"); !errors.Is(err, acme.ErrNotFound) {
		t.Fatalf("replay = %v, want ErrNotFound", err)
	}
	if _, err := s.TakeNonce(ctx, ""); !errors.Is(err, acme.ErrInvalid) {
		t.Fatalf("empty value = %v, want ErrInvalid", err)
	}
	if err := s.PutNonce(ctx, acme.Nonce{Value: "n2"}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.TakeNonce(ctx, "n2"); err != nil || !n.IssuedAt.IsZero() {
		t.Fatalf("undated nonce = %+v %v", n, err)
	}
	for i := range 10 {
		value := "race" + string(rune('a'+i))
		if err := s.PutNonce(ctx, acme.Nonce{Value: value, IssuedAt: t0}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := s.TakeNonce(ctx, value); err == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("%s: %d takers won, want 1", value, wins)
		}
	}
	// A driver failure on the locked path is reported, not read as a miss.
	if err := s.PutNonce(ctx, acme.Nonce{Value: "n3", IssuedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TakeNonce(ctx, "n3"); err == nil || errors.Is(err, acme.ErrNotFound) {
		t.Fatalf("take on a closed database = %v, want a driver error", err)
	}
}

// TestEncodeRefusesUnmarshalableRecords: a record JSON cannot render is
// ErrInvalid rather than a row of nothing. No acme record can reach this,
// which is why it is proved here rather than through a Put.
func TestEncodeRefusesUnmarshalableRecords(t *testing.T) {
	if _, err := encode("challenge", make(chan int)); !errors.Is(err, acme.ErrInvalid) {
		t.Fatalf("encode of a channel = %v, want ErrInvalid", err)
	}
}

// failingResult is a sql.Result whose row count cannot be read, which is
// what a driver reports when it does not track one.
type failingResult struct{}

var errNoRowCount = errors.New("no row count")

func (failingResult) LastInsertId() (int64, error) { return 0, errNoRowCount }
func (failingResult) RowsAffected() (int64, error) { return 0, errNoRowCount }

// TestAffectedSurfacesAnUncountableResult: a statement whose row count
// cannot be read is an error, because the count is what decides whether a
// nonce was taken or a record pruned.
func TestAffectedSurfacesAnUncountableResult(t *testing.T) {
	if _, err := affected("take nonce", failingResult{}); !errors.Is(err, errNoRowCount) {
		t.Fatalf("affected = %v, want the driver's error", err)
	}
}
