# 0060: Published container images

## Context

A reference server exists to be run by people who did not write it. Two audiences need it, and
a container image is the artifact that serves both.

**Bootstrapping.** A usable server is not one binary. It is a storage encryption key, an admin
credential, an issuance key, an initialized database, a setup document naming them, and an
ordered certificate workflow that issues an HTTPS identity and an enrollment authority before
any listener can start. Performed by hand these are a dozen coupled steps whose failure modes
are permissions, paths and ordering rather than protocol behavior. An image with a fixed
filesystem contract — one data volume, one configuration document, one listener — plus a
one-shot helper that generates the secrets makes that sequence reproducible and identical on
every machine, which is what lets onboarding documentation be followed rather than debugged.

**Consumption by adopters.** An operator evaluating or deploying this server is not a Go
developer and should not need a toolchain, a module cache or a source checkout to start one.
They need a versioned artifact they can pin, verify and run, matched to a release whose notes
they can read. The server ships as two binaries that must agree in version, `dmserver` and
`dmctl`, alongside a bootstrap helper that writes the configuration the server reads; an image
per release is what keeps those versions matched, and a registry is how an adopter obtains one
without building anything.

A third requirement shapes the tagging rather than the existence of the images: downstream work
in this project consumes server capabilities before a release contains them. The
device-acceptance lab builds on `server/lab`, and a lab that can only run released servers
cannot exercise the server changes it exists to test. Something must therefore represent the
current main branch as well as each release.

The build itself is straight forward to containerize, which removes the usual reasons not to.
Every dependency is pure Go, the SQLite driver included, so the binaries are static, need no
base image beyond a distroless root filesystem, and cross-compile to any architecture without a
per-architecture toolchain.

## Decision

Server releases and the main branch publish container images to
`ghcr.io/deploymenttheory/go-apple-dm`, public, for `linux/amd64` and `linux/arm64`.

Both Dockerfile targets are published to that one package name, distinguished by tag suffix,
because a consumer that runs the compose package needs both and versioning them together
keeps a bootstrap helper matched to the server it configures.

| Tag | Target | Mutability |
|---|---|---|
| `latest` | `runtime` | Moves; rebuilt on every merge into `main` |
| `latest-quickstart` | `quickstart` | Moves; rebuilt on every merge into `main` |
| `X.Y.Z`, `X.Y`, `server-vX.Y.Z` | `runtime` | Immutable, from a `server/vX.Y.Z` release tag |
| `X.Y.Z-quickstart`, `server-vX.Y.Z-quickstart` | `quickstart` | Immutable, from the same tag |

`latest` tracks `main`, not the newest release. This is deliberate: `server/lab` and other
server capabilities are consumed by the lab before any release contains them, so the moving
tag is the one that makes an unreleased server usable, and a release is identified by its
version. A deployment that needs reproducibility pins `X.Y.Z`.

`.github/workflows/publish-image.yml` owns publication. It runs on merges to `main` for the
moving tags, and is called by `release-please.yml` with the `server/vX.Y.Z` tag for the version
tags, alongside the existing `server-binaries` job. `workflow_dispatch` covers a manual
rebuild of either. A release build validates the tag's shape and confirms the GitHub release
exists before publishing an image that claims to be it.

Each digest is signed with keyless cosign and carries an SBOM and `mode=max` provenance, and a
build-provenance attestation is pushed to the registry. Signatures bind to the digest, never to
a tag, so a moving `latest` cannot carry a signature onto later content. After pushing, the job
pulls the runtime digest back and runs `dmctl version` and `dmserver --version`, so a published
image that cannot execute fails the job rather than the first consumer.

`deploy/compose.yaml` consumes the published images and contains no `build:` section, so it runs
in an empty directory. `deploy/quickstart/compose.yaml` keeps building from the checkout, which
is what server development needs. `DM_SERVER_IMAGE` and `DM_QUICKSTART_IMAGE` override either
image, and `DM_PULL_POLICY` defaults to `always` because the default tags move.

The build stage cross-compiles: `FROM --platform=$BUILDPLATFORM` with `GOOS`/`GOARCH` from
`TARGETOS`/`TARGETARCH`. Every dependency is pure Go, the SQLite driver included, so
`CGO_ENABLED=0` needs no per-architecture toolchain and a two-architecture build costs two
compiles on the native builder rather than an emulated build per architecture. `TARGETOS` and
`TARGETARCH` are unset under the classic builder, which leaves the Go defaults and still
produces a working native image.

A `VERSION` build argument stamps
`server/internal/buildinfo.releaseVersion`, the same symbol `.goreleaser.yaml` sets for the
released binaries, so `dmserver --version` and `dmctl version` inside a release image report
that release. The argument defaults to empty, which reports `(devel)` and is the correct answer
for an image built from a working tree or from the moving tag. The publish job asserts the
reported version equals the release version, because an image that cannot say what it is leaves
an operator no way to tell what they are running.

## Rationale

One package name with suffixed tags was chosen over separate `…/dmserver` and `…/quickstart`
packages because the two artifacts are released as a unit and GHCR applies visibility and
retention per package; one package is one thing to make public and one thing to prune.

Publishing on merge as well as on release addresses the situation that prompted this: the lab
needs server capabilities that no release contains. Restricting publication to releases would
leave that consumer building from source, which is the problem being solved. Restricting it to
merges would leave no reproducible artifact for a deployment.

Signing and attesting at the digest matches the release workflow's treatment of checksums and
keeps a moving tag honest, since the mutable pointer carries no attestation of its own.

## Constraints

`latest` is a moving tag: pulling it twice can yield different servers, and it may contain
unreleased behavior. Deployments pin a version tag. The default `always` pull policy in
`deploy/compose.yaml` makes staleness visible rather than silent, at the cost of a registry
round trip per `up`.

Publication needs `packages: write`, and signing and attestation need `id-token: write` and
`attestations: write`. GHCR packages are created private on first push; the package must be made
public once, by hand, in the repository's package settings. Until then an unauthenticated
`docker pull` fails even though this workflow succeeded.

The images carry server binaries only. Root library releases publish nothing, because the
library is consumed as a Go module.

`HEALTHCHECK` in the runtime image runs `dmserver -check auto`, which Compose honors and
Kubernetes ignores; a cluster deployment configures its own probes against `/healthz` and
`/readyz`.

This decision does not change `deploy/lab/compose.yaml`, which `dmctl lab` drives with
workspace-owned paths and an env file and which still builds from the checkout.

## Verification

Confirmed locally on `linux/arm64`:

- `docker build --target runtime .` builds and both binaries execute; with no `VERSION`
  argument they report `(devel)`, which also confirms the cross-compilation arguments remain
  optional under the classic builder.
- `docker build --target runtime --build-arg VERSION=0.10.3 .` produces binaries where
  `dmserver --version` and `dmctl version` both report `0.10.3`.
- `docker build --check .` reports no warnings.
- `docker compose -f deploy/compose.yaml config` resolves.
- `actionlint` reports no findings for `publish-image.yml` and `release-please.yml`.

Existing coverage:

- `make docker-build` builds the same target through the classic path.
- `deploy/quickstart/test_bootstrap.py`, run by `make test-quickstart`, covers the bootstrap
  helper that the `quickstart` image wraps.

Unverified until the workflow first runs: the multi-architecture `linux/amd64` build, the
published tag set, cosign signing, the provenance attestations, and the package's public
visibility, which requires a manual settings change after the first push.

## References

- `Dockerfile`, `.github/workflows/publish-image.yml`, `.github/workflows/release-please.yml`
- `deploy/compose.yaml`, `deploy/quickstart/compose.yaml`
- Decision 0025 for the `dmserver`/`dmctl` role split, decision 0048 for the lab that consumes
  the images
- [OCI image spec](https://github.com/opencontainers/image-spec),
  [cosign keyless signing](https://docs.sigstore.dev/cosign/signing/overview/),
  [GHCR documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
