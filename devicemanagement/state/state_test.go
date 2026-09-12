package state_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func TestTransactions(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	st := state.NewMemory()
	st.Now = func() time.Time { return now }
	if _, err := st.Get(ctx, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	boom := errors.New("abort")
	err := st.Update(ctx, []string{"a"}, func(tx state.Tx) error {
		if !tx.Now().Equal(now) {
			t.Fatal(tx.Now())
		}
		if err := tx.Put(ctx, state.Record{Key: "a", Value: []byte("first")}); err != nil {
			return err
		}
		v, _ := tx.Get(ctx, "a")
		if string(v.Value) != "first" {
			t.Fatal(v)
		}
		v.Value[0] = 'X'
		rows, _ := tx.List(ctx, "a", "", 10)
		if string(rows[0].Value) != "first" {
			t.Fatal("aliased value")
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, "a"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("rollback", err)
	}
	if err := st.Update(ctx, []string{"a"}, func(tx state.Tx) error {
		for _, k := range []string{"a", "a%", "a_", "b"} {
			if err := tx.Put(ctx, state.Record{Key: k, Value: []byte(k), ExpiresAt: now.Add(-time.Minute)}); err != nil {
				return err
			}
		}
		if err := tx.Delete(ctx, "b"); err != nil {
			return err
		}
		if _, err := tx.Get(ctx, "b"); !errors.Is(err, state.ErrNotFound) {
			t.Fatal(err)
		}
		return tx.Put(ctx, state.Record{Key: "keep"})
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.List(ctx, "a", "a", 10)
	if err != nil || len(rows) != 2 || rows[0].Key != "a%" {
		t.Fatal(rows, err)
	}
	if n, err := st.Prune(ctx, 2); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if n, err := st.Prune(ctx, 2); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := st.Get(ctx, "keep"); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(ctx, []string{"counter"}, func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: "counter", Value: []byte("0")}) }); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			if err := st.Update(ctx, []string{"counter"}, func(tx state.Tx) error {
				v, err := tx.Get(ctx, "counter")
				if err != nil {
					return err
				}
				var n int
				_, _ = fmt.Sscan(string(v.Value), &n)
				return tx.Put(ctx, state.Record{Key: "counter", Value: fmt.Appendf(nil, "%d", n+1)})
			}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	v, _ := st.Get(ctx, "counter")
	if string(v.Value) != "40" {
		t.Fatal(string(v.Value))
	}
}

func TestInvalidAndCancelled(t *testing.T) {
	st := state.NewMemory()
	ctx := t.Context()
	for _, key := range []string{"", "a b", "\x00", "é", string(make([]byte, 256))} {
		if state.ValidKey(key) {
			t.Fatal(key)
		}
		if _, err := st.Get(ctx, key); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
		if err := st.Update(ctx, []string{key}, func(state.Tx) error { return nil }); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := st.Update(ctx, nil, func(state.Tx) error { return nil }); !errors.Is(err, state.ErrInvalid) {
		t.Fatal(err)
	}
	if err := st.Update(ctx, []string{"a"}, nil); !errors.Is(err, state.ErrInvalid) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 10001} {
		if _, err := st.List(ctx, "", "", limit); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := st.Prune(ctx, limit); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := st.Get(cancelled, "a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := st.List(cancelled, "", "", 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := st.Prune(cancelled, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := st.Update(cancelled, []string{"a"}, func(state.Tx) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := st.Update(ctx, []string{"a"}, func(tx state.Tx) error {
		for _, err := range []error{tx.Put(cancelled, state.Record{Key: "a"}), tx.Delete(cancelled, "a")} {
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		for _, err := range []error{tx.Put(ctx, state.Record{Key: ""}), tx.Delete(ctx, "")} {
			if !errors.Is(err, state.ErrInvalid) {
				t.Fatal(err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mid, cancelMid := context.WithCancel(ctx)
	if err := st.Update(mid, []string{"a"}, func(tx state.Tx) error { _ = tx.Put(mid, state.Record{Key: "a"}); cancelMid(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, "a"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
}
