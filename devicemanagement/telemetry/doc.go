// Package telemetry provides explicit OpenTelemetry configuration, bounded
// vocabularies and outbound HTTP measurement.
//
// # Design
//
// Config accepts metric/trace providers and defaults to no-op implementations
// without reading global providers. RoundTripper returns the original transport
// when unconfigured; otherwise it records bounded method, server, status and
// error categories. URL paths, query strings, bodies and error messages are
// excluded from metrics and spans.
//
// Vocabulary maps values outside a fixed set to OtherValue. Consumers own SDKs,
// exporters, sampling and any slog bridge; the library installs none. Recording
// fakes in telemetry/telemetrytest let tests inspect emitted attributes and
// verify that secrets are absent.
//
// # References
//
//   - Decision record 0040: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0040-opentelemetry-seam.md
//   - Decision record 0037: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0037-event-sinks-and-redaction.md
//   - OpenTelemetry: https://opentelemetry.io/docs/languages/go/libraries/
//   - OpenTelemetry: https://opentelemetry.io/docs/specs/semconv/http/http-metrics/
//   - OpenTelemetry: https://opentelemetry.io/docs/specs/otel/versioning-and-stability/
//   - Apple: https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns
package telemetry
