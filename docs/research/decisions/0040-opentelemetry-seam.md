# 0040: An OpenTelemetry seam the consumer owns

## Context

Library consumers need optional metrics and traces without adopting a global provider, exporter or unbounded device-derived attributes.

## Decision

`telemetry.Config` accepts explicit metric and trace providers and uses no-op providers when absent. An unconfigured `RoundTripper` returns the supplied transport without wrapping it. Instrumentation records bounded method, server, status and error categories; paths, query strings, bodies and error messages are excluded.

`Vocabulary` maps values outside a fixed set to `OtherValue`. Test recorders implement the provider APIs without requiring the OpenTelemetry SDK. The library depends on metric/trace APIs and does not install exporters or a logs bridge.

## Rationale

Explicit configuration leaves observability ownership with the embedding application. Bounded attributes limit cardinality and prevent APNs device tokens in URL paths from entering telemetry.

## Constraints

Consumers supply SDKs, readers/exporters and any slog bridge. Server addresses remain attributes, so deployments should use a bounded destination set. No-op defaults do not establish that every domain is instrumented.

## Verification

Telemetry tests cover zero configuration, transport behavior, method/error bounds and hostile vocabulary values. A push request containing sentinel secrets must expose none in attributes or span names. Recorder tests verify captured instruments and spans.

## References

- [telemetry](../../../telemetry)
- [telemetry/telemetrytest](../../../telemetry/telemetrytest)
- [go.mod](../../../go.mod)
- <https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns>
- <https://developer.apple.com/documentation/devicemanagement/check-in>

Reference source identifiers and paths (relative to the named project):

- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`, `pkg/fleethttp/fleethttp.go:253`
- `cmd/fleet/mdm_apple.go:66-73`, `cmd/fleet/otel.go:95,140`, `server/service/handler.go:163-173,210-284,1607`
- `server/contexts/ctxerr/metrics.go:10,22`, `server/contexts/ctxerr/ctxerr.go:317,334-337`
- `server/datastore/mysqlredis/metrics.go:32-71`, `server/service/middleware/otel/otel.go:12-45`
- `server/mdm/nanomdm/push/nanopush/provider.go:73`
- `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.61.0`
- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7`
- `zentral/utils/prometheus.py:39-51`, `zentral/contrib/mdm/metrics_views.py:15-121`
- `zentral/contrib/mdm/workers.py:86-90,138-142`, `server/base/management/commands/runworker.py:50-64`
- `zentral/contrib/mdm/apns.py:26-56`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10`, `cmd/nanomdm/main.go:281-284`
- `push/nanopush/nanopush.go:17-18,81-87`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107`, `platform/apns/push.go:138-146`
- `micromdm/nanodep@2223746268b832f70be50f9ca27428a7785531be`, `client/client.go:83-87`, `client/transport.go:111`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667`, `micromdm/nanocmd@f1302b5fc5684d3b0ad2ee5f2aa5f2c0ca9bd098`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151`
