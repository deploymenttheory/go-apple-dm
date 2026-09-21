package webhook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// testStore opens a temporary SQLite webhook store with encrypted keys and a durable event outbox.
func testStore(t *testing.T, cfg Config) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", sqlite.DSN(filepath.Join(t.TempDir(), "webhook.sqlite"), sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keys, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "test", Strict: true}, Provider: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}})
	if err != nil {
		t.Fatal(err)
	}
	outbox, err := eventstore.Open(t.Context(), db, sqlite.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), db, sqlite.Dialect, keys, outbox, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// subscribe creates an all-event subscription using the supplied payload policy and fixture
// sensitive-payload authorization.
func subscribe(t *testing.T, s *Store, policy PayloadPolicy) Change {
	t.Helper()
	c, err := s.Create(t.Context(), Spec{Name: "workflow", URL: "https://receiver.example.test/webhook", Events: []string{"*"}, Payload: policy}, true)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// occurrence returns an MDM exchange event fixture with private request and response payloads.
func occurrence() Event {
	return Event{EventID: "occurrence-example", Type: "protocol.mdm.exchange", Subject: &Subject{Kind: "enrollment", ID: "device-1", Channel: "device"}, Data: map[string]any{"operation": "connect", "outcome": "succeeded", "device_status": "Acknowledged"}, Payloads: map[string]Payload{"request_raw": BodyPayload([]byte("private-request"), "application/xml", false), "request_json": BodyPayload([]byte(`{"Token":"private-token"}`), "application/json", true), "response_raw": BodyPayload([]byte("private-response"), "application/xml", false)}}
}

// deliveries loads up to 1,000 deliveries for the supplied subscription, failing the test on
// error.
func deliveries(t *testing.T, s *Store, id string) []Delivery {
	t.Helper()
	d, err := s.Deliveries(t.Context(), id, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestCaptureAtomicityAndDisclosure checks capture atomicity and disclosure.
func TestCaptureAtomicityAndDisclosure(t *testing.T) {
	s := testStore(t, Config{})
	if err := s.Capture(t.Context(), occurrence()); err != nil {
		t.Fatal(err)
	}
	if len(deliveries(t, s, "")) != 0 {
		t.Fatal("retained unmatched occurrence")
	}
	summary := subscribe(t, s, PayloadPolicy{})
	full := subscribe(t, s, PayloadPolicy{FullJSON: true, RawRequest: true, RawResponse: true})
	errRollback := errors.New("rollback")
	err := s.outbox.Run(t.Context(), func(ctx context.Context) error {
		if err := s.Capture(ctx, occurrence()); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) || len(deliveries(t, s, "")) != 0 {
		t.Fatal("capture escaped rollback", err)
	}
	e := occurrence()
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	e.Payloads["request_raw"] = BodyPayload([]byte("mutated"), "", false)
	for _, sub := range []Change{summary, full} {
		d := deliveries(t, s, sub.Subscription.ID)
		if len(d) != 1 {
			t.Fatal(d)
		}
		r, _, err := s.retained(t.Context(), d[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if sub.Subscription.Payload.Sensitive() {
			if !bytes.Contains(r.Body, []byte("private-token")) || bytes.Contains(r.Body, []byte("mutated")) {
				t.Fatal(string(r.Body))
			}
		} else if bytes.Contains(r.Body, []byte("payloads")) {
			t.Fatal("summary leaked payload", string(r.Body))
		}
		var sealed []byte
		if err := s.db.QueryRowContext(t.Context(), "SELECT payload FROM webhook_messages WHERE delivery_id = ?", d[0].ID).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(sealed, []byte("private")) {
			t.Fatal("plaintext database body")
		}
		meta, _ := json.Marshal(d[0])
		if bytes.Contains(meta, []byte("private")) {
			t.Fatal("diagnostic leak", string(meta))
		}
	}
	if _, err := s.Rewrap(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// TestJSONReferencePreservesNumberAndDigest checks that JSON reference preserves number and
// digest.
func TestJSONReferencePreservesNumberAndDigest(t *testing.T) {
	s := testStore(t, Config{})
	c := subscribe(t, s, PayloadPolicy{FullJSON: true})
	body := []byte(`{ "large_integer": 9007199254740993, "data": "` + strings.Repeat("x", InlineLimit) + `" }`)
	p := BodyPayload(body, "application/json", true)
	e := occurrence()
	e.Payloads = map[string]Payload{"request_json": p}
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	d := deliveries(t, s, c.Subscription.ID)[0]
	r, _, err := s.retained(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	var delivered Event
	if err := json.Unmarshal(r.Body, &delivered); err != nil {
		t.Fatal(err)
	}
	ref := delivered.Payloads["request_json"]
	if ref.Href == "" || len(ref.Value) != 0 {
		t.Fatal(ref)
	}
	req := httptest.NewRequestWithContext(t.Context(), "GET", ref.Href, nil)
	req.Header.Set("Authorization", "Bearer "+c.Credentials.PayloadToken)
	w := httptest.NewRecorder()
	s.PayloadHandler().ServeHTTP(w, req)
	digest := sha256.Sum256(w.Body.Bytes())
	if w.Code != 200 || w.Body.Len() != ref.Size || hex.EncodeToString(digest[:]) != ref.SHA256 || !bytes.Equal(w.Body.Bytes(), p.Value) || !bytes.Contains(w.Body.Bytes(), []byte("9007199254740993")) {
		t.Fatal("reference changed canonical JSON bytes or numeric precision", w.Code)
	}
	if p := BodyPayload([]byte("malformed JSON"), "application/json", true); p.Availability != "undecodable" || len(p.Value) != 0 {
		t.Fatal(p)
	}
}

// TestRootGateAndRevisionIsolation checks sensitive-payload authorization and isolation of
// subscription revisions.
func TestRootGateAndRevisionIsolation(t *testing.T) {
	s := testStore(t, Config{})
	spec := Spec{Name: "sensitive", URL: "https://receiver.example.test/hook", Events: []string{"*"}, Payload: PayloadPolicy{FullJSON: true}}
	if _, err := s.Create(t.Context(), spec, false); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	c, err := s.Create(t.Context(), spec, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Capture(t.Context(), occurrence()); err != nil {
		t.Fatal(err)
	}
	d := deliveries(t, s, c.Subscription.ID)[0]
	for _, action := range []string{"pause", "resume", "enable", "disable", "delete"} {
		if _, err := s.SetState(t.Context(), c.Subscription.ID, action, false); !errors.Is(err, ErrForbidden) {
			t.Fatal(action, err)
		}
	}
	if _, err := s.Rotate(t.Context(), c.Subscription.ID, time.Hour, false); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if err := s.Retry(t.Context(), d.ID, false); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := s.Test(t.Context(), c.Subscription.ID, false); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := s.Update(t.Context(), c.Subscription.ID, 1, spec, false); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	spec.URL = "https://new-receiver.example.test/hook"
	updated, err := s.Update(t.Context(), c.Subscription.ID, 1, spec, true)
	if err != nil || updated.Subscription.Revision != 2 {
		t.Fatal(updated, err)
	}
	if err := s.Send(t.Context(), d.ID); !errors.Is(err, eventstore.ErrPaused) {
		t.Fatal(err)
	}
	if _, err := s.SetState(t.Context(), c.Subscription.ID, "resume", true); err != nil {
		t.Fatal(err)
	}
	if got := deliveries(t, s, c.Subscription.ID)[0].State; got != "paused" {
		t.Fatal(got)
	}
	if err := s.Retry(t.Context(), d.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Update(t.Context(), c.Subscription.ID, 1, spec, true); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

// TestReplayAndRetention checks webhook replay and expiry-driven retention cleanup.
func TestReplayAndRetention(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	s := testStore(t, Config{Now: func() time.Time { return now }})
	original := subscribe(t, s, PayloadPolicy{FullJSON: true})
	if err := s.Capture(t.Context(), occurrence()); err != nil {
		t.Fatal(err)
	}
	target := subscribe(t, s, PayloadPolicy{})
	req := ReplayRequest{SubscriptionID: target.Subscription.ID, EventIDs: []string{"occurrence-example"}, Key: "operator-replay", DryRun: true}
	preview, err := s.Replay(t.Context(), req, true)
	if err != nil || len(preview.EventIDs) != 1 || len(preview.DeliveryIDs) != 0 {
		t.Fatal(preview, err)
	}
	req.DryRun = false
	out, err := s.Replay(t.Context(), req, true)
	if err != nil || len(out.DeliveryIDs) != 1 {
		t.Fatal(out, err)
	}
	again, err := s.Replay(t.Context(), req, true)
	if err != nil || again.DeliveryIDs[0] != out.DeliveryIDs[0] {
		t.Fatal(again, err)
	}
	r, _, err := s.retained(t.Context(), out.DeliveryIDs[0])
	if err != nil || bytes.Contains(r.Body, []byte("private")) {
		t.Fatal(err, string(r.Body))
	}
	req.Type = "protocol.ddm.exchange"
	if _, err := s.Replay(t.Context(), req, true); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: original.Subscription.ID, DryRun: true}, false); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	now = now.Add(8 * 24 * time.Hour)
	if err := s.Send(t.Context(), out.DeliveryIDs[0]); !errors.Is(err, eventstore.ErrExpired) {
		t.Fatal(err)
	}
	if err := s.Prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := deliveries(t, s, ""); len(got) != 2 || got[0].State != "expired" {
		t.Fatal(got)
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM webhook_messages WHERE payload IS NOT NULL").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	now = now.Add(23 * 24 * time.Hour)
	if err := s.Prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(deliveries(t, s, "")) != 0 {
		t.Fatal("metadata retained past expiry")
	}
}

// TestPayloadReferenceAuthority checks payload-reference authorization and rejection after
// credential rotation.
func TestPayloadReferenceAuthority(t *testing.T) {
	s := testStore(t, Config{})
	c := subscribe(t, s, PayloadPolicy{RawRequest: true})
	other := subscribe(t, s, PayloadPolicy{RawRequest: true})
	e := occurrence()
	original := []byte(strings.Repeat("x", InlineLimit))
	e.Payloads["request_raw"] = BodyPayload(original, "application/octet-stream", false)
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	d := deliveries(t, s, c.Subscription.ID)[0]
	r, _, err := s.retained(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	var envelope Event
	if err := json.Unmarshal(r.Body, &envelope); err != nil {
		t.Fatal(err)
	}
	p := envelope.Payloads["request_raw"]
	if p.Href == "" || len(p.Value) != 0 || len(r.Body) > InlineLimit {
		t.Fatal(p)
	}
	for _, token := range []string{"admin-token", other.Credentials.PayloadToken, c.Credentials.PayloadToken} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, p.Href, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.PayloadHandler().ServeHTTP(w, req)
		if token == c.Credentials.PayloadToken {
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), original) {
				t.Fatal(w.Code)
			}
		} else if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if _, err := s.Rotate(t.Context(), c.Subscription.ID, time.Hour, true); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, p.Href, nil)
	req.Header.Set("Authorization", "Bearer "+c.Credentials.PayloadToken)
	w := httptest.NewRecorder()
	s.PayloadHandler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("rotated payload credential accepted", w.Code)
	}
}
