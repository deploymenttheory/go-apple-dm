package telemetry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Attribute keys from OpenTelemetry's HTTP semantic conventions.
//
// Only the stable subset is used. They are named here rather than imported
// from a semconv package: those are published one import path per
// specification release (.../semconv/v1.42.0, v1.43.0, and so on), so an
// import pins a spec version in our source and every otel upgrade is then a
// choice between editing the path and going stale. It saves no dependency —
// otel/trace already links a semconv package — but the five names below are
// in the stable set, whose spelling cannot change without a major version of
// the specification, so pinning buys nothing.
const (
	AttrHTTPRequestMethod      = "http.request.method"
	AttrHTTPResponseStatusCode = "http.response.status_code"
	AttrServerAddress          = "server.address"
	AttrServerPort             = "server.port"
	AttrErrorType              = "error.type"
)

// MetricHTTPClientDuration is the stable OpenTelemetry instrument for an
// outbound request, measured in seconds.
const MetricHTTPClientDuration = "http.client.request.duration"

// httpMethods is the closed set OpenTelemetry defines for
// http.request.method. Anything else becomes OtherValue, because the method
// reaches us from a caller and a metric label must not.
var httpMethods = NewVocabulary(AttrHTTPRequestMethod, []string{
	http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead,
	http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut,
	http.MethodTrace,
})

// durationBuckets are the boundaries OpenTelemetry recommends for the two
// stable HTTP duration histograms, in seconds. They are advisory: an SDK
// with its own view overrides them.
var durationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10,
}

// rtOptions configure a RoundTripper.
type rtOptions struct {
	scope string
	now   func() time.Time
	extra []attribute.KeyValue
}

// Option configures a RoundTripper.
type Option func(*rtOptions)

// WithScope sets the instrumentation scope, which should be the package
// doing the calling: WithScope("dep") for the DEP client. It defaults to the
// module root, which is correct but tells a reader nothing.
func WithScope(pkg string) Option { return func(o *rtOptions) { o.scope = pkg } }

// WithNow replaces the clock, for tests. internal/clock is not importable by
// a consumer, so the seam is a plain function.
func WithNow(now func() time.Time) Option {
	return func(o *rtOptions) {
		if now != nil {
			o.now = now
		}
	}
}

// WithAttributes adds constant attributes to every measurement. They must be
// fixed at configuration time: an attribute whose value varies per request
// multiplies the series count by its range, and one that varies per device
// is unbounded.
func WithAttributes(attrs ...attribute.KeyValue) Option {
	return func(o *rtOptions) { o.extra = append(o.extra, attrs...) }
}

// roundTripper instruments an http.RoundTripper.
type roundTripper struct {
	next     http.RoundTripper
	duration metric.Float64Histogram
	tracer   trace.Tracer
	opts     rtOptions
}

// RoundTripper wraps next so every outbound request records
// http.client.request.duration and, when a TracerProvider is configured, one
// span. A nil next uses http.DefaultTransport.
//
// It records the request method, the server address and port, the response
// status and the error type — and deliberately nothing drawn from the URL
// path, query or body, on the span as much as on the metric. That is not
// only a cardinality rule: an APNs push is a POST to /3/device/<device
// token>, so a path recorded anywhere in telemetry publishes the credential
// that wakes a device to every backend the traces reach.
//
// Errors are returned unchanged, so the wrapper is transparent to callers.
func RoundTripper(next http.RoundTripper, cfg Config, opts ...Option) http.RoundTripper {
	o := rtOptions{now: time.Now}
	for _, fn := range opts {
		fn(&o)
	}
	if next == nil {
		next = http.DefaultTransport
	}
	// Nothing configured means nothing to do, and the caller keeps the
	// transport it passed in rather than an indirection that measures into a
	// no-op on every request.
	if !cfg.Metrics() && !cfg.Tracing() {
		return next
	}
	rt := &roundTripper{next: next, tracer: cfg.Tracer(o.scope), opts: o}
	rt.duration, _ = cfg.Meter(o.scope).Float64Histogram(
		MetricHTTPClientDuration,
		metric.WithUnit("s"),
		metric.WithDescription("Duration of an outbound HTTP request."),
		metric.WithExplicitBucketBoundaries(durationBuckets...),
	)
	return rt
}

// RoundTrip implements http.RoundTripper.
func (r *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	method := httpMethods.Attr(req.Method)
	attrs := make([]attribute.KeyValue, 0, len(r.opts.extra)+5)
	attrs = append(attrs, r.opts.extra...)
	attrs = append(attrs, method)
	attrs = append(attrs, hostAttrs(req)...)

	ctx := req.Context()
	// The span name is the method alone. Semantic conventions ask for a
	// low-cardinality name and there is no route template for a client call,
	// so the target must not appear in it.
	ctx, span := r.tracer.Start(ctx, method.Value.AsString(), trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()
	req = req.WithContext(ctx)

	start := r.opts.now()
	resp, err := r.next.RoundTrip(req)
	elapsed := r.opts.now().Sub(start).Seconds()

	switch {
	case err != nil:
		attrs = append(attrs, attribute.String(AttrErrorType, errorType(err)))
		span.SetStatus(codes.Error, errorType(err))
	case resp != nil:
		attrs = append(attrs, attribute.Int(AttrHTTPResponseStatusCode, resp.StatusCode))
		// Semantic conventions: a client span is an error for 4xx and 5xx,
		// because the caller asked for something it did not get.
		if resp.StatusCode >= 400 {
			attrs = append(attrs, attribute.String(AttrErrorType, strconv.Itoa(resp.StatusCode)))
			span.SetStatus(codes.Error, strconv.Itoa(resp.StatusCode))
		}
	}
	span.SetAttributes(attrs...)
	if r.duration != nil {
		// ctx is threaded through so an SDK that also traces can attach an
		// exemplar linking this measurement to the span above.
		r.duration.Record(ctx, elapsed, metric.WithAttributes(attrs...))
	}
	return resp, err
}

// hostAttrs returns the server address and port, which are configuration
// rather than device input and so are safe to label with.
func hostAttrs(req *http.Request) []attribute.KeyValue {
	if req.URL == nil {
		return nil
	}
	host, port := req.URL.Hostname(), req.URL.Port()
	if port == "" {
		port = defaultPort(req.URL.Scheme)
	}
	out := make([]attribute.KeyValue, 0, 2)
	if host != "" {
		out = append(out, attribute.String(AttrServerAddress, host))
	}
	if n, err := strconv.Atoi(port); err == nil {
		out = append(out, attribute.Int(AttrServerPort, n))
	}
	return out
}

func defaultPort(scheme string) string {
	switch scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// errorType names the failure in the closed way error.type requires: a type
// name or a well-known cause, never an error message. A message can carry a
// hostname, a URL, or a device token, and would be unbounded besides.
func errorType(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	}
	return "error"
}
