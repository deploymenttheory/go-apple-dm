package telemetrytest_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/deploymenttheory/go-apple-mdm/telemetry"
	"github.com/deploymenttheory/go-apple-mdm/telemetry/telemetrytest"
)

// A fake that silently records nothing would make every assertion built on
// it pass vacuously — including the one that proves a device token never
// reaches telemetry. So the recorder is tested directly.
func TestRecorderCapturesEveryInstrumentKind(t *testing.T) {
	t.Parallel()
	rec := telemetrytest.NewRecorder()
	m := rec.Meter(telemetry.Scope("dep"))
	ctx := context.Background()
	attrs := metric.WithAttributes(attribute.String("outcome", "sent"))

	h, err := m.Float64Histogram("d")
	if err != nil {
		t.Fatal(err)
	}
	h.Record(ctx, 1.5, attrs)

	c, err := m.Int64Counter("c")
	if err != nil {
		t.Fatal(err)
	}
	c.Add(ctx, 2, attrs)

	u, err := m.Int64UpDownCounter("u")
	if err != nil {
		t.Fatal(err)
	}
	u.Add(ctx, -3, attrs)

	g, err := m.Int64Gauge("g")
	if err != nil {
		t.Fatal(err)
	}
	g.Record(ctx, 4, metric.WithAttributes(attribute.String("outcome", "rejected")))

	want := map[string]float64{"d": 1.5, "c": 2, "u": -3, "g": 4}
	got := rec.Measurements()
	if len(got) != len(want) {
		t.Fatalf("measurements = %d, want %d", len(got), len(want))
	}
	for name, v := range want {
		ms := rec.Instrument(name)
		if len(ms) != 1 || ms[0].Value != v {
			t.Errorf("%s = %+v, want value %v", name, ms, v)
		}
		if ms[0].Scope != telemetry.Scope("dep") {
			t.Errorf("%s scope = %q", name, ms[0].Scope)
		}
	}
	// The cardinality primitive: every distinct value seen for one key.
	if vals := rec.AttributeValues("outcome"); len(vals) != 2 || vals[0] != "rejected" || vals[1] != "sent" {
		t.Errorf("AttributeValues = %v", vals)
	}
	if vals := rec.AttributeValues("nosuchkey"); len(vals) != 0 {
		t.Errorf("AttributeValues(nosuchkey) = %v", vals)
	}
	if _, ok := rec.Instrument("d")[0].Attr("nosuchkey"); ok {
		t.Error("Attr reported a key that was never set")
	}
	rec.Reset()
	if len(rec.Measurements()) != 0 {
		t.Error("Reset left measurements behind")
	}
}

func TestSpanRecorderCapturesSpans(t *testing.T) {
	t.Parallel()
	rec := telemetrytest.NewSpanRecorder()
	tr := rec.Tracer(telemetry.Scope("push/apns"))
	_, span := tr.Start(context.Background(), "POST")
	span.SetAttributes(attribute.String("server.address", "api.push.apple.com"), attribute.Int("server.port", 443))
	span.SetStatus(codes.Error, "410")
	span.End()

	got := rec.Named("POST")
	if len(got) != 1 {
		t.Fatalf("spans = %d", len(got))
	}
	s := got[0]
	if s.Scope != telemetry.Scope("push/apns") || !s.Ended {
		t.Errorf("span = %+v", s)
	}
	if s.Status != codes.Error || s.StatusMessage != "410" {
		t.Errorf("status = %v %q", s.Status, s.StatusMessage)
	}
	if v, ok := s.Attr("server.address"); !ok || v != "api.push.apple.com" {
		t.Errorf("server.address = %q (present %v)", v, ok)
	}
	if _, ok := s.Attr("url.path"); ok {
		t.Error("Attr reported a key that was never set")
	}
	if len(rec.Named("GET")) != 0 {
		t.Error("Named matched a span that does not exist")
	}
	if len(rec.Spans()) != 1 {
		t.Errorf("Spans = %d", len(rec.Spans()))
	}
}
