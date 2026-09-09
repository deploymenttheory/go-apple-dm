# 0025: Reference server roles and container

## Context

The library needs a runnable composition for local development and integration testing, including deployments with a separate declaration engine.

## Decision

The shared runtime, optional native TLS, and process-based bench extend this decision; see [0048](0048-reference-server-bench.md). `dmserver` remains the serving executable. The bench owns fixture services and process supervision.

`app.Build` validates configuration and assembles stores, protocol services, enrollment handlers, administrative routes and workers. `server/cmd/dmserver` handles process startup. Roles are `mdm`, `ddm` and `all`; the split determines where the declaration engine runs, not a separate Apple protocol boundary.

Administrative families are mounted according to available components and credentials. An `mdm` role forwarding declarations does not expose a local DDM administrative family. `/healthz` checks readiness against storage. The container uses a Go builder and a distroless, non-root runtime.

## Rationale

A reusable application builder lets tests exercise the same composition as the binary. Component-based route registration keeps administrative operations directed at the process that owns the relevant state.

## Constraints

The reference server is an example composition, not a complete fleet management product. Persistent deployments require stable CA material, keyring configuration, external TLS and appropriate admission policy. The split hop requires both HMAC keys. The container integration scenario skips when its explicit environment is absent.

## Verification

Application tests cover roles, invalid configuration, route families, readiness and worker lifecycle. `scripts/testdb.sh ddm-up` supplies the container used by the split-deployment end-to-end scenario.

## References

- [server/internal/app](../../../server/internal/app)
- [server/cmd/dmserver](../../../server/cmd/dmserver)
- [Dockerfile](../../../Dockerfile)
- [scripts/testdb.sh](../../../scripts/testdb.sh)
- <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management>
- <https://developer.apple.com/documentation/devicemanagement/mdm>

Reference source identifiers and paths (relative to the named project):

- `jessepeterson/kmfddm@4b75a76`, `cmd/kmfddm/main.go`, `http/http.go`, `Dockerfile`, `docs/operations-guide.md`
- `micromdm/nanomdm@4948319`, `cmd/nanomdm/main.go`, `docs/operations-guide.md`
- `micromdm/nanohub@3d73c1a`, `cmd/nanohub/nanohub.go`
- `fleetdm/fleet@b44343c`, `cmd/fleet/main.go`, `server/service/apple_mdm.go`
