package ratelimit_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ratelimit"
	"github.com/deploymenttheory/go-apple-dm/state"
)

func TestAtomicQuotas(t *testing.T) {
	ctx := t.Context()
	now := time.Now().Truncate(time.Microsecond)
	st := state.NewMemory()
	st.Now = func() time.Time { return now }
	l := &ratelimit.Limiter{Store: st}
	b := []ratelimit.Bucket{{Key: "token-secret-never-stored", Interval: time.Second, Burst: 3}}
	for range 3 {
		d, err := l.Check(ctx, b)
		if err != nil || !d.Allowed {
			t.Fatal(d, err)
		}
	}
	d, err := l.Check(ctx, b)
	if err != nil || d.Allowed || d.RetryAfter != time.Second {
		t.Fatal(d, err)
	}
	now = now.Add(500 * time.Millisecond)
	d, _ = l.Check(ctx, b)
	if d.RetryAfter != 500*time.Millisecond {
		t.Fatal(d)
	}
	// A rejected combined request must not consume a fresh bucket.
	other := ratelimit.Bucket{Key: "other", Interval: time.Hour, Burst: 1}
	d, err = l.Check(ctx, append(b, other))
	if err != nil || d.Allowed {
		t.Fatal(d, err)
	}
	d, err = l.Check(ctx, []ratelimit.Bucket{other})
	if err != nil || !d.Allowed {
		t.Fatal("partial debit", d, err)
	}
	rows, _ := st.List(ctx, "rate/", "", 100)
	for _, r := range rows {
		if strings.Contains(r.Key, "token-secret") || strings.Contains(string(r.Value), "token-secret") {
			t.Fatal("raw key persisted")
		}
	}
	now = now.Add(time.Second)
	d, _ = l.Check(ctx, b)
	if !d.Allowed {
		t.Fatal(d)
	}
}

func TestConcurrentCapacityAndFailures(t *testing.T) {
	ctx := t.Context()
	now := time.Now().Truncate(time.Microsecond)
	st := state.NewMemory()
	st.Now = func() time.Time { return now }
	l := &ratelimit.Limiter{Store: st, MaxEntries: 1}
	b := []ratelimit.Bucket{{Key: "one", Interval: time.Hour, Burst: 8}}
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			d, err := l.Check(ctx, b)
			if err != nil {
				t.Error(err)
			}
			if d.Allowed {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 8 {
		t.Fatal(admitted.Load())
	}
	if _, err := l.Check(ctx, []ratelimit.Bucket{{Key: "two", Interval: time.Second, Burst: 1}}); !errors.Is(err, ratelimit.ErrCapacity) || !errors.Is(err, ratelimit.ErrUnavailable) {
		t.Fatal(err)
	}
	now = now.Add(9 * time.Hour)
	d, err := l.Check(ctx, []ratelimit.Bucket{{Key: "two", Interval: time.Second, Burst: 1}})
	if err != nil || !d.Allowed {
		t.Fatal(d, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := l.Check(cancelled, b); !errors.Is(err, ratelimit.ErrUnavailable) {
		t.Fatal(err)
	}
	for _, buckets := range [][]ratelimit.Bucket{nil, {{Key: ""}}, {{Key: "a", Interval: 1, Burst: 1}}, {{Key: "a", Interval: 48 * time.Hour, Burst: 1}}, {{Key: "a", Interval: time.Second, Burst: 0}}, {{Key: "a", Interval: 24 * time.Hour, Burst: 100}}, append(b, b...)} {
		if _, err := l.Check(ctx, buckets); !errors.Is(err, ratelimit.ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, bad := range []*ratelimit.Limiter{{}, {Store: st, MaxEntries: 10001}, {Store: st, MaxEntries: -1}} {
		if _, err := bad.Check(ctx, b); !errors.Is(err, ratelimit.ErrInvalid) {
			t.Fatal(err)
		}
	}
}

func TestHTTPAndProxyTrust(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, tc := range []struct{ peer, forward, want string }{
		{"192.0.2.1:1", "1.1.1.1", "192.0.2.1"}, {"10.0.0.1:1", "8.8.8.8, 192.0.2.5, 10.0.0.2", "192.0.2.5"},
		{"10.0.0.1:1", "garbage", "10.0.0.1"}, {"bad", "", "unknown"}, {"::ffff:192.0.2.1", "", "192.0.2.1"},
		{"10.0.0.1:1", strings.Repeat("1.1.1.1,", 40), "10.0.0.1"}, {"10.0.0.1:1", "10.0.0.2", "10.0.0.2"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.forward)
		if got := ratelimit.PeerKey(r, trusted); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	st := state.NewMemory()
	cfg := ratelimit.HTTPConfig{Limiter: &ratelimit.Limiter{Store: st}, Buckets: func(r *http.Request) []ratelimit.Bucket {
		if r.URL.Path == "/healthz" {
			return nil
		}
		return []ratelimit.Bucket{{Key: "a", Interval: time.Hour, Burst: 1}}
	}}
	h := ratelimit.Middleware(cfg, next)
	for _, want := range []int{204, 429} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != want {
			t.Fatal(w.Code)
		}
		if want == 429 && w.Header().Get("Retry-After") == "" {
			t.Fatal("missing retry")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	for _, empty := range []ratelimit.HTTPConfig{{}, {Limiter: cfg.Limiter}} {
		w := httptest.NewRecorder()
		ratelimit.Middleware(empty, next).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 204 {
			t.Fatal(w.Code)
		}
	}
	cfg.Limiter = &ratelimit.Limiter{}
	cfg.Reject = func(w http.ResponseWriter, r *http.Request, status int) { w.WriteHeader(status) }
	w = httptest.NewRecorder()
	ratelimit.Middleware(cfg, next).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
