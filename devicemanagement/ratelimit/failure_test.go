package ratelimit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/ratelimit"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

var errStorage = errors.New("storage fault")

type faultyStore struct {
	state.Store
	op string
}

func (s faultyStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(faultyTx{Tx: tx, op: s.op}) })
}

type faultyTx struct {
	state.Tx
	op string
}

func (t faultyTx) Get(ctx context.Context, key string) (state.Record, error) {
	if t.op == "get" {
		return state.Record{}, errStorage
	}
	if t.op == "corrupt" {
		return state.Record{Key: key, Value: []byte("broken")}, nil
	}
	return t.Tx.Get(ctx, key)
}
func (t faultyTx) List(ctx context.Context, prefix, after string, n int) ([]state.Record, error) {
	if t.op == "list" {
		return nil, errStorage
	}
	return t.Tx.List(ctx, prefix, after, n)
}
func (t faultyTx) Delete(ctx context.Context, key string) error {
	if t.op == "delete" {
		return errStorage
	}
	return t.Tx.Delete(ctx, key)
}
func (t faultyTx) Put(ctx context.Context, r state.Record) error {
	if t.op == "put" {
		return errStorage
	}
	return t.Tx.Put(ctx, r)
}

func TestStorageFailuresNeverAdmitUnaccountedTraffic(t *testing.T) {
	for _, op := range []string{"get", "corrupt", "list", "delete", "put"} {
		t.Run(op, func(t *testing.T) {
			ctx := t.Context()
			now := time.Now()
			st := state.NewMemory()
			st.Now = func() time.Time { return now }
			lim := &ratelimit.Limiter{Store: st}
			old := []ratelimit.Bucket{{Key: "old", Interval: time.Second, Burst: 1}}
			if _, err := lim.Check(ctx, old); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Hour)
			lim.Store = faultyStore{Store: st, op: op}
			fresh := []ratelimit.Bucket{{Key: "new", Interval: time.Hour, Burst: 1}}
			d, err := lim.Check(ctx, fresh)
			if d.Allowed || !errors.Is(err, ratelimit.ErrUnavailable) {
				t.Fatal(d, err)
			}
			rows, err := st.List(ctx, "rate/", "", 100)
			if err != nil || len(rows) != 1 {
				t.Fatal("failure changed state", rows, err)
			}
			lim.Store = st
			if d, err := lim.Check(ctx, fresh); err != nil || !d.Allowed {
				t.Fatal("failed attempt consumed quota", d, err)
			}
		})
	}
}
