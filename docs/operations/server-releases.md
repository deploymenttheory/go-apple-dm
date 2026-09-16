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
state, follow the [reference-server walkthrough](https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/getting-started/reference-server.md)
and [certificate lifecycle guide](https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/certificate-lifecycle.md).
Certificates, credentials, databases and local lab files are never packaged.

The main CI matrix runs both modules' race-enabled tests on Linux, macOS and
Windows. Windows also checks independent module installation and runs the
packaged executables. Private output files use owner-only Unix modes or Windows
ACLs limited to the current user, LocalSystem and administrators. Files are
synced before publication on every platform; Unix also syncs directory metadata.

## Verify a download

Download the archive, `go-apple-dm-server_<version>_checksums.txt`, and its
`.sigstore.json` bundle from the same release. With
[Cosign](https://docs.sigstore.dev/cosign/system_config/installation/) installed,
verify the checksum signature before checking the archive's hash. For example:

```sh
version=0.9.1
cosign verify-blob \
  --bundle "go-apple-dm-server_${version}_checksums.txt.sigstore.json" \
  --certificate-identity-regexp '^https://github\.com/deploymenttheory/go-apple-dm/\.github/workflows/(release|release-please)\.yml@refs/(tags/server/v[0-9A-Za-z.-]+|heads/main)$' \
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

Release Please manages both modules in manifest mode. Merging its release PR
creates a GitHub release and version tag for each changed module: `vX.Y.Z` for
the library, `server/vX.Y.Z` for the server. Nonbreaking pre-1.0 changes advance
the patch version; breaking changes advance the minor version. Authentication
uses the organization GitHub App (`RP_APP_ID` and `RP_APP_PRIVATE_KEY`).

The release-please workflow passes its documented `server--tag_name` output to
**Release server** when `server--release_created` is true. That job checks out the
tag, builds both commands for all six targets with `GOWORK=off`, checks the
archive hashes and Linux executable versions, signs the checksums, and attaches
the eight files to the existing server release. The full test suite runs in
application CI; release previews also execute the packaged Windows binaries.

GoReleaser OSS builds the archives in snapshot mode with `SERVER_VERSION` set to
the tag's exact version. Native support for the `server/` tag prefix requires
GoReleaser Pro, so `gh release upload` attaches the files. Release Please keeps
ownership of the tags and release notes.

To retry an upload, dispatch **Release server** with the existing `server/vX.Y.Z`
tag. It checks out that tag and replaces its binary archives, checksums and
signature. Library releases have no server assets.

**Check server release assets** builds and checks the same packages on pull
requests without publishing. It retains unsigned previews for seven days. Run
the packaging check locally with GoReleaser 2.18.1 from the repository root:

```sh
export SERVER_VERSION=0.0.0-local
goreleaser check
goreleaser release --clean --snapshot
(cd dist && shasum -a 256 --check "go-apple-dm-server_${SERVER_VERSION}_checksums.txt")
```

Upstream documentation:

- [Release Please manifest configuration](https://github.com/googleapis/release-please/blob/main/docs/manifest-releaser.md)
- [Release Please path outputs](https://github.com/googleapis/release-please-action#path-outputs)
- [Attaching files to a Release Please release](https://github.com/googleapis/release-please-action#attaching-files-to-the-github-release)
- [GoReleaser snapshots](https://goreleaser.com/customization/snapshots/)
- [GoReleaser module tag prefixes](https://goreleaser.com/customization/monorepo/)
