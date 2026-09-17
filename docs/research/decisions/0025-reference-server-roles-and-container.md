# 0025: Reference server roles and container

## Context

The library needs a runnable composition for local development and integration testing, including deployments with a separate declaration engine.

## Decision

The shared runtime, native TLS, and process-based bench extend this decision; see [0048](0048-reference-server-bench.md). `dmserver` remains the serving executable. The bench owns fixture services and process supervision. Both the binary and image default to `127.0.0.1:8080`; plaintext listeners require literal loopback addresses. Remote and container-network listeners require native TLS through managed HTTPS identities or `DM_TLS_CERT_FILE` and `DM_TLS_KEY_FILE`. A TLS reverse proxy can use a loopback backend or verified TLS to a remote backend.

`app.Build` validates configuration and assembles stores, protocol services, enrollment handlers, administrative routes and workers. `server/cmd/dmserver` handles process startup. Roles are `mdm`, `ddm` and `all`; the split determines where the declaration engine runs, not a separate Apple protocol boundary.

Administrative families are mounted according to available components and credentials. An `mdm` role forwarding declarations does not expose a local DDM administrative family. `/healthz` checks readiness against storage. The container uses a Go builder and a distroless, non-root runtime.

The runtime image includes `dmctl` for one-shot administration. The local
Compose package builds both binaries from the checkout and uses a separate
Python helper image to call the existing `dmctl setup` workflow. Both run as
UID 65532. Bootstrap generates local HTTPS and enrollment identities once in a
named volume, preserves operator configuration and keys on later starts, and
requires explicit recovery for missing initialized material. Its JSON apply
operation validates local setup loading before atomic replacement; runtime
validation still happens at server start. The server image remains distroless.
The package selects SQLite, role `all`, stored administrators and audit, with
HTTPS exposed only on host loopback. Apple push credentials and admission remain
operator responsibilities. Compose clears image environment defaults which
would otherwise override its setup document.

## Rationale

A reusable application builder lets tests exercise the same composition as the binary. Component-based route registration keeps administrative operations directed at the process that owns the relevant state.

## Constraints

The reference server is an example composition, not a complete fleet management product. Persistent deployments require stable CA material, keyring configuration, trusted public TLS and appropriate admission policy. Split roles require the same persistent database, database schema and compatible
storage keyrings. The split hop requires HTTPS and both HMAC keys. The container integration scenario skips when its explicit environment is absent. The image runs `dmserver -check auto`. It derives the scheme and port from
`DM_LISTEN` and `DM_TLS_CERT_FILE`/`DM_TLS_KEY_FILE`, using loopback for wildcard
listeners. Automatic HTTPS probes pin the configured server certificate and use
its SANs for normal hostname verification, so private and DNS-only certificates
work without public DNS or disabled verification. This checks the local listener
and storage, not public ingress trust or reachability. Replacing certificate files
requires restarting the server. The two-second probe refuses redirects and requires
HTTP 200. `-check URL -check-ca-file roots.pem` supports explicit private-CA probes.
Container listener/TLS settings should use environment variables; command-only
server overrides require the same flags in an overridden Docker health command.
`DM_SETUP_FILE` is also read by the automatic probe; managed certificates are
loaded from the same persistent configuration. The local Compose package uses
this path. Its named volume preserves state across container replacement but
does not replace the backup/restore workflow.

## Verification

Application tests cover roles, invalid configuration, route families, readiness and worker lifecycle. `scripts/testdb.sh ddm-up` supplies both containers and their shared SQLite or PostgreSQL database for the
split-deployment end-to-end scenario. E2E_STORE selects the backend for both roles.
`make test-quickstart` checks bootstrap resume/failure behavior, onboarding
examples and an isolated Compose lifecycle with verified HTTPS, stored-admin
handoff and retained identities/configuration across restart.

## References

- [server/internal/app](../../../server/internal/app)
- [server/cmd/dmserver](../../../server/cmd/dmserver)
- [Dockerfile](../../../Dockerfile)
- [Local Compose package](../../../deploy/quickstart/compose.yaml)
- [Reference-server walkthrough](../../getting-started/reference-server.md)
- [scripts/testdb.sh](../../../scripts/testdb.sh)
- <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management>
- <https://developer.apple.com/documentation/devicemanagement/mdm>
