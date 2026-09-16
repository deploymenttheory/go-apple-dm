# Selected feature validation, 2026-09-15–16

This record covers the [eight approved additions](../research/extension-proposals-2026-09-14.md)
and their automatic Go certificate workflow. Code and automated checks are complete
in the working tree. Physical-device evidence covers DDM inspection, one profile
lint/install/remove cycle and FileVault escrow decryption. It does not establish
live interoperability for every Apple service or device family.

## Physical test Mac

The existing Apple silicon Mac ran macOS 26.6.2 (25G83), with FileVault enabled,
a retained bootstrap token and user-approved MDM enrollment. It was not enrolled
through ADE. The existing enrollment access rights were preserved at 19: profile
inspection, profile installation/removal and device information. No user password
was supplied for these tests.

| Check | Observed result |
| --- | --- |
| Fresh inventory | `DeviceInformation` acknowledged through the candidate server |
| DDM delivery | Live declaration/status scenario completed and removed its temporary declaration |
| Values pagination | 29 values traversed with limit 1; order and completeness matched a single large page |
| Reports pagination | 10 retained reports traversed with limit 1; order and completeness matched a single large page |
| Errors pagination | Empty page with no continuation; nonempty error pagination covered by automated tests |
| Prefix filter | Returned only the requested operating-system status paths |
| Final CLI | Values/errors/reports with `-limit 1 -all -output json` matched API record counts; human output included decoded values |
| Profile lint | Generated a benign managed-preferences profile; `dmctl profile lint` exited 0 for the Mac target |
| Profile acceptance | `InstallProfile` acknowledged; `ProfileList` confirmed its identifier |
| Profile cleanup | `RemoveProfile` acknowledged; subsequent `ProfileList` confirmed absence |
| FileVault escrow | Mac acknowledged the generated escrow profile and produced a CMS-encrypted recovery key |
| CMS recovery | `cms.DecryptEnvelope` recovered a 29-byte key matching the personal recovery-key format |
| Encrypted persistence | Recovered key was written to and read back from encrypted SQL; raw SQL contained a sealed value |
| Restart recovery | The same retained recipient decrypted the same CMS envelope after a server process restart |
| Escrow cleanup | Temporary escrow profile removed and absence confirmed; encrypted recovery key and recipient retained |

The benign profile set only a validation marker in the project's test preference
domain. These results establish acceptance of that profile, not all payloads the
linter can inspect. More than 1,000 status records, nonempty error cursors,
authorization, channel isolation and malformed inputs were checked in automated
tests rather than manufactured on the Mac.

### FileVault evidence and retrieval limit

[Apple's macOS 26 release notes](https://support.apple.com/en-us/124963) document
automatic rotation before escrow when an existing recovery key and a bootstrap
token are present. The server's new escrow route generated its RSA-2048 key and
self-signed certificate entirely in Go, retained them in encrypted SQL, and queued
a system profile containing the certificate and its referencing escrow payload.

The test confirmed no existing escrow payload before installing the temporary
profile. The Mac created a 446-byte `/var/db/FileVaultPRK.dat`; the test retained
that encrypted file and decrypted it with the stored recipient. Apple documents
both `SecurityInfo` retrieval and extraction by local administrative software in
the [escrow payload reference](https://developer.apple.com/documentation/devicemanagement/fderecoverykeyescrow).

`SecurityInfo` itself returned `MCMDMErrorDomain` error 12007 because this enrollment
does not grant that command's access right. The test used the documented local
file and did not expand the enrollment's permissions. Therefore the result proves
certificate acceptance, Mac-produced CMS decryption and encrypted persistence;
it does **not** prove remote `SecurityInfo` retrieval, the older password-based
`RotateFileVaultKey` command, or unlocking the disk with the recovered key.

Public fingerprints identifying the tested artifacts:

| Artifact | SHA-256 |
| --- | --- |
| Generated recipient certificate DER | `2548ae7258fb42df1efab7eb6ebefd67582c20f08b663bff93882573125afc33` |
| Mac-produced CMS envelope | `bc3c2d0419b554d994b3b8c6bdfe98512bd9e0c3c7211657072a198c7abd9991` |

The recovery key rotated during this workflow. Removing the profile does not
restore the previous key. The new key and recipient remain encrypted in the lab
database, with a consistent post-test database backup and copies of its external
storage keys. No plaintext recovery key was printed or written to a plaintext
artifact. This backup was integrity-checked; a full restore drill was not run.

Private evidence stays under the ignored
`test-lab/local/certs/candidates/selected-features/` directory:
`live-pagination.json`, `live-profile-lint.json`, `live-profile.json`,
`escrow-intent.json`, `escrow-result.json`, `live-filevault-decryption.json`,
`live-filevault-after-restart.json`, `escrow-cleanup.json`, `final-check.json`, command responses and
`after-live-checkpoint/`. Device identifiers and secret material are not published
in this report.

The lab's historical audit table lacked the current `event_id` column. A private
fixture repair preserved its 692 existing rows and added the column/index before
running the candidate. This was a lab repair, not a new production migration.

## Automated checks

| Check | Result |
| --- | --- |
| Root module: `go test -race -shuffle=on -count=1 ./...` | All packages passed across the full run and the affected-package rerun after a package-comment correction |
| Server module: same race/shuffle command from `server/` | Passed, including the bench and new admin, CLI and certificate tests |
| Root and server: `golangci-lint run` | Passed with zero issues |
| `make verify` | Passed: generated output verification and repository/workflow checks |
| Affected SQL contracts with PostgreSQL and MySQL configured | `server/statestore`, `server/internal/app` and `server/ddmstore/sqlstore` passed with `-race -tags integration` |
| SQLite end-to-end and acceptance suites | Passed with `-race -tags e2e` |
| PostgreSQL end-to-end suite | Passed with `E2E_STORE=postgres`, the configured test DSN and `-race -tags e2e` |
| Standalone server installation checker | Passed with `GOWORK=off`, no replacements and the declared published root-library version |
| Package documentation audit | All 118 owned Go package directories contain `doc.go` with Design and References sections |

The root run caught a malformed `contentcache/doc.go` comment while the layout
checker loaded the import graph. After correcting it, `internal/layout` and
`devicemanagement/contentcache` passed under race/shuffle, and root lint passed.
No new production dependency was introduced. No claim is made here about passing
the repository-wide coverage threshold or remote GitHub Actions execution.

The local `/usr/bin/make` launcher was blocked by a pending Xcode license. The
existing Command Line Tools `make` at
`/Library/Developer/CommandLineTools/usr/bin/make` ran the same `verify` target
successfully. No license was accepted and no global toolchain setting changed.

Independent checks include JWT signature/claim verification, PBKDF2 vectors,
pre-generated OpenSSL CMS fixtures, Activation Lock encoding/hash vectors and
manifest digests. Certificate tests cover concurrent preparation, conflicting
retries, encrypted persistence, server reopening and delayed replies after
certificate expiry. No test or runtime certificate generation invokes OpenSSL.
Apps and Books tests use controlled HTTP services for pagination, ownership,
limits, tokens, user lifecycle, notifications and asynchronous outcomes.

## Documentation evidence

The six destinations in
[`appsbooks/doc.go`](../../devicemanagement/appleplatformservices/appsbooks/doc.go)
were checked against Apple's actual page content, titles and canonical document
identifiers, rather than accepting HTTP 200 from a JavaScript shell:

- [Getting started with the management API](https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api)
- [Managing assets](https://developer.apple.com/documentation/devicemanagement/managing-assets)
- [Managing users](https://developer.apple.com/documentation/devicemanagement/managing-users)
- [Using paginated endpoints](https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints)
- [Service Config](https://developer.apple.com/documentation/devicemanagement/service-config)
- [Subscribing to notifications](https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications)

The package's new local guide is referenced by repository path. It is not linked
to an unpublished GitHub `main` URL.

The [Apple service clients diagram](../diagrams/apple-service-clients.html) includes
Apps and Books and its separate bearer credential. Its nine showcase artifact
checks passed with zero errors/warnings, and browser interaction checks passed.
Viewport checks covered 1440, 1600, 1920 and 2048 widths; full-page light/dark images
were inspected at 1440 and 2048. There was no horizontal overflow or label
collision. The upstream visual checker reports vertical scrolling as a failure;
the repository explicitly allows vertical scrolling for these diagrams.

## Remaining live checks

| Feature | Still needed for live acceptance |
| --- | --- |
| Managed Apple Account JWT | An Apple-registered ADE identity and a real supported `GetToken` exchange |
| ADE password hashes | An ADE-created account and authorised account-configuration/password-change test |
| Explicit FileVault rotation | Appropriate enrollment rights and a FileVault-enabled user's credential; test the command separately from the successful macOS 26 escrow flow |
| Activation Lock bypass codes | An authorised organisation/device Activation Lock enable-and-clear cycle |
| Installation manifests | Actual distributable macOS package and enterprise iOS/iPadOS app assets, plus matching test devices |
| Apps and Books | An organisation location token, available app/book licences and suitable device/user assignments; verify asynchronous completion before installation |

These are verification prerequisites, not evidence of success. The current Mac
and fixture services cannot establish those Apple-service results.
