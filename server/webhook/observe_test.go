package webhook

import (
	"bytes"
	"context"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// TestObservationBoundaries checks HTTP observation boundaries without suppressing panics or
// leaking query strings.
func TestObservationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, request string
		read          bool
		panic         bool
		want          string
	}{
		{"unread", "private-body", false, false, "not_read"},
		{"over-bound", strings.Repeat("x", 2048), true, false, "too_large"},
		{"panic", "body", true, true, "complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t, Config{MaxBody: 1024})
			c := subscribe(t, s, PayloadPolicy{FullJSON: true, RawRequest: true, RawResponse: true})
			mux := http.NewServeMux()
			mux.HandleFunc("/mdm", func(w http.ResponseWriter, r *http.Request) {
				if tc.read {
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						t.Error(err)
					}
				}
				if tc.panic {
					panic("handler failed")
				}
				w.WriteHeader(403)
			})
			func() {
				defer func() {
					if tc.panic && recover() == nil {
						t.Error("panic suppressed")
					}
				}()
				s.Observe(mux, mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "PUT", "/mdm?secret=never-export", strings.NewReader(tc.request)))
			}()
			d := deliveries(t, s, c.Subscription.ID)
			if len(d) != 1 {
				t.Fatal(d)
			}
			r, _, err := s.retained(t.Context(), d[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if p := r.Parts["request_raw"]; p.Availability != tc.want || p.Availability != "complete" && len(p.Value) != 0 {
				t.Fatal(p)
			}
			if bytes.Contains(r.Body, []byte("never-export")) {
				t.Fatal("query leaked")
			}
			if tc.panic && r.Event.Data["outcome"] != "incomplete" {
				t.Fatal(r.Event)
			}
		})
	}
}

// TestCMSObservationAndCaptureFailure checks CMS observation and capture failure.
func TestCMSObservationAndCaptureFailure(t *testing.T) {
	s := testStore(t, Config{CommandType: func(context.Context, mdm.EnrollmentID, string) (string, error) { return "DeviceInformation", nil }})
	c := subscribe(t, s, PayloadPolicy{FullJSON: true, RawRequest: true, RawResponse: true})
	mux := http.NewServeMux()
	mux.HandleFunc("/mdm", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		decoded := []byte(`<plist version="1.0"><dict><key>Status</key><string>Acknowledged</string><key>UDID</key><string>device-1</string><key>CommandUUID</key><string>command-1</string></dict></plist>`)
		resp, err := mdm.DecodeResponse(decoded, "")
		if err != nil {
			t.Error(err)
			return
		}
		ObserveMDM(r.Context(), nil, resp, true)
		w.WriteHeader(103)
		w.WriteHeader(200)
		w.WriteHeader(500) // ignored duplicate final header
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, "reply")
	})
	h := s.Observe(mux, mux)
	raw := []byte("original-CMS-body")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "PUT", "/mdm", bytes.NewReader(raw)))
	d := deliveries(t, s, c.Subscription.ID)[0]
	r, _, err := s.retained(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := payloadBytes(r.Parts["request_raw"])
	if err != nil || !bytes.Equal(b, raw) {
		t.Fatal(err, string(b))
	}
	if r.Event.Data["command_type"] != "DeviceInformation" || !bytes.Contains(r.Parts["request_json"].Value, []byte("Acknowledged")) {
		t.Fatal(r)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE webhook_messages"); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "PUT", "/mdm", bytes.NewReader(raw)))
	if w.Body.String() != "reply" || s.captureFailures.Load() != 1 {
		t.Fatal("observation failure changed reply or was hidden", w.Body.String(), s.captureFailures.Load())
	}
}

// TestRouteAndCorrelationBoundaries checks route and correlation boundaries.
func TestRouteAndCorrelationBoundaries(t *testing.T) {
	s := testStore(t, Config{})
	for path, want := range map[string]string{"/mdm": "mdm", "/scep": "scep", "/acme/new-account": "acme", "/pki/ocsp/issuer": "certificate_status", "/content-cache/metrics": "content_cache", "/.well-known/com.apple.remotemanagement": "enrollment", "/enroll/ade": "enrollment", "/enroll/authenticate": "enrollment", "/enroll/oidc/callback": "enrollment", "/enroll/oauth2/token": "enrollment", "/enroll/mdm-byod": "enrollment", "/ota": "enrollment", "/configuration-profiles/revision": "profile", "/admin/v1/webhooks": "", "/healthz": "", "/ddm/v1/status": "", "/webhooks/v1/payloads/id/body": ""} {
		family, _ := ClassifyRoute(path, path)
		if family != want {
			t.Fatal(path, family)
		}
	}
	if family, _ := ClassifyRoute("", "/mdm"); family != "" {
		t.Fatal("unrouted request captured")
	}
	for _, id := range []string{"", strings.Repeat("x", 65), "untrusted\nvalue"} {
		if CorrelationID(WithCorrelation(t.Context(), id)) != "" {
			t.Fatal(id)
		}
	}
	if CorrelationID(WithCorrelation(t.Context(), "authenticated_hop-1")) != "authenticated_hop-1" {
		t.Fatal("authenticated correlation lost")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/v1/config", func(w http.ResponseWriter, r *http.Request) {
		if CorrelationID(r.Context()) == "" {
			t.Error("admin correlation missing")
		}
	})
	s.Observe(mux, mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "GET", "/admin/v1/config", nil))
	ObserveMDM(t.Context(), nil, nil, true)
	ObserveSubject(t.Context(), Subject{})
	ObserveOutcome(t.Context(), "unknown")
}

// TestOutcomeCaptureUsesSafeSummary checks that outcome capture uses safe summary.
func TestOutcomeCaptureUsesSafeSummary(t *testing.T) {
	s := testStore(t, Config{CommandType: func(context.Context, mdm.EnrollmentID, string) (string, error) { return "DeviceInformation", nil }})
	summary := subscribe(t, s, PayloadPolicy{})
	full := subscribe(t, s, PayloadPolicy{FullJSON: true})
	id := mdm.EnrollmentID{ID: "device-1", Channel: mdm.ChannelDevice}
	for _, e := range []event.Event{
		{Type: event.TokenUpdated, Enrollment: id, Data: &checkin.TokenUpdate{Token: []byte("private-push-token"), PushMagic: "private-magic"}},
		{Type: event.CommandQueued, Enrollment: id, Data: &mdm.Command{UUID: "c1", RequestType: "DeviceInformation"}},
		{Type: event.CommandResult, Enrollment: id, Data: &mdm.Response{CommandUUID: "c1", Status: mdm.StatusAcknowledged}},
		{Type: event.EnrollmentDenied, Enrollment: id},
		{Type: event.Type("future-internal-event")},
	} {
		if err := s.CaptureOutcome(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	if len(deliveries(t, s, summary.Subscription.ID)) != 4 {
		t.Fatal("catalogue capture mismatch")
	}
	for _, d := range deliveries(t, s, summary.Subscription.ID) {
		r, _, err := s.retained(t.Context(), d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(r.Body, []byte("private")) || len(r.Parts) != 0 {
			t.Fatal("summary disclosure")
		}
		if d.Event.Type == "server.enrollment.denied" && d.Event.Subject != nil {
			t.Fatal("claim promoted to verified subject")
		}
	}
	var found bool
	for _, d := range deliveries(t, s, full.Subscription.ID) {
		r, _, err := s.retained(t.Context(), d.ID)
		if err != nil {
			t.Fatal(err)
		}
		found = found || bytes.Contains(r.Body, []byte("private-magic"))
	}
	if !found {
		t.Fatal("full data absent")
	}
}

// TestManagedCertificateCaptureIsAtomic checks managed certificate capture is atomic.
func TestManagedCertificateCaptureIsAtomic(t *testing.T) {
	s := testStore(t, Config{})
	subscribe(t, s, PayloadPolicy{})
	repository, err := statestore.Open(t.Context(), s.db, s.d, s.keys)
	if err != nil {
		t.Fatal(err)
	}
	manager := &lifecycle.Manager{Store: s.ObserveCertificates(repository)}
	request := lifecycle.Request{ID: "managed-issuer", Kind: lifecycle.Issuer, Subject: pkix.Name{CommonName: "Example issuer"}}
	identity, err := manager.Begin(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Pending == "" || len(deliveries(t, s, "")) != 1 {
		t.Fatal(identity)
	}
	if _, err := manager.Begin(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if len(deliveries(t, s, "")) != 1 {
		t.Fatal("no-op transition emitted")
	}
	if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER fail_capture BEFORE INSERT ON webhook_messages BEGIN SELECT RAISE(FAIL,'injected'); END"); err != nil {
		t.Fatal(err)
	}
	request.ID = "must-rollback"
	if _, err := manager.Begin(t.Context(), request); err == nil {
		t.Fatal("transition accepted capture failure")
	}
	if _, err := repository.Get(t.Context(), "pki/lifecycle/identity/must-rollback"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("mutation survived capture failure", err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER fail_capture"); err != nil {
		t.Fatal(err)
	}
	key := "pki/lifecycle/identity/managed-issuer"
	if err := manager.Store.Update(t.Context(), []string{key}, func(tx state.Tx) error {
		r, err := tx.Get(t.Context(), key)
		if err != nil {
			return err
		}
		var fields map[string]any
		if err := json.Unmarshal(r.Value, &fields); err != nil {
			return err
		}
		fields["Active"] = identity.Pending
		fields["Pending"] = ""
		r.Value, err = json.Marshal(fields)
		if err != nil {
			return err
		}
		return tx.Put(t.Context(), r)
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Store.Update(t.Context(), []string{key}, func(tx state.Tx) error { return tx.Delete(t.Context(), key) }); err != nil {
		t.Fatal(err)
	}
	if len(deliveries(t, s, "")) != 3 {
		t.Fatal("missing lifecycle transitions")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := manager.Store.Update(ctx, []string{key}, func(state.Tx) error { return errors.New("transition rejected") }); err == nil {
		t.Fatal("callback error suppressed")
	}
}

type brokenRequest struct{}

// Read returns a partial request body with io.ErrUnexpectedEOF.
func (brokenRequest) Read(p []byte) (int, error) { return copy(p, "partial"), io.ErrUnexpectedEOF }

// Close closes the fixture request body without error.
func (brokenRequest) Close() error { return nil }

type brokenReply struct {
	header   http.Header
	deadline bool
}

// Header returns the fixture response header map.
func (w *brokenReply) Header() http.Header { return w.header }

// WriteHeader ignores response status writes in this failure fixture.
func (*brokenReply) WriteHeader(int) {}

// Write returns a partial write count and io.ErrClosedPipe.
func (*brokenReply) Write([]byte) (int, error) { return 2, io.ErrClosedPipe }

// SetWriteDeadline records that a response-write deadline was set.
func (w *brokenReply) SetWriteDeadline(time.Time) error { w.deadline = true; return nil }

// TestObservationIncompleteIOAndResponseController checks observation incomplete I/O and response
// controller.
func TestObservationIncompleteIOAndResponseController(t *testing.T) {
	s := testStore(t, Config{})
	c := subscribe(t, s, PayloadPolicy{RawRequest: true, RawResponse: true})
	mux := http.NewServeMux()
	mux.HandleFunc("/mdm", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Error(err)
		}
		controller := http.NewResponseController(w)
		if err := controller.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			t.Error(err)
		}
		// Flush is unsupported by the underlying writer and must mark capture incomplete.
		_ = controller.Flush()
		if n, err := io.WriteString(w, "reply"); n != 2 || !errors.Is(err, io.ErrClosedPipe) {
			t.Error(n, err)
		}
	})
	w := &brokenReply{header: http.Header{}}
	s.Observe(mux, mux).ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "PUT", "/mdm", brokenRequest{}))
	if !w.deadline {
		t.Fatal("response controller did not reach the underlying writer")
	}
	d := deliveries(t, s, c.Subscription.ID)[0]
	r, _, err := s.retained(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Event.Data["outcome"] != "incomplete" {
		t.Fatal(r.Event)
	}
	for _, name := range []string{"request_raw", "response_raw"} {
		if p := r.Parts[name]; p.Availability != "incomplete" || len(p.Value) != 0 || p.SHA256 != "" {
			t.Fatal(name, p)
		}
	}
}
