package axm

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
)

const (
	testClientID = "BUSINESSAPI.c75c0a8a-a026-4dae-99aa-89ea1e1103e5"
	testKeyID    = "e339d085-a821-438a-a527-d044edacf50a"
)

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func sec1PEM(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func pkcs8PEM(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// instantClock reports the wall clock but never waits: After fires at
// once and records the requested duration, so retry and polling delays
// can be asserted without sleeping.
type instantClock struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (c *instantClock) Now() time.Time                  { return time.Now() }
func (c *instantClock) Since(t time.Time) time.Duration { return time.Since(t) }
func (c *instantClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.delays = append(c.delays, d)
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
}

func (c *instantClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}

var _ clock.Clock = (*instantClock)(nil)

// fixture is a fake server with one registered account and a client.
type fixture struct {
	srv   *axmtest.Server
	key   *ecdsa.PrivateKey
	clock *instantClock
	cfg   Config
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	srv := axmtest.NewServer()
	t.Cleanup(srv.Close)
	key := newKey(t)
	srv.RegisterKey(testClientID, testKeyID, &key.PublicKey)
	ic := &instantClock{}
	return &fixture{srv: srv, key: key, clock: ic, cfg: Config{
		ClientID: testClientID, KeyID: testKeyID, PrivateKey: key,
		BaseURL: srv.URL, TokenURL: srv.TokenURL, HTTPClient: srv.Client(), Clock: ic,
		Retry: Retry{Max: 3, Base: time.Millisecond, Cap: 4 * time.Millisecond},
	}}
}

// client builds a client, applying mod to the config first.
func (f *fixture) client(t *testing.T, mod func(*Config)) *Client {
	t.Helper()
	cfg := f.cfg
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// stub is a minimal server: the token endpoint always succeeds and every
// other path goes to handler.
func stub(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"stub-token","token_type":"Bearer","expires_in":3600,"scope":"business.api"}`))
	})
	mux.HandleFunc("/", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// stubClient builds a client against a stub server.
func stubClient(t *testing.T, srv *httptest.Server, mod func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		ClientID: testClientID, KeyID: testKeyID, PrivateKey: newKey(t),
		BaseURL: srv.URL, TokenURL: srv.URL + "/auth/oauth2/token", HTTPClient: srv.Client(), Clock: &instantClock{},
		Retry: Retry{Max: 0, Base: time.Millisecond, Cap: time.Millisecond},
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// decodeBody decodes a recorded request body.
func decodeBody(t *testing.T, r axmtest.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("body %q: %v", r.Body, err)
	}
	return m
}

// dig walks a decoded JSON object by keys.
func dig(t *testing.T, m any, keys ...string) any {
	t.Helper()
	cur := m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("dig %v: %T is not an object", keys, cur)
		}
		cur, ok = obj[k]
		if !ok {
			t.Fatalf("dig %v: missing %q", keys, k)
		}
	}
	return cur
}

// apiRequests returns the recorded non-token requests.
func apiRequests(srv *axmtest.Server) []axmtest.Request {
	var out []axmtest.Request
	for _, r := range srv.Requests() {
		if r.Path != axmtest.TokenPath {
			out = append(out, r)
		}
	}
	return out
}
