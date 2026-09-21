package webhook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

// These fixtures are receiver-visible JSON produced by capture and delivery.
// Only random occurrence/delivery/correlation IDs are normalized. To intentionally
// update the reviewed contract, run UPDATE_WEBHOOK_FIXTURES=1 go test ./webhook
// from server, then review the complete testdata diff.
func TestReceiverExamples(t *testing.T) {
	const udid = "55693EB3-DF03-5FD1-9263-F7CDB8AD7FFD"
	const uuid = "41d35de3-a343-4146-ba4b-0069bae2a54f"
	const ack = `<?xml version="1.0"?><plist version="1.0"><dict><key>UDID</key><string>` + udid + `</string><key>CommandUUID</key><string>` + uuid + `</string><key>Status</key><string>Acknowledged</string></dict></plist>`
	const idle = `<plist version="1.0"><dict><key>UDID</key><string>` + udid + `</string><key>Status</key><string>Idle</string></dict></plist>`
	const token = `<plist version="1.0"><dict><key>MessageType</key><string>TokenUpdate</string><key>UDID</key><string>` + udid + `</string><key>Topic</key><string>com.apple.mgmt.External.example</string><key>Token</key><data>ZXhhbXBsZS10b2tlbg==</data><key>PushMagic</key><string>example-push-magic</string></dict></plist>`
	ddmStatus := `<plist version="1.0"><dict><key>MessageType</key><string>DeclarativeManagement</string><key>UDID</key><string>` + udid + `</string><key>Endpoint</key><string>status</string><key>Data</key><data>` + base64.StdEncoding.EncodeToString([]byte(`{"StatusItems":{"device.operating-system.version":"27.0"}}`)) + `</data></dict></plist>`
	full := PayloadPolicy{FullJSON: true}
	raw := PayloadPolicy{RawRequest: true, RawResponse: true}
	all := PayloadPolicy{FullJSON: true, RawRequest: true, RawResponse: true}
	cases := []struct {
		name, path, request, response string
		status                        int
		policy                        PayloadPolicy
		mdmKind                       string
		accepted                      bool
	}{
		{"mdm-command-summary", "/mdm", ack, "", 200, PayloadPolicy{}, "connect", true},
		{"mdm-command-full-json", "/mdm", ack, "", 200, full, "connect", true},
		{"mdm-command-raw", "/mdm", ack, "", 200, raw, "connect", true},
		{"mdm-rejected", "/mdm", ack, "Forbidden\n", 403, all, "connect", false},
		{"mdm-malformed", "/mdm", "not a plist", "Bad Request\n", 400, all, "", false},
		{"ddm-status", "/mdm", ddmStatus, "", 200, all, "checkin", true},
		{"mdm-idle", "/mdm", idle, "", 200, full, "connect", true},
		{"mdm-token-update", "/mdm", token, "", 200, all, "checkin", true},
		{"enrollment-profile", "/enroll/ade", "", `<plist version="1.0"><dict><key>PayloadType</key><string>Configuration</string><key>PayloadIdentifier</key><string>example.mdm</string></dict></plist>`, 200, all, "", false},
		{"enrollment-discovery", "/.well-known/com.apple.remotemanagement", "", `{"Servers":[{"Version":"mdm-byod","BaseURL":"https://mdm.example.test/enroll/mdm-byod"}]}`, 200, full, "", false},
		{"scep-rejected", "/scep", "bad-scep-request", "Bad Request\n", 400, raw, "", false},
		{"acme-error", "/acme/new-order", `{"protected":"example","payload":"example","signature":"example"}`, `{"type":"urn:ietf:params:acme:error:malformed","detail":"invalid request"}`, 400, full, "", false},
		{"certificate-status", "/pki/crl/example", "", "example-der-body", 200, raw, "", false},
		{"content-cache", "/content-cache/metrics", `{"version":1,"reportDate":"2026-09-20T12:00:00Z","hostname":"example-mac","hardware":"Mac16,1","serverGUID":"13D4D110-B2B7-4F26-8E25-CD22E58C00EE"}`, "", 202, full, "content-cache", true},
		{"large-reference", "/mdm", strings.Repeat("x", InlineLimit), "Bad Request\n", 400, raw, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
			var received []byte
			var headers http.Header
			receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var err error
				received, err = io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				headers = r.Header.Clone()
				w.WriteHeader(204)
			}))
			defer receiver.Close()
			s := testStore(t, Config{Source: "mdm", Now: func() time.Time { return now }, Client: receiver.Client(), PrivateNetworks: []string{"127.0.0.0/8"}})
			c, err := s.Create(t.Context(), Spec{Name: tc.name, URL: receiver.URL, Events: []string{"protocol.*"}, Payload: tc.policy}, true)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			mux.HandleFunc(tc.path, func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				switch tc.mdmKind {
				case "connect":
					resp, err := mdm.DecodeResponse(b, "")
					if err != nil {
						t.Error(err)
						return
					}
					ObserveMDM(r.Context(), nil, resp, tc.accepted)
				case "checkin":
					ck, err := mdm.DecodeCheckin(b)
					if err != nil {
						t.Error(err)
						return
					}
					ObserveMDM(r.Context(), ck, nil, tc.accepted)
				case "content-cache":
					ObserveSubject(r.Context(), Subject{Kind: "enrollment", ID: udid, Channel: "device"})
					ObserveOutcome(r.Context(), "succeeded")
				}
				w.Header().Set("Content-Type", "application/xml")
				if json.Valid([]byte(tc.response)) {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.response)
			})
			method, contentType := http.MethodPut, "application/x-apple-aspen-mdm"
			if tc.mdmKind == "checkin" {
				contentType = "application/x-apple-aspen-mdm-checkin"
			}
			if tc.path != "/mdm" {
				method = http.MethodPost
				contentType = "application/octet-stream"
				if tc.request == "" {
					method = http.MethodGet
				}
				if json.Valid([]byte(tc.request)) {
					contentType = "application/json"
				}
			}
			req := httptest.NewRequestWithContext(t.Context(), method, tc.path, strings.NewReader(tc.request))
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			s.Observe(mux, mux).ServeHTTP(w, req)
			if w.Code != tc.status || w.Body.String() != tc.response {
				t.Fatal("observation changed response", w.Code, w.Body.String())
			}
			d := deliveries(t, s, c.Subscription.ID)
			if len(d) != 1 {
				t.Fatal("missing capture", d)
			}
			if err := s.Send(t.Context(), d[0].ID); err != nil {
				t.Fatal(err)
			}
			if err := Verify(c.Credentials.SigningSecret, headers, received, now); err != nil {
				t.Fatal("receiver verification", err)
			}
			var e Event
			if err := json.Unmarshal(received, &e); err != nil {
				t.Fatal(err)
			}
			e.EventID = "evt_example_" + strings.ReplaceAll(tc.name, "-", "_")
			e.CorrelationID = "corr_example_exchange"
			for k, p := range e.Payloads {
				if p.Href != "" {
					p.Href = PayloadPath + "delivery_example/" + k
					e.Payloads[k] = p
				}
			}
			assertReceiverExample(t, tc.name, e)
		})
	}
}

// assertReceiverExample compares the event's formatted JSON with the receiver fixture, optionally
// updating it when UPDATE_WEBHOOK_FIXTURES is set.
func assertReceiverExample(t *testing.T, name string, e Event) {
	t.Helper()
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	path := filepath.Join("testdata", name+".json")
	if os.Getenv("UPDATE_WEBHOOK_FIXTURES") == "1" {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) // #nosec G304 -- path is built exclusively from fixed fixture names in these tests.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, b) {
		t.Fatalf("receiver contract changed; review %s\ngot:\n%s", path, b)
	}
}

// TestServerOutcomeReceiverExamples checks server outcome receiver examples.
func TestServerOutcomeReceiverExamples(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"server-command-result", "server-command-result-full-json", "server-worker-state", "server-certificate-lifecycle"} {
		t.Run(name, func(t *testing.T) {
			var body []byte
			var headers http.Header
			receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var err error
				body, err = io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				headers = r.Header.Clone()
				w.WriteHeader(204)
			}))
			defer receiver.Close()
			s := testStore(t, Config{Source: "mdm", Now: func() time.Time { return now }, Client: receiver.Client(), PrivateNetworks: []string{"127.0.0.0/8"}, CommandType: func(context.Context, mdm.EnrollmentID, string) (string, error) { return "DeviceInformation", nil }})
			c, err := s.Create(t.Context(), Spec{Name: name, URL: receiver.URL, Events: []string{"server.*"}, Payload: PayloadPolicy{FullJSON: strings.HasSuffix(name, "full-json")}}, true)
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithCorrelation(t.Context(), "corr_example_exchange")
			switch name {
			case "server-worker-state":
				err = s.Capture(ctx, Event{Type: "server.worker.state", Data: map[string]any{"worker": "event-delivery", "running": true}})
			case "server-certificate-lifecycle":
				err = s.Capture(ctx, Event{Type: "server.certificate.lifecycle", Subject: &Subject{Kind: "certificate", ID: "device-identity-issuer"}, CorrelationID: CorrelationID(ctx), Data: map[string]any{"operation": "activated", "certificate_id": "device-identity-issuer", "version": "revision_example", "outcome": "succeeded", "generation": 2, "kind": "issuer"}})
			default:
				resp, decodeErr := mdm.DecodeResponse([]byte(`<plist version="1.0"><dict><key>UDID</key><string>55693EB3-DF03-5FD1-9263-F7CDB8AD7FFD</string><key>CommandUUID</key><string>41d35de3-a343-4146-ba4b-0069bae2a54f</string><key>Status</key><string>Acknowledged</string><key>QueryResponses</key><dict><key>DeviceName</key><string>Example Mac</string></dict></dict></plist>`), "")
				if decodeErr != nil {
					t.Fatal(decodeErr)
				}
				err = s.CaptureOutcome(ctx, event.Event{Type: event.CommandResult, At: now, Enrollment: resp.ID, Actor: "device", Data: resp})
			}
			if err != nil {
				t.Fatal(err)
			}
			d := deliveries(t, s, c.Subscription.ID)
			if len(d) != 1 {
				t.Fatal(d)
			}
			if err := s.Send(t.Context(), d[0].ID); err != nil {
				t.Fatal(err)
			}
			if err := Verify(c.Credentials.SigningSecret, headers, body, now); err != nil {
				t.Fatal(err)
			}
			var e Event
			if err := json.Unmarshal(body, &e); err != nil {
				t.Fatal(err)
			}
			e.EventID = "evt_example_" + strings.ReplaceAll(name, "-", "_")
			assertReceiverExample(t, name, e)
		})
	}
}

// TestReplayedOutcomeReceiverExample checks replayed outcome receiver example.
func TestReplayedOutcomeReceiverExample(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	var received []byte
	var headers http.Header
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		received, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		headers = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	s := testStore(t, Config{Source: "mdm", Now: func() time.Time { return now }, Client: receiver.Client(), PrivateNetworks: []string{"127.0.0.0/8"}})
	c, err := s.Create(t.Context(), Spec{Name: "command-results", URL: receiver.URL, Events: []string{"server.command.result"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	response, err := mdm.DecodeResponse([]byte(`<plist version="1.0"><dict><key>UDID</key><string>55693EB3-DF03-5FD1-9263-F7CDB8AD7FFD</string><key>CommandUUID</key><string>41d35de3-a343-4146-ba4b-0069bae2a54f</string><key>Status</key><string>Acknowledged</string></dict></plist>`), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithCorrelation(t.Context(), "corr_example_exchange")
	if err := s.CaptureOutcome(ctx, event.Event{Type: event.CommandResult, At: now, Enrollment: response.ID, Actor: "device", Data: response}); err != nil {
		t.Fatal(err)
	}
	// A later configuration change cannot recover previously uncaptured data.
	spec := c.Subscription.Spec
	spec.Payload.FullJSON = true
	c, err = s.Update(t.Context(), c.Subscription.ID, c.Subscription.Revision, spec, true)
	if err != nil {
		t.Fatal(err)
	}
	var replay ReplayResult
	for _, key := range []string{"first-replay", "replay-again"} {
		replay, err = s.Replay(t.Context(), ReplayRequest{SubscriptionID: c.Subscription.ID, Key: key}, true)
		if err != nil || len(replay.DeliveryIDs) != 1 || len(replay.Missing) != 1 {
			t.Fatal(replay, err)
		}
	}
	if err := s.Send(t.Context(), replay.DeliveryIDs[0]); err != nil {
		t.Fatal(err)
	}
	if err := Verify(c.Credentials.SigningSecret, headers, received, now); err != nil {
		t.Fatal(err)
	}
	var e Event
	if err := json.Unmarshal(received, &e); err != nil {
		t.Fatal(err)
	}
	e.EventID = "evt_example_server_command_result_replay"
	assertReceiverExample(t, "server-command-result-replay", e)
}
