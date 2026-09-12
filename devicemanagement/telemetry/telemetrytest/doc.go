// Package telemetrytest records OpenTelemetry measurements and spans for
// assertions.
//
// # Design
//
// Recorder and SpanRecorder expose what instruments emit so tests can verify
// values, cardinality and excluded sensitive attributes. Implementations embed
// no-op API types to accommodate additive interface methods. Recorder behavior
// is tested directly so a missing measurement cannot make a negative disclosure
// assertion pass without exercising instrumentation. No exporter or
// OpenTelemetry SDK is required.
//
// # References
//
//   - Decision record 0040: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0040-opentelemetry-seam.md
//   - OpenTelemetry: https://opentelemetry.io/docs/specs/otel/versioning-and-stability/
package telemetrytest
