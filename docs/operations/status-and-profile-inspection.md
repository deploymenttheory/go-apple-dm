# Status and profile inspection

## DDM status

```sh
dmctl enrollments status values device DEVICE-ID -prefix management. -limit 200 -all -output json
dmctl enrollments status errors device DEVICE-ID -limit 100 -output json
dmctl enrollments status reports user USER-ENROLLMENT-ID -parent DEVICE-ID -limit 100 -output json
```

`dmctl status` continues to describe the server. Enrollment diagnostics use the
`enrollments status` subcommands and the existing server/credential settings.
`-cursor` resumes a page; `-all` follows the returned continuation cursors.
Human, JSON and NDJSON output use the existing CLI conventions.
Without `-all`, JSON preserves the server's `Items`/`NextCursor` page envelope.
`-all -output json` and `-output ndjson` follow every page and stream one item per
line. For those streaming modes, decode values with `jq -r '.Value | @base64d'`.

The corresponding authenticated routes are:

| Route below `/admin/v1/enrollments/{channel}/{id}/status` | Query | Default |
| --- | --- | --- |
| `/values` | `limit`, `cursor`, `prefix`, user `parent` | 1,000 values |
| `/errors` | `limit`, `cursor`, user `parent` | 100 errors, newest first |
| `/reports` | `limit`, `cursor`, user `parent` | 100 retained reports, newest first |

All use `ReadEnrollmentStatus` authorization, the existing enrollment identity
rules and `Items`/`NextCursor` response shape. The shared paging contract clamps
positive limits to 1,000. Treat cursors as opaque and preserve filters and parent
identity when resuming. Reports only exist within configured retention.

`Value`, `Reasons` and `Raw` remain byte fields, represented as base64 in JSON.
For a single JSON page, decode values with `jq -r '.Items[].Value | @base64d'`.
Credential-bearing paths and free-form error descriptions/details are redacted in
admin projections. Stored evidence is unchanged. Malformed stored JSON projects
as `null`. These queries do not refresh DDM delivery snapshots.

Apple defines [status reports](https://developer.apple.com/documentation/devicemanagement/statusreport)
and [status errors](https://developer.apple.com/documentation/devicemanagement/processing-status-for-managed-apps).
Pagination is this project's administrative API, not an Apple status wire format.

## Offline profile lint

```sh
dmctl profile lint -file profile.mobileconfig -target macos:26,channel=device,supervised,dep
dmctl profile lint -file - -target ios:26,channel=device,supervised -output json < profile.mobileconfig
dmctl profile lint -file signed.mobileconfig -target macos:26 -require-signature -trust-roots roots.pem
```

Lint reads at most 4 MiB and makes no server requests. The OS and version are
required; use the existing `explain` target syntax for channel, supervision,
ADE and user-enrollment properties. Checks use the pinned generated profile
schemas and support metadata.

Diagnostics identify paths and rules without including invalid values. Unknown
payloads/keys and encrypted content are explicitly unvalidated. Signature integrity
and certificate trust are separate: a valid signature has `trust=not-checked`
unless roots were provided. Supplied roots do not implicitly require a signature;
use `-require-signature` for that policy.

| Exit | Meaning |
| --- | --- |
| 0 | No errors or unvalidated content; deprecation warnings may remain |
| 1 | Malformed input, invalid signature/trust, or schema/support error |
| 2 | Invalid CLI arguments or target |
| 3 | Some content could not be validated |

The linter preserves unknown-input diagnostics instead of relying solely on a
typed decoder that discards unknown keys. Apple's `ANY` dictionaries remain
open-ended. Lint does not establish successful installation or device-specific
behavior. Sources: [profile structure/signing](https://developer.apple.com/documentation/devicemanagement/configuring-multiple-devices-using-profiles)
and [schema metadata](https://github.com/apple/device-management/blob/release/docs/schema.md).
