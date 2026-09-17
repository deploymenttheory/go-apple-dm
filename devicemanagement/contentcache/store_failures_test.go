package contentcache_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/contentcache"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

type brokenCacheState struct {
	state.Store
	err  error
	mode string
}

func (s brokenCacheState) Get(context.Context, string) (state.Record, error) {
	return state.Record{}, s.err
}

func (s brokenCacheState) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	if s.mode == "update" {
		return s.err
	}
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(brokenCacheTx{Tx: tx, err: s.err, mode: s.mode}) })
}

type brokenCacheTx struct {
	state.Tx
	err  error
	mode string
}

func (t brokenCacheTx) Get(ctx context.Context, key string) (state.Record, error) {
	if t.mode == "get" {
		return state.Record{}, t.err
	}
	return t.Tx.Get(ctx, key)
}

func (t brokenCacheTx) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	if t.mode == "list" {
		return nil, t.err
	}
	return t.Tx.List(ctx, prefix, after, limit)
}

func testCacheReport() *contentcache.Report {
	return &contentcache.Report{Version: new(int64(1)), ReportDate: new("2026-09-16T12:00:00Z"), Hostname: new("test"), Hardware: new("Mac16,1"), ServerGUID: new("13D4D110-B2B7-4F26-8E25-CD22E58C00EE")}
}

func TestReportStoreFailures(t *testing.T) {
	memory := state.NewMemory()
	store := &contentcache.StateStore{State: memory}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "failures"}
	token, err := store.RotateCredential(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("storage unavailable")
	broken := &contentcache.StateStore{State: brokenCacheState{Store: memory, mode: "update", err: boom}}
	if _, err := broken.RotateCredential(t.Context(), id); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := broken.Authenticate(t.Context(), token); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	for _, mode := range []string{"get", "list"} {
		broken.State = brokenCacheState{Store: memory, mode: mode, err: boom}
		if mode == "get" {
			if err := broken.Accept(t.Context(), token, testCacheReport()); !errors.Is(err, boom) {
				t.Fatal(err)
			}
		} else {
			if _, err := broken.Reports(t.Context(), id, paging.Page{}); !errors.Is(err, boom) {
				t.Fatal(err)
			}
		}
	}
	if err := store.Accept(t.Context(), "bad", testCacheReport()); !errors.Is(err, contentcache.ErrCredential) {
		t.Fatal(err)
	}
	for _, invalid := range []mdm.EnrollmentID{{}, {Channel: mdm.ChannelUser, ID: "user", ParentID: "device"}} {
		if err := store.RevokeCredential(t.Context(), invalid); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := store.Reports(t.Context(), invalid, paging.Page{}); !errors.Is(err, state.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := store.Authenticate(t.Context(), strings.Repeat("a", 64)+"."+strings.Repeat("!", 43)); !errors.Is(err, contentcache.ErrCredential) {
		t.Fatal(err)
	}
	key, _, _ := strings.Cut(token, ".")
	for _, value := range []string{`{`, `{"Enrollment":{"Channel":"device","ID":"other"},"Digest":"AA=="}`} {
		err := memory.Update(t.Context(), []string{"contentcache/v1/credential/" + key}, func(tx state.Tx) error {
			return tx.Put(t.Context(), state.Record{Key: "contentcache/v1/credential/" + key, Value: []byte(value)})
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Authenticate(t.Context(), token); !errors.Is(err, contentcache.ErrCredential) {
			t.Fatal(err)
		}
	}
}

func TestReportPagingPastExpiredRecordsAndCorruption(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	memory := state.NewMemory()
	memory.Now = func() time.Time { return now }
	store := &contentcache.StateStore{State: memory, Retention: 24 * time.Hour}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "expiry"}
	token, err := store.RotateCredential(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(t.Context(), token, testCacheReport()); err != nil {
		t.Fatal(err)
	}
	store.Retention = time.Second
	for range 6 {
		now = now.Add(time.Second)
		if err := store.Accept(t.Context(), token, testCacheReport()); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Second)
	reports, err := store.Reports(t.Context(), id, paging.Page{Limit: 1})
	if err != nil || len(reports.Items) != 1 || reports.NextCursor != "" {
		t.Fatalf("expired prefix hid live report: %+v %v", reports, err)
	}
	rows, err := memory.List(t.Context(), "contentcache/v1/reports/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`{`, `{"Enrollment":{"Channel":"device","ID":"other"}}`} {
		record := rows[len(rows)-1]
		record.Value = []byte(value)
		if err := memory.Update(t.Context(), []string{record.Key}, func(tx state.Tx) error { return tx.Put(t.Context(), record) }); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Reports(t.Context(), id, paging.Page{}); err == nil {
			t.Fatal("corrupt report accepted")
		}
	}
}
