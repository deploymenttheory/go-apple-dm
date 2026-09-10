package webauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/clock"
)

func TestKnownJWKSKeyExpiresAndFailsClosed(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	replacement, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	var phase atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if phase.Load() == 2 {
			w.WriteHeader(503)
			return
		}
		k, kid := key, "old"
		if phase.Load() == 1 {
			k, kid = replacement, "new"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(mustJSON(t, map[string]any{"keys": []map[string]any{ecJWK(t, kid, k)}}))
	}))
	defer srv.Close()
	clk := clock.NewFake(time.Now())
	f := &Flow{
		http:     srv.Client(),
		clock:    clk,
		maxBytes: 1 << 20,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if len(f.keysFor(t.Context(), srv.URL, "old", algES256)) != 1 {
		t.Fatal("initial key")
	}
	phase.Store(1)
	clk.Advance(16 * time.Minute)
	if len(f.keysFor(t.Context(), srv.URL, "old", algES256)) != 0 {
		t.Fatal("removed key retained")
	}
	if len(f.keysFor(t.Context(), srv.URL, "new", algES256)) != 1 {
		t.Fatal("new key unavailable")
	}
	phase.Store(2)
	clk.Advance(16 * time.Minute)
	if len(f.keysFor(t.Context(), srv.URL, "new", algES256)) != 0 {
		t.Fatal("expired key accepted during outage")
	}
}
