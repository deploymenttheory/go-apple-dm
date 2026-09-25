package telemetrytest

import (
	"context"
	"sort"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Span is one recorded span.
type Span struct {
	// Scope is the instrumentation scope of the tracer that started it.
	Scope string
	// Name is the span name.
	Name string
	// Attrs are the attributes as sorted key=value strings.
	Attrs []string
	// Status and StatusMessage are what SetStatus recorded.
	Status        codes.Code
	StatusMessage string
	// Ended reports whether End was called.
	Ended bool
}

// Attr returns the value of one attribute and whether it was present.
func (s *Span) Attr(key string) (string, bool) {
	for _, kv := range s.Attrs {
		if k, v, ok := cut(kv); ok && k == key {
			return v, true
		}
	}
	return "", false
}

// SpanRecorder is a trace.TracerProvider that keeps every span. Like
// Recorder, every type embeds the corresponding no-op so the API may gain
// methods without breaking this package.
//
// It is safe for concurrent use.
type SpanRecorder struct {
	embedded.TracerProvider

	mu   sync.Mutex
	got  []*Span
	noop trace.TracerProvider
}

// NewSpanRecorder returns an empty SpanRecorder.
func NewSpanRecorder() *SpanRecorder {
	return &SpanRecorder{noop: tracenoop.NewTracerProvider()}
}

// Tracer implements trace.TracerProvider.
func (r *SpanRecorder) Tracer(scope string, opts ...trace.TracerOption) trace.Tracer {
	return &recordingTracer{Tracer: r.noop.Tracer(scope, opts...), rec: r, scope: scope}
}

// Spans returns every span started so far, in order.
func (r *SpanRecorder) Spans() []*Span {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Span, len(r.got))
	copy(out, r.got)
	return out
}

// Named returns every span with a given name.
func (r *SpanRecorder) Named(name string) []*Span {
	var out []*Span
	for _, s := range r.Spans() {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

type recordingTracer struct {
	trace.Tracer
	rec   *SpanRecorder
	scope string
}

// Start records a new test span and returns its span-bearing context.
func (t *recordingTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	ctx, inner := t.Tracer.Start(ctx, name, opts...)
	s := &Span{Scope: t.scope, Name: name}
	t.rec.mu.Lock()
	t.rec.got = append(t.rec.got, s)
	t.rec.mu.Unlock()
	rs := &recordingSpan{Span: inner, rec: t.rec, out: s}
	// The recording span is what the context carries, so code that finds the active
	// span through trace.SpanFromContext records against it as well.
	return trace.ContextWithSpan(ctx, rs), rs
}

type recordingSpan struct {
	trace.Span
	rec *SpanRecorder
	out *Span
}

// SetAttributes records attributes attached to the test span.
func (s *recordingSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.rec.mu.Lock()
	for _, kv := range attrs {
		s.out.Attrs = append(s.out.Attrs, string(kv.Key)+"="+kv.Value.String())
	}
	sort.Strings(s.out.Attrs)
	s.rec.mu.Unlock()
	s.Span.SetAttributes(attrs...)
}

// SetName records a rename, which a transport does once it knows the route.
func (s *recordingSpan) SetName(name string) {
	s.rec.mu.Lock()
	s.out.Name = name
	s.rec.mu.Unlock()
	s.Span.SetName(name)
}

// SetStatus records the span status code and description for assertions.
func (s *recordingSpan) SetStatus(code codes.Code, description string) {
	s.rec.mu.Lock()
	s.out.Status, s.out.StatusMessage = code, description
	s.rec.mu.Unlock()
	s.Span.SetStatus(code, description)
}

// End marks the recorded span complete and retains its final attributes.
func (s *recordingSpan) End(opts ...trace.SpanEndOption) {
	s.rec.mu.Lock()
	s.out.Ended = true
	s.rec.mu.Unlock()
	s.Span.End(opts...)
}
