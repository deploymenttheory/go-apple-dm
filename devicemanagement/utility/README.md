# Device management utilities

Reusable administration helpers live in the root library module. `utility` is a
directory namespace; import its focused packages directly.

| Package | Responsibility |
| --- | --- |
| [`publicappstoreidentity`](publicappstoreidentity/) | Public App Store Identity: search public listings and resolve numeric store IDs to bundle identifiers |
| [`appidentity`](appidentity/) | Inspect bundle metadata and each architecture's code-signing identity on macOS |
| [`appsettings`](appsettings/) | Construct and validate app controls and privacy defaults using generated Apple payload types |

Discovery returns observations. Callers explicitly select the matching policy and
target. These packages do not assign declarations, store policy, or contact a
management server. The utility tier can use lower library layers but cannot
import storage, simulator, or server packages. Lower layers cannot import utility.

## Public App Store Identity

`publicappstoreidentity.Client.Search` accepts a search term, an explicit country
and entity, and an optional limit. The default limit is 50; Apple's maximum is
200. Entities are `software`, `iPadSoftware`, and `macSoftware`.
`Client.Lookup` takes a numeric App Store ID and the same explicit storefront.
Results include the bundle ID, listing name, developer, version, URL, and query
storefront. Search results do not identify the signed code installed on a device.

Each operation makes one request. Empty search results succeed; a missing lookup
returns `ErrNotFound`. `StatusError` exposes status and `Retry-After` for caller
policy. Inject `HTTPClient` to supply transport, caching, or instrumentation.
Requests have a default 15-second deadline and an 8 MiB response limit. No remote
pagination, automatic retry, disk cache, or undocumented external-version API is
implied. See the executable package example for a public listing lookup.

## Native application identity

`appidentity.Inspect(ctx, path)` accepts an app bundle or standalone Mach-O binary.
It reads the bundle's main executable only. On macOS, `/usr/bin/lipo -archs`
enumerates architectures and `/usr/bin/codesign` displays and verifies each slice.
Install Apple's Command Line Tools if the host cannot run `lipo`. The library has
no compiled architecture catalogue and does not execute the target file.

Bundle identifiers come from Info.plist and may differ from signing identifiers.
Designated requirements and CDHashes are reported per architecture. Each record
has a signature status (`valid`, `invalid`, or `unsigned`) and independently
verified category (`Apple`, `DeveloperID`, `AppStore`, or `unknown`). A valid
ad-hoc signature can have category `unknown`. Verification does not establish
Gatekeeper acceptance, notarization, or organizational trust.

The native operation has a 30-second deadline and bounds tool metadata to 1 MiB.
Other platforms return `ErrUnsupported` without starting a subprocess. Tool and
parsing failures return errors; unsigned and invalid signatures remain visible
in reports. All architectures are returned, or inspection fails. JSON reports
are portable observations and must come from a source the caller trusts.

## App Settings construction

All constructors return `*ddm.AppSettings` and call its generated `Validate`
method. Supply `support.Target` with OS, version, channel, and the actual
enrollment capabilities. Compatibility comes from the generated schema; the
utilities contain no release-version switches.

`AllowApps` and `DenyApps` accept explicit bundle IDs. `AllowBinaries` and
`DenyBinaries` accept an identity report and `BinaryOptions`:

| Match mode | Emitted identifiers | Effect |
| --- | --- | --- |
| `MatchCDHash` (`cdhash`) | CDHash | Exact signed code; updates commonly need new rules |
| `MatchApp` (`app`) | TeamID and SigningID | One app identity across code updates |
| `MatchTeam` (`team`) | TeamID | Publisher-wide matching |
| `MatchSigningID` (`signing-id`) | SigningID | Denial only; not bound to a publisher |

Every architecture must have a valid signature and the selected identifiers.
Missing identifiers never broaden the match. Equal rules are deduplicated.
`*APPLE*` is emitted for an absent team ID only when Apple signing was verified.
Optional path prefixes and signing states are explicit additional constraints;
they are not copied automatically from inspection. Path prefixes are preserved
exactly: include the trailing slash when matching the contents of a directory.
The managed-app exception is available only for binary allow lists.

`PrivacyDefaults` accepts `PrivacyEntry` records containing a bundle ID, a
designated requirement for macOS, and a generated `ddm.AppSettingsAppDictionary`.
The permission record must contain an organization justification. macOS keys use
`Bundle-ID {Designated-Requirement}`; iOS keys use the bundle ID alone.
`ComposeIdentifier` checks composition delimiters without implementing Apple's
requirement grammar. Duplicate keys are errors. Generated validation enforces
permission values, platform eligibility, and scope; macOS binary controls and
privacy defaults require different channels and therefore separate payloads.

Empty lists are rejected because the current generated serialization omits empty
arrays. Constructors do not claim to express a restrictive empty allow list.
Wrap a completed payload using existing DDM declaration or blueprint APIs.

## References

- [Apple iTunes Search API](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/)
- [Apple code-signing hashes](https://developer.apple.com/documentation/technotes/tn3126-inside-code-signing-hashes)
- [Pinned App Settings schema](../../../third_party/apple-device-management/current/declarative/declarations/configurations/app.settings.yaml)
- [Stop hunting for bundle IDs and CDHashes](https://cantscript.com/posts/stop-hunting-for-bundle-ids--cdhashes/)
