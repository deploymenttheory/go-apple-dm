// Package telemetry is the OpenTelemetry seam every other package
// instruments through: a Config carrying the providers, a Vocabulary that
// bounds an attribute to a closed set, and a RoundTripper that measures an
// outbound call.
//
// # Why
//
// Phase 9 of the plan of record needs the library to be observable without
// deciding anything for the consumer. Two rules follow from that, and this
// package exists to hold both in one place rather than repeat them in the
// eight Configs that will embed it.
//
// The first is that a library instruments through the API and never the SDK.
// OpenTelemetry's own guidance for instrumentation authors is that they
// "MUST NOT directly reference any SDK package of any kind, only the API",
// because the SDK is the application's choice: readers, exporters,
// samplers, resource attributes and cardinality limits all belong to whoever
// runs the process. So the dependency is exactly three stable v1.x modules —
// otel, otel/metric and otel/trace — and a nil provider means no-op rather
// than "use the global", because reading the global would let an unrelated
// dependency of the consumer switch this library on. Configure nothing and
// this library records nothing (decision record 0040).
//
// The second is that no string a device sent may become a metric attribute.
// A MessageType, a RequestType or a declaration reason code all arrive from
// the wire, and an attribute built straight from one is a time series count
// that a single malformed enrollment can grow without limit; an SDK answers
// that by discarding the overflow into a synthetic series, so the metric
// stops being trustworthy before anyone notices. Vocabulary is the bound: it
// is built from a set fixed at compile time, usually one of the generated
// registries in schema/, and maps everything else to OtherValue. Values that
// cannot be bounded — a UDID, a serial, a declaration identifier, an
// ErrorChain description — belong on a span, never on a metric.
//
// RoundTripper applies both rules to outbound HTTP. It records only the
// method, the server address and port, the status and the error type, and
// deliberately nothing from the URL path, query or body: an APNs push is a
// POST to /3/device/<device token>, so a path recorded anywhere in telemetry
// publishes the credential that wakes a device.
//
// The logs bridge is deliberately absent. Metrics and traces are stable
// v1.x; otel/log is v0.x with a policy that "anything MAY change at any
// time", so taking it into library packages would put a permanently
// unstable dependency in front of every consumer. The library keeps emitting
// slog with the Context variants it already uses, and internal/app — the
// reference server, which is allowed to be opinionated — wires the bridge.
//
// Fakes that record what was emitted are telemetry/telemetrytest.
//
// # References
//
//   - Decision record 0040: docs/research/decisions/0040-opentelemetry-seam.md
//   - Decision record 0037: docs/research/decisions/0037-event-sinks-and-redaction.md
//   - Plan of record: docs/research/implementation_plan.md (phase 9)
//   - OpenTelemetry: https://opentelemetry.io/docs/languages/go/libraries/
//   - OpenTelemetry: https://opentelemetry.io/docs/specs/semconv/http/http-metrics/
//   - OpenTelemetry: https://opentelemetry.io/docs/specs/otel/versioning-and-stability/
//   - Apple: https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns
package telemetry
