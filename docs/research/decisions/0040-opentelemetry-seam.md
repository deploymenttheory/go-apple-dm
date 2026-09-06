# 0040: An OpenTelemetry seam the consumer owns

Status: accepted
Date: 2026-09-03
Phase: 9

## Apple sources

- Doc: <https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns> —
  the push request is `POST /3/device/<device token>`, which is why a URL path may not be recorded.
- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> — `MessageType` arrives
  from the device, so it is device-supplied input wherever it is used as a label.

## References read

- OpenTelemetry: <https://opentelemetry.io/docs/languages/go/libraries/> (instrumentation authors
  "MUST NOT directly reference any SDK package of any kind, only the API"),
  <https://opentelemetry.io/docs/specs/otel/versioning-and-stability/>,
  <https://opentelemetry.io/docs/specs/semconv/http/http-metrics/>
- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a` `pkg/fleethttp/fleethttp.go:253`,
  `cmd/fleet/mdm_apple.go:66-73`, `cmd/fleet/otel.go:95,140`, `server/service/handler.go:163-173,210-284,1607`,
  `server/contexts/ctxerr/metrics.go:10,22`, `server/contexts/ctxerr/ctxerr.go:317,334-337`,
  `server/datastore/mysqlredis/metrics.go:32-71`, `server/service/middleware/otel/otel.go:12-45`,
  `server/mdm/nanomdm/push/nanopush/provider.go:73`
- `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.61.0` (the version Fleet pins)
  `internal/semconv/httpconv.go:388-398`, `internal/semconv/env.go:211-227`
- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7`
  `zentral/utils/prometheus.py:39-51`, `zentral/contrib/mdm/metrics_views.py:15-121`,
  `zentral/contrib/mdm/workers.py:86-90,138-142`, `server/base/management/commands/runworker.py:50-64`,
  `zentral/contrib/mdm/apns.py:26-56`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10` `cmd/nanomdm/main.go:281-284`,
  `push/nanopush/nanopush.go:17-18,81-87`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107` `platform/apns/push.go:138-146`
- `micromdm/nanodep@2223746268b832f70be50f9ca27428a7785531be` `client/client.go:83-87`, `client/transport.go:111`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667`, `micromdm/nanocmd@f1302b5fc5684d3b0ad2ee5f2aa5f2c0ca9bd098`,
  `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151` (searched, nothing found — see below)

## Known pitfalls found

- **Fleet publishes APNs push tokens into its traces.** The chain is four links, each verified:
  `fleethttp.NewClient` ends with `cli.Transport = otelhttp.NewTransport(baseTransport)`
  (`fleethttp.go:253`) — unconditionally, not gated on `OTELEnabled()`; the APNs pusher is built from
  that exact constructor (`cmd/fleet/mdm_apple.go:69-72`); the request it makes is
  `p.baseURL + "/3/device/" + pushInfo.Token.String()` (`nanopush/provider.go:73`); and at the
  `otelhttp` version Fleet pins, `RequestTraceAttrs` appends `semconvNew.URLFull(u)` having removed
  only `req.URL.User` (`internal/semconv/httpconv.go:388-398`), on a client path with no
  `OTEL_SEMCONV_STABILITY_OPT_IN` gate (`internal/semconv/env.go:211-227`). So with OpenTelemetry
  enabled — a supported configuration, not a misconfiguration — every push emits a span whose
  `url.full` is the device's push token. The same client is used for DEP and ABM
  (`server/mdm/apple/apple_mdm.go:1063`). This is the single strongest reason not to hand consumers
  `otelhttp.NewTransport` and call the job done.
- **`otelhttp` is v0.x contrib.** It moves independently of the v1.x API and carries no stability
  guarantee, so taking it into library packages would put an unstable module in front of every
  consumer — for a wrapper whose useful part is about a hundred lines.
- **Reaching for the OpenTelemetry global turns instrumentation on by accident.** Fleet's
  library-shaped packages do exactly this: `var meter = otel.Meter("fleet")` at package scope with an
  `init()` that builds instruments and `panic(err)`s on failure (`ctxerr/metrics.go:10,22`;
  `mysqlredis/metrics.go:32-59`), and Prometheus goes to `prometheus.DefaultRegisterer` hardcoded
  (`handler.go:211`) with an `AlreadyRegisteredError` dance to survive double registration. For an
  application that is a reasonable trade. For a library it means a consumer's unrelated
  `otel.SetMeterProvider` silently starts our emission.
- **`otel/log` is not ready.** It is v0.x whose policy says anything may change at any time, and
  `otelslog` is likewise v0.x contrib. A logs bridge in library packages would be a permanently
  unstable dependency.
- **Nobody instruments the MDM protocol itself.** No `MessageType` or `RequestType` counter exists in
  any of the eight references. Fleet's Apple MDM endpoint is registered on a plain `*http.ServeMux`
  (`handler.go:1607`) rather than the gorilla router its Prometheus middleware walks, so the check-in
  and connect path has a span and **no counter at all**. The cardinality question is therefore ours
  to answer first: the wire value is device-supplied and must be allowlisted, not passed through.
- **The Go MDM ecosystem has no observability seam to differ from.** nanomdm, micromdm, nanohub,
  nanocmd, nanodep and kmfddm define no metrics, expose no `/metrics`, and create no spans. Their
  entire instrumentation surface is a random 8-byte hex request ID in a log line, alongside the same
  `// ... would be better served by ... https://opentelemetry.io/ someday.` comment duplicated in
  five `main.go` files. micromdm's go.sum mentions Prometheus only as module-graph hashes.
- **nanohub's library purity is accidental.** It imports nanomdm, nanocmd and kmfddm, and nothing
  leaks — because none of them measures anything. The property this record wants is real there, but
  nothing defends it.
- **`internal/clock` is not importable by a consumer**, so a consumer writing an instrument cannot
  fake time through our seams. The RoundTripper takes `WithNow(func() time.Time)` rather than a
  `clock.Clock` for that reason.

## What they do

- **Fleet**: three stacks at once — Prometheus on the default registry, OpenTelemetry through the
  globals, and Elastic APM as the fallback when OTel is off. Metric labels are `handler`, `method`,
  `code`, `error`, `mode`, `result`, `kind`, `op`, `reason`, `error.type`, `http.route`; none is
  device-supplied, and `error.type` is `fmt.Sprintf("%T", Cause(cause))` — the Go type, not the
  message (`ctxerr.go:317`), which is the right way to label an error. Full error text and stack do
  go on span attributes (`ctxerr.go:334-337`). `server/service/middleware/otel.WrapHandler` returns
  the bare handler when OTel is off, which is the nearest thing in any reference to no-op-by-default,
  but it is a config flag rather than a provider seam.
- **Zentral**: the better precedent, and not OpenTelemetry. Its scrape views build a fresh
  `CollectorRegistry()` per request rather than touching the global (`utils/prometheus.py:39-51`),
  and its workers take a duck-typed `metrics_exporter` argument defaulting to `None`, with
  Prometheus and statsd implementations chosen by the entrypoint (`workers.py:86-90,138-142`;
  `runworker.py:50-64`). Labels are bounded by enums and operator-created names; no UDID, serial or
  token appears in any of them. Its APNs client is a plain `httpx.Client` with no timing at all, and
  the token appears only in a debug log.
- **nanomdm, micromdm, nanohub, nanocmd, nanodep, kmfddm**: nothing. micromdm's go-kit logging
  middleware records `"udid", udid` and a duration as *log fields* (`platform/apns/push.go:138-146`),
  which is the closest any of them comes to a measurement.

## What we do better

1. **Configure nothing, pay nothing — and a nil provider means no-op, not "use the global".**
   `Config` holds the two providers and defaults to the `noop` packages, so an unrelated
   `otel.SetMeterProvider` in a consumer's process cannot switch this library on. Fleet's
   package-scope `otel.Meter("fleet")` does the opposite by construction. `RoundTripper` goes further
   and returns the caller's own transport unwrapped when neither provider is set, so an unconfigured
   deployment does not even pay the indirection.
2. **Three stable modules, and the SDK is not among them.** `otel`, `otel/metric` and `otel/trace`,
   all v1.46.0. Fourteen packages reach the binary and the only non-OTel one is `cespare/xxhash`,
   pulled by `otel/attribute` for attribute-set hashing; `otel/internal/global`,
   `go.opentelemetry.io/auto/sdk` and `go-logr` are all absent, which is checked rather than assumed.
3. **A URL never becomes an attribute — on a metric or a span.** `RoundTripper` records the method,
   server address and port, status and error type, and nothing drawn from the path, query or body.
   That is not merely a cardinality rule: it is the difference between our transport and the one
   Fleet ships, which publishes device push tokens into traces in a supported configuration.
4. **Bounding a label is a type, not a convention.** `Vocabulary` is built from a set fixed at
   compile time — usually one of the generated registries in `schema/` — and maps everything else to
   `OtherValue`, so a caller labels freely without auditing the call site. Fleet and Zentral both
   keep device strings out of labels by discipline; nothing in either enforces it, and neither has a
   `MessageType`-shaped label to be disciplined about.
5. **`error.type` is bounded too.** A cancelled context, a timeout and everything else, never the
   error message — which may carry a hostname, a URL or a token, and is unbounded besides. Fleet
   reaches the same conclusion with `%T`; nobody else labels errors at all.
6. **The fakes are part of the deliverable.** `telemetry/telemetrytest` records measurements and
   spans so a test can assert the negative — that no device string reached an attribute — which the
   no-op providers discard and the SDK exposes only through a reader the library must not import.
   Every type embeds the corresponding no-op, as the API's stability policy requires of an
   implementation outside the SDK.
7. **The logs bridge is deliberately out.** Metrics and traces are stable v1.x; `otel/log` is v0.x.
   The library keeps emitting `slog` with the `Context` variants it already uses and `internal/app`
   wires the bridge, so no consumer inherits an unstable module from us.

## Verified by

1. `telemetry.TestZeroConfigIsNoOp` (a zero Config reports itself unconfigured, returns working
   no-op instruments rather than nil, and leaves the caller's transport unwrapped) (proves claim 1;
   would fail on any implementation reading `otel.GetMeterProvider()`).
2. `go list -deps ./telemetry/` (proves claim 2; recorded here as fourteen packages, one non-OTel).
3. `telemetry.TestPushTokenNeverReachesTelemetry` (drives a request to
   `/3/device/<token>?secret=...` and asserts the token appears in no metric attribute, no span
   attribute, and not in the span name) (proves claim 3). The same assertion would fail against
   `otelhttp`: its client path appends `URLFull(u)` after stripping only `req.URL.User`, read in the
   v0.61.0 source cited above. That is a deduction from their source, not a run of this test against
   their transport, which would mean taking the dependency this record declines.
4. `telemetry.TestVocabularyBoundsHostileValues` (an empty value, a 4KiB value, an embedded NUL and
   a traversal string all become `OtherValue`; `Values` is a copy so the set cannot be widened
   through it) and `TestRoundTripperBoundsTheMethod` (proves claim 4).
5. `telemetry.TestErrorTypeIsBounded` (cancellation, deadline, a `net.Error` timeout, and an
   arbitrary error carrying an IP address, which must not reach the attribute) (proves claim 5).
6. `telemetrytest.TestRecorderCapturesEveryInstrumentKind` and `TestSpanRecorderCapturesSpans`
   (proves claim 6; a fake that silently recorded nothing would make the assertion in claim 3 pass
   vacuously, so the fake is tested directly).
7. The absence of any `otel/log` import in the module (claim 7).

Failing paths: `TestRoundTripperSurvivesAnEmptyURL`, `TestServerPortDefaultsToTheScheme/None`,
`TestVocabularyBoundsHostileValues`'s empty-vocabulary case.

## Rejected alternatives

- **`otelhttp.NewTransport`.** It is v0.x contrib moving independently of the v1.x API, and it
  records `url.full` on client spans — which for an APNs push is the device token. Fleet ships
  exactly this. A ~100-line RoundTripper keeps the dependency surface to three stable modules and
  lets the attribute set be a decision rather than a default.
- **Reading `otel.GetMeterProvider()` when the Config field is nil.** It is what Fleet's
  library-shaped packages do, and it would mean a consumer who configured OpenTelemetry for their own
  application silently acquired our metrics, with our cardinality, without asking.
- **Importing a `semconv` package for the attribute names.** Those are published one import path per
  specification release, so an import pins a spec version in our source and every upgrade becomes a
  choice between editing the path and going stale. It would save no dependency — `otel/trace`
  already links one — and the five names used here are in the stable set, whose spelling cannot
  change without a major version of the specification.
- **A `metrics` sub-package per domain package, or a single global registry.** The `Config` field
  pattern already exists for `Logger`, `Clock` and `Bus`; a ninth way to pass a dependency would be
  a new convention for no gain.
- **Taking `otel/log` and `otelslog` now.** Both are v0.x, and their own policy is that anything may
  change at any time. Consumers who want OTel logs today can bridge `slog` themselves and will get
  it here for free when the API stabilises.
- **Recording a route template on outbound calls.** Attractive for grouping, but Apple's APNs path
  has exactly one shape and the useful grouping is the host, which is already `server.address`. A
  template field is an invitation for the next edit to interpolate the token into it.
- **A `clock.Clock` seam on the RoundTripper.** `internal/clock` is internal, so a consumer could not
  supply a fake. `WithNow(func() time.Time)` is the same seam without the import restriction.
