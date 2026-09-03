package telemetrytest

import (
	"context"
	"sort"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// Measurement is one recorded value with the attributes it carried.
type Measurement struct {
	// Scope is the instrumentation scope of the meter that produced it.
	Scope string
	// Instrument is the metric name.
	Instrument string
	// Value is the recorded number. Counters record their increment.
	Value float64
	// Attrs are the attributes as key=value strings, sorted, so a test can
	// compare them without depending on argument order.
	Attrs []string
}

// Attr returns the value of one attribute and whether it was present.
func (m Measurement) Attr(key string) (string, bool) {
	for _, kv := range m.Attrs {
		if k, v, ok := cut(kv); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func cut(s string) (k, v string, ok bool) {
	for i := range len(s) {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// Recorder is a metric.MeterProvider that keeps every measurement, for
// tests that need to assert what an instrument emitted — above all which
// attribute values reached it.
//
// Every type here embeds the corresponding no-op, which is what the
// OpenTelemetry API's stability policy requires of an implementation outside
// the SDK: the interfaces may gain methods in a minor release, and an
// embedded no-op absorbs them. Implementing them directly would break the
// build on an upgrade.
//
// It is safe for concurrent use.
type Recorder struct {
	embedded.MeterProvider

	mu   sync.Mutex
	got  []Measurement
	noop metric.MeterProvider
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{noop: metricnoop.NewMeterProvider()}
}

// Meter implements metric.MeterProvider.
func (r *Recorder) Meter(scope string, opts ...metric.MeterOption) metric.Meter {
	return &recordingMeter{Meter: r.noop.Meter(scope, opts...), rec: r, scope: scope}
}

// Measurements returns everything recorded so far, in order.
func (r *Recorder) Measurements() []Measurement {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Measurement, len(r.got))
	copy(out, r.got)
	return out
}

// Reset discards everything recorded so far.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = nil
}

// AttributeValues returns every distinct value seen for one attribute key
// across every instrument, sorted. It is the cardinality question: a key
// whose values grow with the fleet shows up here as a long list.
func (r *Recorder) AttributeValues(key string) []string {
	seen := map[string]bool{}
	for _, m := range r.Measurements() {
		if v, ok := m.Attr(key); ok {
			seen[v] = true
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// Instrument returns every measurement recorded for one instrument name.
func (r *Recorder) Instrument(name string) []Measurement {
	var out []Measurement
	for _, m := range r.Measurements() {
		if m.Instrument == name {
			out = append(out, m)
		}
	}
	return out
}

func (r *Recorder) add(scope, name string, value float64, set attribute.Set) {
	attrs := make([]string, 0, set.Len())
	for _, kv := range set.ToSlice() {
		attrs = append(attrs, string(kv.Key)+"="+kv.Value.Emit())
	}
	sort.Strings(attrs)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, Measurement{Scope: scope, Instrument: name, Value: value, Attrs: attrs})
}

// recordingMeter builds recording instruments, falling back to the embedded
// no-op meter for the instrument kinds no test needs yet.
type recordingMeter struct {
	metric.Meter
	rec   *Recorder
	scope string
}

func (m *recordingMeter) Float64Histogram(name string, _ ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	h, err := m.Meter.Float64Histogram(name)
	return &f64hist{Float64Histogram: h, m: m, name: name}, err
}

func (m *recordingMeter) Int64Counter(name string, _ ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	c, err := m.Meter.Int64Counter(name)
	return &i64counter{Int64Counter: c, m: m, name: name}, err
}

func (m *recordingMeter) Int64UpDownCounter(name string, _ ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	c, err := m.Meter.Int64UpDownCounter(name)
	return &i64updown{Int64UpDownCounter: c, m: m, name: name}, err
}

func (m *recordingMeter) Int64Gauge(name string, _ ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	g, err := m.Meter.Int64Gauge(name)
	return &i64gauge{Int64Gauge: g, m: m, name: name}, err
}

type f64hist struct {
	metric.Float64Histogram
	m    *recordingMeter
	name string
}

func (h *f64hist) Record(ctx context.Context, v float64, opts ...metric.RecordOption) {
	h.m.rec.add(h.m.scope, h.name, v, metric.NewRecordConfig(opts).Attributes())
	h.Float64Histogram.Record(ctx, v, opts...)
}

type i64counter struct {
	metric.Int64Counter
	m    *recordingMeter
	name string
}

func (c *i64counter) Add(ctx context.Context, v int64, opts ...metric.AddOption) {
	c.m.rec.add(c.m.scope, c.name, float64(v), metric.NewAddConfig(opts).Attributes())
	c.Int64Counter.Add(ctx, v, opts...)
}

type i64updown struct {
	metric.Int64UpDownCounter
	m    *recordingMeter
	name string
}

func (c *i64updown) Add(ctx context.Context, v int64, opts ...metric.AddOption) {
	c.m.rec.add(c.m.scope, c.name, float64(v), metric.NewAddConfig(opts).Attributes())
	c.Int64UpDownCounter.Add(ctx, v, opts...)
}

type i64gauge struct {
	metric.Int64Gauge
	m    *recordingMeter
	name string
}

func (g *i64gauge) Record(ctx context.Context, v int64, opts ...metric.RecordOption) {
	g.m.rec.add(g.m.scope, g.name, float64(v), metric.NewRecordConfig(opts).Attributes())
	g.Int64Gauge.Record(ctx, v, opts...)
}
