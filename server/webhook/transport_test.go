package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
)

// TestStandardWebhooksSignatures checks Standard Webhooks signature construction and verification.
func TestStandardWebhooksSignatures(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	secret := "whsec_" + base64.StdEncoding.EncodeToString(key)
	at := time.Unix(1800446400, 0)
	body := []byte(`{"type":"webhook.test"}`)
	id := "delivery-example"
	sig, err := Sign(secret, id, at, body)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id + ".1800446400."))
	_, _ = mac.Write(body)
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if sig != want {
		t.Fatal(sig, want)
	}
	h := http.Header{}
	h.Set(HeaderID, id)
	h.Set(HeaderTimestamp, strconv.FormatInt(at.Unix(), 10))
	h.Set(HeaderSignature, sig)
	if err := Verify(secret, h, body, at); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(http.Header){
		func(h http.Header) { h.Set(HeaderID, "tampered") },
		func(h http.Header) { h.Del(HeaderTimestamp) },
		func(h http.Header) { h.Set(HeaderTimestamp, "invalid") },
		func(h http.Header) { h.Set(HeaderTimestamp, "0") },
		func(h http.Header) { h.Add(HeaderSignature, sig) },
		func(h http.Header) { h.Set(HeaderSignature, "v1,invalid") },
		func(h http.Header) { h.Set(HeaderID, strings.Repeat("x", 65)) },
	} {
		copy := h.Clone()
		mutate(copy)
		if err := Verify(secret, copy, body, at); !errors.Is(err, ErrForbidden) {
			t.Fatal(err)
		}
	}
	if err := Verify(secret, h, []byte(`{}`), at); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	for _, delta := range []time.Duration{-6 * time.Minute, 6 * time.Minute} {
		if err := Verify(secret, h, body, at.Add(delta)); !errors.Is(err, ErrForbidden) {
			t.Fatal(err)
		}
	}
	if _, err := Sign("bad", id, at, body); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := Verify("bad", h, body, at); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// TestTransportRetriesRotationAndRestart checks transport retries rotation and restart.
func TestTransportRetriesRotationAndRestart(t *testing.T) {
	var mu sync.Mutex
	status := 503
	var bodies [][]byte
	var headers []http.Header
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, b)
		headers = append(headers, r.Header.Clone())
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "receiver-private-error")
	}))
	defer receiver.Close()
	now := time.Now().UTC()
	s := testStore(t, Config{Client: receiver.Client(), PrivateNetworks: []string{"127.0.0.0/8"}, Now: func() time.Time { return now }})
	c, err := s.Create(t.Context(), Spec{Name: "receiver", URL: receiver.URL, Events: []string{"*"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Capture(t.Context(), occurrence()); err != nil {
		t.Fatal(err)
	}
	d := deliveries(t, s, c.Subscription.ID)[0]
	w := &eventstore.Worker{Store: s.outbox, Resolve: s.Resolve}
	if err := w.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := deliveries(t, s, c.Subscription.ID)[0]
	if state.LastCode != "http-5xx" || state.State != "pending" || state.NextAttempt.Before(now.Add(119*time.Second)) {
		t.Fatal(state)
	}
	change, err := s.Rotate(t.Context(), c.Subscription.ID, time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	status = 204
	mu.Unlock()
	if err := s.Retry(t.Context(), d.ID, true); err != nil {
		t.Fatal(err)
	}
	// New store and worker instances consume the persisted body and keys.
	reopened, err := Open(t.Context(), s.db, s.d, s.keys, s.outbox, s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	w = &eventstore.Worker{Store: s.outbox, Resolve: reopened.Resolve}
	if err := w.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) || headers[0].Get(HeaderID) != headers[1].Get(HeaderID) {
		t.Fatal("retry mutated occurrence")
	}
	for _, secret := range []string{c.Credentials.SigningSecret, change.Credentials.SigningSecret} {
		if err := Verify(secret, headers[1], bodies[1], now); err != nil {
			t.Fatal(err)
		}
	}
	mu.Unlock()
	if got := deliveries(t, s, c.Subscription.ID)[0]; got.State != "delivered" || got.Attempts != 2 {
		t.Fatal(got)
	}
	if err := s.Retry(t.Context(), d.ID, true); !errors.Is(err, eventstore.ErrLease) {
		t.Fatal(err)
	}
	if err := w.Step(t.Context()); !errors.Is(err, eventstore.ErrEmpty) {
		t.Fatal(err)
	}
	mu.Lock()
	status = 302
	mu.Unlock()
	err = s.Send(t.Context(), d.ID)
	var httpErr *eventsink.HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 302 || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	mu.Lock()
	status = 200
	mu.Unlock()
	if err := s.Send(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	last := len(headers) - 1
	if len(strings.Fields(headers[last].Get(HeaderSignature))) != 1 {
		t.Fatal("expired signing key retained")
	}
	mu.Unlock()
}

// TestTransportPolicyAndStorageFailure checks transport policy and storage failure.
func TestTransportPolicyAndStorageFailure(t *testing.T) {
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer receiver.Close()
	for _, cfg := range []Config{{Client: receiver.Client()}, {PrivateNetworks: []string{"127.0.0.0/8"}}} {
		s := testStore(t, cfg)
		c, err := s.Create(t.Context(), Spec{Name: "denied", URL: receiver.URL, Events: []string{"*"}}, true)
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.Test(t.Context(), c.Subscription.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Send(t.Context(), id); err == nil || strings.Contains(err.Error(), receiver.URL) {
			t.Fatal("private/trust policy", err)
		}
	}
	s := testStore(t, Config{})
	c := subscribe(t, s, PayloadPolicy{})
	id, err := s.Test(t.Context(), c.Subscription.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE webhook_messages SET payload = ? WHERE delivery_id = ?", []byte("corrupt"), id); err != nil {
		t.Fatal(err)
	}
	w := eventstore.Worker{Store: s.outbox, Resolve: s.Resolve}
	var source *eventstore.SourceError
	if err := w.Step(t.Context()); !errors.As(err, &source) {
		t.Fatal("database failure treated as transport retry", err)
	}
	if sender, err := s.Resolve(t.Context(), "retired-webhook"); sender != nil || err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.RunRetention(ctx); err == nil {
		t.Fatal("cancel ignored")
	}
}

// TestTransportRedirectTimeoutAndRetryDates checks transport redirect timeout and retry dates.
func TestTransportRedirectTimeoutAndRetryDates(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	now := time.Now().UTC().Truncate(time.Second)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		case "/date":
			w.Header().Set("Retry-After", now.Add(time.Hour).Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
		case "/capped":
			w.Header().Set("Retry-After", "9999999999")
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/timeout":
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		}
	}))
	defer receiver.Close()
	s := testStore(t, Config{Client: receiver.Client(), PrivateNetworks: []string{"127.0.0.0/8"}, Now: func() time.Time { return now }})
	for _, tc := range []struct {
		path  string
		code  int
		delay time.Duration
	}{{"/redirect", 307, 0}, {"/date", 429, time.Hour}, {"/capped", 503, 24 * time.Hour}, {"/timeout", 0, 0}} {
		c, err := s.Create(t.Context(), Spec{Name: tc.path, URL: receiver.URL + tc.path, Events: []string{"*"}}, true)
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.Test(t.Context(), c.Subscription.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		err = s.Send(ctx, id)
		cancel()
		if tc.code == 0 {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			continue
		}
		var httpErr *eventsink.HTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != tc.code || httpErr.RetryAfter != tc.delay {
			t.Fatal(tc.path, err)
		}
	}
	if redirected.Load() {
		t.Fatal("followed a receiver redirect")
	}
	if err := s.Send(t.Context(), "metadata-already-pruned"); !errors.Is(err, eventstore.ErrExpired) {
		t.Fatal(err)
	}
}

// TestTransportInvalidPersistentCredentialsStopDelivery checks transport invalid persistent
// credentials stop delivery.
func TestTransportInvalidPersistentCredentialsStopDelivery(t *testing.T) {
	for _, mutate := range []func(*storedSubscription){
		func(s *storedSubscription) { s.Key = "corrupt" },
		func(s *storedSubscription) { s.PreviousKey = "corrupt"; s.PreviousUntil = time.Now().Add(time.Hour) },
		func(s *storedSubscription) { s.URL = "%invalid" },
	} {
		s := testStore(t, Config{})
		c := subscribe(t, s, PayloadPolicy{})
		id, err := s.Test(t.Context(), c.Subscription.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		sub, err := s.load(t.Context(), c.Subscription.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&sub)
		if err := s.save(t.Context(), sub, false); err != nil {
			t.Fatal(err)
		}
		var source *eventstore.SourceError
		if err := s.Send(t.Context(), id); !errors.As(err, &source) {
			t.Fatal("invalid persistent data was retried", err)
		}
	}
}

// TestDialPolicyFailures checks guarded webhook dialing, DNS failures, and redaction of failed
// connection addresses.
func TestDialPolicyFailures(t *testing.T) {
	client, err := newHTTPClient(Config{PrivateNetworks: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("guarded transport missing")
	}
	if _, err := transport.DialContext(t.Context(), "tcp", "missing-port"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := transport.DialContext(ctx, "tcp", "receiver.invalid:443"); err == nil {
		t.Fatal("resolution failure ignored")
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.DialContext(t.Context(), "tcp", address); err == nil || strings.Contains(err.Error(), address) {
		t.Fatal("connection failure leaked address", err)
	}
}
