package webhook

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// TestInvalidConfigurationAndInputs checks invalid configuration and inputs.
func TestInvalidConfigurationAndInputs(t *testing.T) {
	s := testStore(t, Config{})
	for _, cfg := range []Config{{PayloadRetention: -1}, {PayloadRetention: time.Hour, MetadataRetention: time.Minute}, {MaxBody: 1}, {MaxBody: 65 << 20}, {PrivateNetworks: []string{"invalid"}}, {RootCAFile: filepath.Join(t.TempDir(), "missing")}} {
		if _, err := Open(t.Context(), s.db, s.d, s.keys, s.outbox, cfg); err == nil {
			t.Fatal("invalid configuration accepted", cfg)
		}
	}
	if _, err := Open(t.Context(), nil, s.d, s.keys, s.outbox, Config{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), s.db, s.d, nil, s.outbox, Config{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), s.db, s.d, s.keys, nil, Config{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), s.db, sqlcommon.Dialect{Name: "unsupported"}, s.keys, s.outbox, Config{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	base := Spec{Name: "receiver", URL: "https://example.test/webhook", Events: []string{"*"}}
	for _, mutate := range []func(*Spec){
		func(s *Spec) { s.Name = "" }, func(s *Spec) { s.Events = nil }, func(s *Spec) { s.Events = []string{"unknown.*"} },
		func(s *Spec) { s.URL = "http://example.test" }, func(s *Spec) { s.URL = "https://user:secret@example.test" }, func(s *Spec) { s.URL = "https://example.test/#fragment" },
		func(s *Spec) { s.Filters.Subjects = make([]string, 257) }, func(s *Spec) { s.Filters.Subjects = []string{""} }, func(s *Spec) { s.Filters.Subjects = []string{strings.Repeat("x", 257)} },
		func(s *Spec) { s.Filters.Outcomes = []string{"unreviewed"} }, func(s *Spec) { s.Filters.Channels = []string{"unreviewed"} },
	} {
		spec := base
		mutate(&spec)
		if _, err := s.Create(t.Context(), spec, true); !errors.Is(err, ErrInvalid) {
			t.Fatal(spec, err)
		}
		if _, err := s.Update(t.Context(), "missing", 1, spec, true); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, e := range []Event{{Type: "unknown"}, {Type: "webhook.test", EventID: strings.Repeat("x", 65)}} {
		if err := s.Capture(t.Context(), e); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	c := subscribe(t, s, PayloadPolicy{FullJSON: true})
	if _, err := s.SetState(t.Context(), c.Subscription.ID, "bad", true); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Rotate(t.Context(), c.Subscription.ID, -time.Second, true); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { _, err := s.SetState(t.Context(), "missing", "pause", true); return err },
		func() error { _, err := s.Rotate(t.Context(), "missing", 0, true); return err },
		func() error { _, err := s.Update(t.Context(), "missing", 1, base, true); return err },
		func() error { _, err := s.Test(t.Context(), "missing", true); return err },
		func() error { return s.Retry(t.Context(), "missing", true) },
		func() error {
			_, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: "missing", DryRun: true}, true)
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if _, err := s.Deliveries(t.Context(), "", "", 1001); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.List(t.Context(), "", -1); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.SetState(t.Context(), c.Subscription.ID, "delete", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rotate(t.Context(), c.Subscription.ID, 0, true); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Test(t.Context(), c.Subscription.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: c.Subscription.ID, DryRun: true}, true); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

// TestCaptureErrorsRollBackEveryDestination checks capture errors roll back every destination.
func TestCaptureErrorsRollBackEveryDestination(t *testing.T) {
	for _, table := range []string{"webhook_messages", "event_records", "event_deliveries"} {
		t.Run(table, func(t *testing.T) {
			s := testStore(t, Config{})
			subscribe(t, s, PayloadPolicy{})
			subscribe(t, s, PayloadPolicy{FullJSON: true})
			if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER fail_capture BEFORE INSERT ON "+table+" BEGIN SELECT RAISE(FAIL,'injected'); END"); err != nil { // #nosec G202 -- table comes exclusively from the three fixed fixture names above.
				t.Fatal(err)
			}
			if err := s.Capture(t.Context(), occurrence()); err == nil {
				t.Fatal("capture failure suppressed")
			}
			var count int
			if err := s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM webhook_messages").Scan(&count); err != nil || count != 0 {
				t.Fatal("partial fanout committed", count, err)
			}
			status, err := s.Status(t.Context())
			if err != nil || status["capture_failures"] != uint64(1) {
				t.Fatal(status, err)
			}
		})
	}
	s := testStore(t, Config{})
	subscribe(t, s, PayloadPolicy{FullJSON: true})
	for _, data := range []map[string]any{{"unsupported": make(chan int)}, {"oversize": strings.Repeat("x", InlineLimit+1)}} {
		e := occurrence()
		e.Data = data
		if err := s.Capture(t.Context(), e); err == nil {
			t.Fatal("invalid summary accepted")
		}
	}
	if err := s.CaptureOutcome(t.Context(), event.Event{Type: event.AdminAction, Data: make(chan int)}); err == nil {
		t.Fatal("unsupported data accepted")
	}
	s.cfg.CommandType = func(context.Context, mdm.EnrollmentID, string) (string, error) {
		return "", errors.New("unavailable command metadata")
	}
	if err := s.CaptureOutcome(t.Context(), event.Event{Type: event.CommandResult, Data: &mdm.Response{CommandUUID: "c1"}}); err == nil {
		t.Fatal("query failure hidden")
	}
}

// TestSubscriptionChangesRollBackCredentialsAndBacklog checks subscription changes roll back
// credentials and backlog.
func TestSubscriptionChangesRollBackCredentialsAndBacklog(t *testing.T) {
	for _, table := range []string{"event_deliveries", "webhook_subscriptions"} {
		t.Run(table, func(t *testing.T) {
			s := testStore(t, Config{})
			c := subscribe(t, s, PayloadPolicy{})
			if err := s.Capture(t.Context(), occurrence()); err != nil {
				t.Fatal(err)
			}
			before, err := s.load(t.Context(), c.Subscription.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER fail_change BEFORE UPDATE ON "+table+" BEGIN SELECT RAISE(FAIL,'injected'); END"); err != nil {
				t.Fatal(err)
			} // #nosec G202 -- table comes only from the two fixed fixture names above.
			if _, err := s.Update(t.Context(), before.ID, before.Revision, before.Spec, true); err == nil {
				t.Fatal("update ignored persistence failure")
			}
			if _, err := s.SetState(t.Context(), before.ID, "pause", true); err == nil {
				t.Fatal("pause ignored persistence failure")
			}
			if table == "webhook_subscriptions" {
				if _, err := s.Rotate(t.Context(), before.ID, time.Hour, true); err == nil {
					t.Fatal("rotation ignored persistence failure")
				}
			}
			after, err := s.load(t.Context(), before.ID, false)
			if err != nil || after.Key != before.Key || after.TokenHash != before.TokenHash || after.Revision != before.Revision || after.Paused {
				t.Fatal("configuration changed on rollback", err)
			}
			if d := deliveries(t, s, before.ID); len(d) != 1 || d[0].State != "pending" {
				t.Fatal("backlog changed on rollback", d)
			}
		})
	}
}

// TestPagedFanoutAndRetentionLoop checks paged fanout and retention loop.
func TestPagedFanoutAndRetentionLoop(t *testing.T) {
	s := testStore(t, Config{MaxBody: 1024})
	// More than one configuration page must participate in the same capture.
	for i := range 1001 {
		if _, err := s.Create(t.Context(), Spec{Name: fmt.Sprintf("receiver-%d", i), URL: "https://receiver.example.test", Events: []string{"webhook.test"}, Payload: PayloadPolicy{RawRequest: true}}, true); err != nil {
			t.Fatal(err)
		}
	}
	e := Event{Type: "webhook.test", Payloads: map[string]Payload{"request_raw": BodyPayload([]byte(strings.Repeat("x", 2048)), "application/octet-stream", false)}}
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM webhook_messages").Scan(&count); err != nil || count != 1001 {
		t.Fatal(count, err)
	}
	status, err := s.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	counts, ok := status["states"].(map[string]int64)
	if !ok || counts["pending"] != 1001 {
		t.Fatal(status)
	}
	d := deliveries(t, s, "")[0]
	r, _, err := s.retained(t.Context(), d.ID)
	if err != nil || r.Parts["request_raw"].Availability != "too_large" {
		t.Fatal(err, r)
	}
	synctest.Test(t, func(t *testing.T) {
		loop := testStore(t, Config{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- loop.RunRetention(ctx) }()
		time.Sleep(61 * time.Second)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

// TestCorruptEncryptedStateAndUnavailableDatabase checks corrupt encrypted state and unavailable
// database.
func TestCorruptEncryptedStateAndUnavailableDatabase(t *testing.T) {
	s := testStore(t, Config{})
	c := subscribe(t, s, PayloadPolicy{})
	if _, err := s.db.ExecContext(t.Context(), "UPDATE webhook_subscriptions SET config = ?", []byte(`{"Key":"plaintext"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), c.Subscription.ID); err == nil {
		t.Fatal("plaintext secret accepted")
	}
	if _, err := s.List(t.Context(), "", 0); err == nil {
		t.Fatal("corrupt configuration listed")
	}
	if err := s.Capture(t.Context(), occurrence()); err == nil {
		t.Fatal("corrupt capture configuration ignored")
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), s.db, s.d, s.keys, s.outbox, Config{}); err == nil {
		t.Fatal("closed database accepted")
	}
	if _, err := s.Deliveries(t.Context(), "", "", 0); err == nil {
		t.Fatal("closed database read")
	}
	if _, err := s.Status(t.Context()); err == nil {
		t.Fatal("closed database status")
	}
	if _, err := s.Rewrap(t.Context()); err == nil {
		t.Fatal("closed database rewrap")
	}
}

// TestReplayNeverInventsMissingParts checks that replay never invents missing parts.
func TestReplayNeverInventsMissingParts(t *testing.T) {
	s := testStore(t, Config{})
	subscribe(t, s, PayloadPolicy{})
	for i := range 3 {
		e := occurrence()
		e.EventID = fmt.Sprintf("occurrence-%d", i)
		if err := s.Capture(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	target := subscribe(t, s, PayloadPolicy{FullJSON: true, RawRequest: true})
	r, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: target.Subscription.ID, Key: "replay-missing", Limit: 2, After: time.Now().Add(-time.Hour), Before: time.Now().Add(time.Hour), Type: "protocol.mdm.exchange"}, true)
	if err != nil || len(r.EventIDs) != 2 || len(r.Missing) != 2 || !r.Truncated {
		t.Fatal(r, err)
	}
	for _, id := range r.DeliveryIDs {
		retained, _, err := s.retained(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range retained.Parts {
			if p.Availability != "not_captured" || len(p.Value) != 0 {
				t.Fatal("invented data", p)
			}
		}
	}
	standard := subscribe(t, s, PayloadPolicy{})
	if _, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: standard.Subscription.ID, Key: "delegated", EventIDs: []string{"occurrence-1"}}, false); err != nil {
		t.Fatal(err)
	}
}

// TestRepeatedReplayPreservesAvailablePartsAndMissingReport checks that repeated replay preserves
// available parts and missing report.
func TestRepeatedReplayPreservesAvailablePartsAndMissingReport(t *testing.T) {
	s := testStore(t, Config{})
	subscribe(t, s, PayloadPolicy{FullJSON: true})
	e := occurrence()
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	// The source retained request_json. This broader destination adds explicit
	// not_captured descriptors for the three representations that were absent.
	target := subscribe(t, s, PayloadPolicy{FullJSON: true, RawRequest: true, RawResponse: true})
	first, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: target.Subscription.ID, Key: "first"}, true)
	if err != nil || len(first.Missing[e.EventID]) != 3 {
		t.Fatal(first, err)
	}
	// Replaying a replay must not mistake not_captured descriptors for data.
	second, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: target.Subscription.ID, Key: "second"}, true)
	if err != nil || len(second.Missing[e.EventID]) != 3 {
		t.Fatal("missing representations disappeared after repeated replay", second, err)
	}
	r, _, err := s.retained(t.Context(), second.DeliveryIDs[0])
	if err != nil || string(r.Parts["request_json"].Value) != string(e.Payloads["request_json"].Value) {
		t.Fatal("replay lost the captured representation", r, err)
	}
}

// TestReplayPrefersCapturedJSONOverReplayPlaceholders checks replay prefers captured JSON over
// replay placeholders.
func TestReplayPrefersCapturedJSONOverReplayPlaceholders(t *testing.T) {
	s := testStore(t, Config{})
	subscribe(t, s, PayloadPolicy{RawRequest: true, RawResponse: true})
	jsonSource := subscribe(t, s, PayloadPolicy{FullJSON: true})
	e := occurrence()
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	// Raw captures cover two parts; the JSON capture covers only request_json.
	// Replaying to all representations chooses the raw snapshot and adds two
	// not_captured JSON descriptors without combining the source snapshots.
	all := subscribe(t, s, PayloadPolicy{FullJSON: true, RawRequest: true, RawResponse: true})
	if _, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: all.Subscription.ID, Key: "all"}, true); err != nil {
		t.Fatal(err)
	}
	// Those two descriptors must not outrank the original captured JSON.
	result, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: jsonSource.Subscription.ID, Key: "json"}, true)
	if err != nil || len(result.DeliveryIDs) != 1 {
		t.Fatal(result, err)
	}
	r, _, err := s.retained(t.Context(), result.DeliveryIDs[0])
	if err != nil || string(r.Parts["request_json"].Value) != string(e.Payloads["request_json"].Value) {
		t.Fatal("placeholder snapshot outranked the retained body", r.Parts, err)
	}
}

// TestRetentionFailurePreservesData checks that retention failure preserves data.
func TestRetentionFailurePreservesData(t *testing.T) {
	for _, spec := range []struct{ table, operation string }{{"event_deliveries", "UPDATE"}, {"webhook_messages", "UPDATE"}, {"event_deliveries", "DELETE"}, {"event_records", "DELETE"}, {"webhook_messages", "DELETE"}, {"webhook_replays", "DELETE"}} {
		t.Run(spec.table+spec.operation, func(t *testing.T) {
			s := testStore(t, Config{})
			c := subscribe(t, s, PayloadPolicy{})
			if err := s.Capture(t.Context(), occurrence()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Replay(t.Context(), ReplayRequest{SubscriptionID: c.Subscription.ID, Key: "retention-replay"}, true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER fail_prune BEFORE "+spec.operation+" ON "+spec.table+" BEGIN SELECT RAISE(FAIL,'injected'); END"); err != nil {
				t.Fatal(err)
			}
			s.cfg.Now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
			if err := s.Prune(t.Context()); err == nil {
				t.Fatal("prune failure ignored")
			}
			var count int
			if err := s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM webhook_messages WHERE payload IS NOT NULL").Scan(&count); err != nil || count != 2 {
				t.Fatal("partial prune", count, err)
			}
		})
	}
}

// TestPayloadAuthenticationAndExpiryFailures checks payload authentication and expiry failures.
func TestPayloadAuthenticationAndExpiryFailures(t *testing.T) {
	s := testStore(t, Config{})
	c := subscribe(t, s, PayloadPolicy{RawRequest: true})
	if err := s.Capture(t.Context(), occurrence()); err != nil {
		t.Fatal(err)
	}
	id := deliveries(t, s, c.Subscription.ID)[0].ID
	request := func(method, path, token string, want int) {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), method, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.PayloadHandler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(method, path, w.Code, want)
		}
	}
	request("POST", PayloadPath+id+"/request_raw", c.Credentials.PayloadToken, 405)
	request("GET", PayloadPath+id, c.Credentials.PayloadToken, 404)
	request("GET", PayloadPath+"missing/request_raw", c.Credentials.PayloadToken, 404)
	request("GET", PayloadPath+id+"/request_raw", "", 401)
	request("GET", PayloadPath+id+"/request_json", c.Credentials.PayloadToken, 404)
	if _, err := s.SetState(t.Context(), c.Subscription.ID, "delete", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(t.Context(), id); !errors.Is(err, eventstore.ErrCancelled) {
		t.Fatal(err)
	}
	request("GET", PayloadPath+id+"/request_raw", c.Credentials.PayloadToken, 401)
	other := subscribe(t, s, PayloadPolicy{RawRequest: true})
	if err := s.Capture(t.Context(), occurrence()); err != nil {
		t.Fatal(err)
	}
	id = deliveries(t, s, other.Subscription.ID)[0].ID
	s.cfg.Now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	request("GET", PayloadPath+id+"/request_raw", other.Credentials.PayloadToken, 410)
	if err := s.Retry(t.Context(), id, true); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	s.cfg.Now = time.Now
	if _, err := s.db.ExecContext(t.Context(), "UPDATE webhook_messages SET payload = NULL WHERE delivery_id = ?", id); err != nil {
		t.Fatal(err)
	}
	request("GET", PayloadPath+id+"/request_raw", other.Credentials.PayloadToken, 410)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	request("GET", PayloadPath+id+"/request_raw", other.Credentials.PayloadToken, 503)
	for _, p := range []Payload{{Availability: "incomplete"}, {Availability: "complete", Encoding: "base64", Value: json.RawMessage(`42`)}, {Availability: "complete", Encoding: "base64", Value: json.RawMessage(`"invalid"`)}} {
		if _, err := payloadBytes(p); err == nil {
			t.Fatal("invalid representation accepted")
		}
	}
}

// TestPrivateTrustConfiguration checks private webhook trust configuration.
func TestPrivateTrustConfiguration(t *testing.T) {
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer receiver.Close()
	root := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(root, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: receiver.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	s := testStore(t, Config{RootCAFile: root, PrivateNetworks: []string{"127.0.0.0/8"}})
	c, err := s.Create(t.Context(), Spec{Name: "private", URL: receiver.URL, Events: []string{"*"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Test(t.Context(), c.Subscription.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("bad PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newHTTPClient(Config{RootCAFile: root}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- negative test proves this configuration is rejected.
	if _, err := newHTTPClient(Config{Client: &http.Client{Transport: transport}}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
