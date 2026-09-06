// Package telemetrytest provides recording OpenTelemetry providers, so a
// test can assert what an instrument emitted.
//
// # Why
//
// The property phase 9 has to keep is a negative one: that no device-supplied
// string ever reaches a metric attribute. Asserting a negative needs the
// attributes themselves, which the no-op providers discard and the SDK only
// exposes through a reader the library must not import. Recorder and
// SpanRecorder keep every measurement and span, and AttributeValues answers
// the cardinality question directly — a key whose values grow with the fleet
// shows up as a long list.
//
// Every type here embeds the corresponding no-op. That is what the
// OpenTelemetry API's stability policy requires of an implementation outside
// the SDK: the interfaces may gain methods in a minor release, and an
// embedded no-op absorbs them, where implementing them directly would break
// the build on an upgrade.
//
// # References
//
//   - Decision record 0040: docs/research/decisions/0040-opentelemetry-seam.md
//   - Plan of record: docs/research/implementation_plan.md (phase 9)
//   - OpenTelemetry: https://opentelemetry.io/docs/specs/otel/versioning-and-stability/
package telemetrytest
