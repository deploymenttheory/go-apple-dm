# Reference server downloads

Download the archive for your OS and CPU from a
[`server/v…` GitHub release](https://github.com/deploymenttheory/go-apple-dm/releases).
Each archive contains `dmserver`, the reference server, and `dmctl`, its admin CLI,
plus this guide and the license. Windows executables have an `.exe` suffix.

| Operating system | Intel / AMD 64-bit | ARM 64-bit |
| --- | --- | --- |
| Linux | `linux_amd64.tar.gz` | `linux_arm64.tar.gz` |
| macOS | `darwin_amd64.tar.gz` | `darwin_arm64.tar.gz` |
| Windows | `windows_amd64.zip` | `windows_arm64.zip` |

The binaries are built with CGO disabled, including SQLite support. They do not
require a Go installation. After verifying and extracting your archive, put the
binaries on your PATH and run `dmserver --version` and `dmctl version` to identify
the release. For enrollment, certificates, server configuration and persistent
state, follow the [getting started guide](https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/getting-started/getting-started.md)
and [certificate lifecycle guide](https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/certificate-lifecycle.md).
Certificates, credentials, databases and local lab files are never packaged.

## Verify a download

Download the archive, `go-apple-dm-server_<version>_checksums.txt`, and its
`.sigstore.json` bundle from the same release. With
[Cosign](https://docs.sigstore.dev/cosign/system_config/installation/) installed,
verify the checksum signature before checking the archive's hash. For example:

```sh
version=0.9.1
cosign verify-blob \
  --bundle "go-apple-dm-server_${version}_checksums.txt.sigstore.json" \
  --certificate-identity-regexp '^https://github\.com/deploymenttheory/go-apple-dm/\.github/workflows/release\.yml@refs/(tags/server/v[0-9A-Za-z.-]+|heads/main)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "go-apple-dm-server_${version}_checksums.txt"

# Linux: verifies whichever archives you downloaded.
sha256sum --check --ignore-missing "go-apple-dm-server_${version}_checksums.txt"

# macOS: compare this digest to its entry in the verified checksum file.
shasum -a 256 "go-apple-dm-server_${version}_darwin_arm64.tar.gz"
```

On Windows, use `Get-FileHash <archive> -Algorithm SHA256` and compare the digest
with its entry in the verified checksum file. These are Sigstore workflow
signatures; the macOS executables are not Apple notarized applications.

## Release maintenance

Release Please owns versions, changelogs, tags and GitHub releases. The device management Go
library uses `vX.Y.Z`; the separate server module uses `server/vX.Y.Z`. Nonbreaking
pre-1.0 changes advance the patch version; breaking changes advance the minor
version. The organization App token (or the configured Release Please PAT)
allows the resulting release event to trigger the asset workflow.

The **Release server** workflow runs after a server release is published. It
checks the tag against the server manifest, checks out that exact tag, builds
both commands with `GOWORK=off`, verifies all archive contents and checksums,
signs the checksum file with GitHub OIDC, and uploads those eight assets to the
existing release. The library release receives no server binaries. Release
notes remain owned by Release Please.

The configuration uses OSS GoReleaser's snapshot packaging mode because its
[module tag prefix support requires Pro](https://goreleaser.com/customization/monorepo/).
`SERVER_VERSION` supplies the validated tag's exact version, with no snapshot
suffix. GoReleaser publication is disabled; the workflow uploads the verified
files to the original `server/v…` release without creating alternate tags.

To retry an interrupted upload, rerun the release job, or dispatch **Release
server** from `main` with the existing server tag. Only the six versioned archives,
their checksum file and signature bundle are replaced. The workflow requires
an existing, published release whose server manifest matches the tag. It does
not backfill the old `server-v…` naming scheme.

Pull requests run **Check server release assets** with read-only permissions.
This builds all targets, verifies each archive and runs the Linux binaries'
version commands. Unsigned preview archives are retained as CI artifacts for
seven days. To perform the same packaging check locally with GoReleaser 2.18.1:

```sh
export SERVER_VERSION=0.0.0-local
goreleaser check
goreleaser release --clean --snapshot --skip=publish
python3 .github/scripts/server_release.py verify dist "$SERVER_VERSION"
```

Run these commands from the repository root; `dist/` is ignored by Git.
