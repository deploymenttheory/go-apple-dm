package telemetry_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/deploymenttheory/go-apple-dm/v3/telemetry"
	"github.com/deploymenttheory/go-apple-dm/v3/telemetry/telemetrytest"
)

// The contract a consumer relies on: configure nothing, pay nothing. A zero
// Config must hand back working no-op instruments rather than nil, and must
// not reach for the OpenTelemetry global, which would let an unrelated
// dependency switch this library on.
func TestZeroConfigIsNoOp(t *testing.T) {
	t.Parallel()
	var cfg telemetry.Config
	if cfg.Metrics() || cfg.Tracing() {
		t.Fatal("a zero Config reports itself as configured")
	}
	m := cfg.Meter("dep")
	if m == nil {
		t.Fatal("Meter returned nil")
	}
	h, err := m.Float64Histogram("x")
	if err != nil || h == nil {
		t.Fatalf("Float64Histogram = %v, %v", h, err)
	}
	h.Record(context.Background(), 1)

	tr := cfg.Tracer("dep")
	if tr == nil {
		t.Fatal("Tracer returned nil")
	}
	_, span := tr.Start(context.Background(), "x")
	span.SetAttributes(attribute.String("k", "v"))
	span.End()

	// With nothing configured the wrapper is not installed at all, so a
	// request pays no measurement cost and no extra indirection.
	base := http.DefaultTransport
	if got := telemetry.RoundTripper(base, cfg); got != base {
		t.Fatal("an unconfigured RoundTripper wrapped the transport anyway")
	}
	// A nil transport still resolves to something usable.
	if telemetry.RoundTripper(nil, cfg) == nil {
		t.Fatal("RoundTripper(nil) returned nil")
	}
}

func TestScope(t *testing.T) {
	t.Parallel()
	if got := telemetry.Scope("dep"); got != telemetry.ScopeRoot+"/dep" {
		t.Errorf("Scope(dep) = %q", got)
	}
	if got := telemetry.Scope(""); got != telemetry.ScopeRoot {
		t.Errorf("Scope() = %q, want the module root with no trailing slash", got)
	}
	if telemetry.Version() == "" {
		t.Error("Version is empty")
	}
}

// The rule from record 0037, enforced: a value that reaches us from a device
// must never become a metric attribute unchanged.
func TestVocabularyBoundsHostileValues(t *testing.T) {
	t.Parallel()
	v := telemetry.NewVocabulary("message_type", []string{"TokenUpdate", "Authenticate", "TokenUpdate"})
	if got := v.Values(); len(got) != 2 || got[0] != "Authenticate" || got[1] != "TokenUpdate" {
		t.Fatalf("Values = %v, want the duplicate collapsed and sorted", got)
	}
	if v.Cardinality() != 3 {
		t.Errorf("Cardinality = %d, want the set plus %s", v.Cardinality(), telemetry.OtherValue)
	}
	if got := v.Attr("TokenUpdate").Value.AsString(); got != "TokenUpdate" {
		t.Errorf("a known value became %q", got)
	}
	for _, hostile := range []string{"", strings.Repeat("A", 4096), "TokenUpdate\x00", "../../etc/passwd"} {
		if got := v.Attr(hostile).Value.AsString(); got != telemetry.OtherValue {
			t.Errorf("hostile value %q became %q", hostile, got)
		}
		if v.Allows(hostile) {
			t.Errorf("Allows(%q) = true", hostile)
		}
	}
	if v.Key() != "message_type" {
		t.Errorf("Key = %q", v.Key())
	}
	// Values is a copy: a caller cannot widen the set through it.
	got := v.Values()
	got[0] = "Injected"
	if v.Allows("Injected") {
		t.Fatal("the vocabulary was widened through Values")
	}
	// An empty vocabulary is usable, and admits nothing.
	empty := telemetry.NewVocabulary("k", nil)
	if empty.Attr("anything").Value.AsString() != telemetry.OtherValue || empty.Cardinality() != 1 {
		t.Error("an empty vocabulary should map everything to the other value")
	}
}

// An APNs push is POST /3/device/<device token>. The token is the credential
// that wakes a device, so it must not reach a metric, a span, or a span
// name — this is the assertion that keeps a future edit from adding url.path
// "just for debugging".
func TestPushTokenNeverReachesTelemetry(t *testing.T) {
	t.Parallel()
	const token = "b0aa1e5f9d3c4718bb6d2f0a9c7e4413aa55ef0011223344556677889900aabb"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec, spans := telemetrytest.NewRecorder(), telemetrytest.NewSpanRecorder()
	cfg := telemetry.Config{MeterProvider: rec, TracerProvider: spans}
	c := &http.Client{Transport: telemetry.RoundTripper(nil, cfg, telemetry.WithScope("push/apns"))}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/3/device/"+token+"?secret=alsohere", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	measurements := rec.Instrument(telemetry.MetricHTTPClientDuration)
	if len(measurements) != 1 {
		t.Fatalf("measurements = %d, want 1", len(measurements))
	}
	for _, m := range measurements {
		for _, kv := range m.Attrs {
			if strings.Contains(kv, token) || strings.Contains(kv, "secret") {
				t.Errorf("the URL leaked into a metric attribute: %q", kv)
			}
		}
	}
	got := spans.Spans()
	if len(got) != 1 {
		t.Fatalf("spans = %d, want 1", len(got))
	}
	if strings.Contains(got[0].Name, token) {
		t.Errorf("the token leaked into the span name: %q", got[0].Name)
	}
	for _, kv := range got[0].Attrs {
		if strings.Contains(kv, token) || strings.Contains(kv, "secret") {
			t.Errorf("the URL leaked into a span attribute: %q", kv)
		}
	}
	if !got[0].Ended {
		t.Error("the span was never ended")
	}
}

func TestRoundTripperRecordsTheStableAttributes(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	rec, spans := telemetrytest.NewRecorder(), telemetrytest.NewSpanRecorder()
	cfg := telemetry.Config{MeterProvider: rec, TracerProvider: spans}
	// A fake clock so the recorded duration is exact rather than flaky.
	var now time.Time
	c := &http.Client{Transport: telemetry.RoundTripper(nil, cfg,
		telemetry.WithScope("dep"),
		telemetry.WithAttributes(attribute.String("apple.service", "dep")),
		telemetry.WithNow(func() time.Time { now = now.Add(1500 * time.Millisecond); return now }),
	)}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	m := rec.Instrument(telemetry.MetricHTTPClientDuration)[0]
	if m.Scope != telemetry.Scope("dep") {
		t.Errorf("scope = %q", m.Scope)
	}
	if m.Value != 1.5 {
		t.Errorf("duration = %v seconds, want 1.5", m.Value)
	}
	for key, want := range map[string]string{
		telemetry.AttrHTTPRequestMethod:      http.MethodPost,
		telemetry.AttrHTTPResponseStatusCode: "418",
		telemetry.AttrServerAddress:          "127.0.0.1",
		telemetry.AttrErrorType:              "418",
		"apple.service":                      "dep",
	} {
		got, ok := m.Attr(key)
		if !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", key, got, ok, want)
		}
	}
	if _, ok := m.Attr(telemetry.AttrServerPort); !ok {
		t.Error("server.port missing")
	}
	// A 4xx is an error on the span as well as an attribute.
	if s := spans.Named(http.MethodPost); len(s) != 1 || s[0].Status != codes.Error {
		t.Errorf("span status = %+v", s)
	}
}

// A method we do not recognise comes from a caller, so it is bounded like
// any other string rather than trusted onto a label.
func TestRoundTripperBoundsTheMethod(t *testing.T) {
	t.Parallel()
	rec := telemetrytest.NewRecorder()
	rt := telemetry.RoundTripper(errTransport{err: errors.New("nope")}, telemetry.Config{MeterProvider: rec})
	req, err := http.NewRequestWithContext(context.Background(), "WHAT", "https://example.test:8443/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("the transport error was swallowed")
	}
	m := rec.Instrument(telemetry.MetricHTTPClientDuration)[0]
	if got, _ := m.Attr(telemetry.AttrHTTPRequestMethod); got != telemetry.OtherValue {
		t.Errorf("method = %q, want %q", got, telemetry.OtherValue)
	}
	if got, _ := m.Attr(telemetry.AttrServerPort); got != "8443" {
		t.Errorf("server.port = %q", got)
	}
	if got, _ := m.Attr(telemetry.AttrErrorType); got != "error" {
		t.Errorf("error.type = %q", got)
	}
	if _, ok := m.Attr(telemetry.AttrHTTPResponseStatusCode); ok {
		t.Error("a failed request recorded a status code")
	}
}

// error.type is a closed vocabulary too: an error message can carry a
// hostname, a URL or a token, and is unbounded besides.
func TestErrorTypeIsBounded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()

	for name, tc := range map[string]struct {
		ctx  context.Context
		err  error
		want string
	}{
		"Cancelled": {ctx, context.Canceled, "context_canceled"},
		"Deadline":  {deadline, context.DeadlineExceeded, "timeout"},
		"Timeout":   {context.Background(), timeoutErr{}, "timeout"},
		"Other":     {context.Background(), errors.New("connection refused to 10.0.0.1"), "error"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := telemetrytest.NewRecorder()
			rt := telemetry.RoundTripper(errTransport{err: tc.err}, telemetry.Config{MeterProvider: rec})
			req, err := http.NewRequestWithContext(tc.ctx, http.MethodGet, "https://example.test/x", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rt.RoundTrip(req); err == nil {
				t.Fatal("no error")
			}
			m := rec.Instrument(telemetry.MetricHTTPClientDuration)[0]
			got, _ := m.Attr(telemetry.AttrErrorType)
			if got != tc.want {
				t.Errorf("error.type = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "10.0.0.1") {
				t.Error("the error message leaked into the attribute")
			}
		})
	}
}

// A request with no URL must not panic the wrapper.
func TestRoundTripperSurvivesAnEmptyURL(t *testing.T) {
	t.Parallel()
	rec := telemetrytest.NewRecorder()
	rt := telemetry.RoundTripper(errTransport{err: errors.New("x")}, telemetry.Config{MeterProvider: rec})
	req := &http.Request{Method: http.MethodGet}
	if _, err := rt.RoundTrip(req.WithContext(context.Background())); err == nil {
		t.Fatal("no error")
	}
	m := rec.Instrument(telemetry.MetricHTTPClientDuration)[0]
	if _, ok := m.Attr(telemetry.AttrServerAddress); ok {
		t.Error("server.address recorded for a request with no URL")
	}
}

type errTransport struct{ err error }

func (e errTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.err }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

// server.port is required by the convention even when the URL omits it, so
// the scheme's default stands in — and a scheme with no default records no
// port rather than a wrong one.
func TestServerPortDefaultsToTheScheme(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		url  string
		want string
	}{
		"HTTPS": {"https://example.test/x", "443"},
		"HTTP":  {"http://example.test/x", "80"},
		"None":  {"ftp://example.test/x", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := telemetrytest.NewRecorder()
			rt := telemetry.RoundTripper(errTransport{err: errors.New("x")}, telemetry.Config{MeterProvider: rec})
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rt.RoundTrip(req); err == nil {
				t.Fatal("no error")
			}
			m := rec.Instrument(telemetry.MetricHTTPClientDuration)[0]
			got, ok := m.Attr(telemetry.AttrServerPort)
			if tc.want == "" {
				if ok {
					t.Errorf("server.port = %q, want it absent", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("server.port = %q, want %q", got, tc.want)
			}
		})
	}
}
