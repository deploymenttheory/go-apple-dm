package telemetry

import (
	"runtime/debug"
	"sync"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// ScopeRoot is the module path every instrumentation scope is rooted at.
const ScopeRoot = "github.com/deploymenttheory/go-apple-dm"

// Scope returns the instrumentation scope name for a package, which
// OpenTelemetry defines as the fully qualified name of the instrumenting
// library: Scope("dep") is the scope for package dep.
//
// An empty pkg returns ScopeRoot, so a caller that has no sub-package to
// name still produces a valid scope rather than a trailing slash.
func Scope(pkg string) string {
	if pkg == "" {
		return ScopeRoot
	}
	return ScopeRoot + "/" + pkg
}

// version reports this module's version for WithInstrumentationVersion, so a
// consumer can tell which build of the library produced a series. It is
// resolved once: debug.ReadBuildInfo walks the module graph.
var version = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	for _, d := range info.Deps {
		if d.Path == ScopeRoot && d.Version != "" {
			return d.Version
		}
	}
	// Built from this module itself, as tests and the reference server are.
	if info.Main.Path == ScopeRoot && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
})

// Version returns the library version reported on every meter and tracer.
func Version() string { return version() }

// Config carries the OpenTelemetry providers a package should instrument
// through. It is embedded in each package's own Config beside Logger, Clock
// and Bus, and every field is optional.
//
// A nil provider means no-op, not "use the global". Reading
// otel.GetMeterProvider() would let a consumer's unrelated global
// configuration silently switch this library on, which is the opposite of
// the contract: unless a consumer passes a provider here, this library
// records nothing and allocates nothing.
type Config struct {
	// MeterProvider supplies meters. Nil records no metrics.
	MeterProvider metric.MeterProvider
	// TracerProvider supplies tracers. Nil records no spans.
	TracerProvider trace.TracerProvider
}

// Meter returns the meter for a package, never nil.
func (c Config) Meter(pkg string) metric.Meter {
	if c.MeterProvider == nil {
		return metricnoop.NewMeterProvider().Meter(Scope(pkg))
	}
	return c.MeterProvider.Meter(Scope(pkg), metric.WithInstrumentationVersion(Version()))
}

// Tracer returns the tracer for a package, never nil.
func (c Config) Tracer(pkg string) trace.Tracer {
	if c.TracerProvider == nil {
		return tracenoop.NewTracerProvider().Tracer(Scope(pkg))
	}
	return c.TracerProvider.Tracer(Scope(pkg), trace.WithInstrumentationVersion(Version()))
}

// Metrics reports whether a consumer asked for metrics. Instrument
// construction is cheap but not free, so a package that would build many
// instruments may skip the work entirely.
func (c Config) Metrics() bool { return c.MeterProvider != nil }

// Tracing reports whether a consumer asked for traces.
func (c Config) Tracing() bool { return c.TracerProvider != nil }
