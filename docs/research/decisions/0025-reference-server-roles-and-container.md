# 0025: Reference server roles and container

Status: accepted
Date: 2026-09-02
Phase: 5

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management> (DDM and MDM v1 coexist on one enrollment; the split is an implementation choice)
- Doc: <https://developer.apple.com/documentation/devicemanagement/mdm>

## References read

- `jessepeterson/kmfddm@4b75a76` `cmd/kmfddm/main.go`, `http/http.go`, `Dockerfile`, `docs/operations-guide.md`
- `micromdm/nanomdm@4948319` `cmd/nanomdm/main.go`, `docs/operations-guide.md`
- `micromdm/nanohub@3d73c1a` `cmd/nanohub/nanohub.go`
- `fleetdm/fleet@b44343c` `cmd/fleet/main.go`, `server/service/apple_mdm.go`

## Known pitfalls found

- KMFDDM: the device-facing endpoints and the admin API are served on the same port and the device endpoints are unauthenticated; an empty `-api` value silently disables the API rather than failing.
- NanoMDM: a `-dm` value without a trailing slash forwards to the wrong path (0023).
- Fleet: a single binary whose MDM server is not importable; every deployment is the whole product.
- All three: no health endpoint that reflects storage readiness, so a container orchestrator sees a listening port before the schema exists.

## What they do

- **KMFDDM**: one binary, flags for storage and API key, MySQL or file backends, distroless image.
- **NanoMDM**: one binary, flags, `-dm` proxy to a DDM server, distroless image.
- **NanoHUB**: NanoMDM plus KMFDDM plus NanoDEP in one process.
- **Fleet**: one large binary with MDM as a feature.

## What we do better

1. `internal/app.Config` (listen address, storage backend and DSN, keyring names, push certificate topic and store, HMAC keys, DDM role settings) and `app.Build(ctx, cfg) (*App, error)` wire storage, keyring, service core, push, engine, and notifier and return an `http.Handler` plus `Run(ctx)` for workers; a bad configuration is a `Build` error, never a silently disabled feature.
2. `cmd/mdmserver` is a thin main (flags and `DM_*` environment) with roles `mdm` (check-in and connect on `/mdm`; DDM through `inproc` when a local engine is configured or through `proxyclient` when `-ddm-url` is set), `ddm` (engine plus `proxyserver` on `/ddm/` plus the admin API), and `all` (both in one process through `inproc`).
3. The `ddm` role exposes a minimal authenticated JSON admin API: `PUT /admin/v1/declarations`, `PUT /admin/v1/sets/{set}/declarations/{id}`, `PUT /admin/v1/enrollments/{id}/sets/{set}`, and status reads, behind a bearer token; device ingress and admin are distinct route trees with distinct authentication. Phase 8 extends it.
4. `/healthz` answers 200 only after storage is reachable and migrated, so `HEALTHCHECK` and orchestrators see readiness rather than a bound port (E2E-015 partially delivered).
5. `Dockerfile` is multi-stage: `golang:1.27` builder, distroless static runtime, non-root user, `/data` volume for SQLite, `HEALTHCHECK` on `/healthz`; the image is built by CI from the repository and never pulled from a third party.
6. `scripts/testdb.sh ddm-up` and `ddm-down` build the image and run the `ddm` role container with generated HMAC keys and an admin token, exporting `TEST_DDM_URL` and the keys; E2E-010 is the split-deployment scenario (our `mdm` role in-process through `proxyclient`, our `ddm` role in the container) and skips with an explicit message when `TEST_DDM_URL` is unset, the same convention as the storage DSNs.

## Verified by

1. `app.TestBuild/MDMRole`, `/DDMRole`, `/AllRole`, `/BadConfig` (prove claims 1 and 2; would fail on KMFDDM because an empty `-api` disables the API without error).
2. Same tests, plus `e2e.TestE2E_DDMSplitDeployment` for the `mdm` role with `-ddm-url`.
3. `app.TestAdminAPI/Auth`, `/PutDeclaration`, `/Assign`, `/Status` (prove claim 3; would fail on KMFDDM because device routes share the API's listener without authentication).
4. `app.TestHealthz` (prove claim 4).
5. The CI e2e job step that runs `docker build` on the repository and starts the container before `make test-e2e` (prove claim 5).
6. `e2e.TestE2E_DDMSplitDeployment` (E2E-010) including the negative cases: wrong send key answers 401 at the server, wrong receive key makes the client reject the response, an oversized body answers 413 (prove claim 6).

## Rejected alternatives

- Interop against a NanoMDM container (the previous exit criterion for E2E-010): superseded by the user decision of 2026-09-02; both sides of the wire are ours.
- One role only, with the split left to phase 8: the wire contract in 0023 is untested without a real network hop, and the container is what CI needs for that hop.
- A full admin API now: phase 8 owns it; the three `PUT` routes and a status read are what E2E-010 needs.
- Alpine or scratch runtime: distroless static carries CA certificates and a non-root user with no shell.
- Serving admin and device ingress on the same route tree with one key (KMFDDM): a leaked device-side key must not grant admin access.

## Amendment 1: roles are a deployment choice (2026-09-03, phase 9)

Claim 2 describes the `mdm`, `ddm` and `all` roles as though the split were structural. Record 0039
establishes that declarative management is an extension of the MDM protocol, so the split is an
operational choice about where the declaration engine runs. The roles stay; the framing is
corrected.

Two things this record implied are no longer true. The `ddm` role no longer holds the admin API
alone: every role that has a credential serves it, because withholding it from the `mdm` role left
the half owning enrollments, commands and push with no administrative surface. And claim 3's
"minimal authenticated JSON admin API" has been superseded twice over, by record 0034's
authorization and by the `mdm` route family added in phase 9.
