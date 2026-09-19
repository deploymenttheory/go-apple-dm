# Application identity discovery

These Go helpers find the identifiers needed when authoring DDM app controls.
An author can start with an app name or a local macOS app, without constructing
Apple API requests or parsing `codesign` output. The returned facts populate the
existing `ddm.AppSettings` types: `AllowedApps`, `DeniedApps`, `AllowedBinaries`
and `DeniedBinaries`.

`utility` is a directory namespace in the root library module. Import its focused
packages directly.

| Package | Input | Discovered facts |
| --- | --- | --- |
| [`publicappstoreidentity`](publicappstoreidentity/) | App name, storefront and software entity; optional developer filter | Public listings with bundle IDs, developer names and Apple's platform metadata |
| [`appleappidentity`](appleappidentity/) | Apple app name or bundle ID | Bundle IDs from Apple's iPhone and iPad catalogue, including preinstalled apps |
| [`appidentity`](appidentity/) | Local macOS app bundle or Mach-O binary path | Bundle metadata and each architecture's CDHash, SigningID, TeamID and verified signing category |

Callers choose the intended application, allow or deny policy, and matching
breadth. Identity discovery runs while authoring, before a payload is passed to
the existing declaration or Blueprint APIs. Blueprint compilation operates on
the resulting explicit values. These packages have no persistence, simulator or
server dependency. Lower library tiers cannot import them.

## Public App Store identity

Search by name without knowing a numeric App Store ID:

```go
client := publicappstoreidentity.Client{}
apps, err := client.Search(ctx, publicappstoreidentity.Query{
    Term:      "Microsoft Teams",
    Developer: "Microsoft",
    Store: publicappstoreidentity.Store{
        Country: "GB",
        Entity:  publicappstoreidentity.Software,
    },
})
```

Handle `err`, then review the returned names, developers and platform metadata
before selecting a listing. Search never silently selects the first match.
`Software`, `IPadSoftware` and `MacSoftware` select Apple's software entities.
`Kind`, `Features` and `SupportedDevices` preserve the listing's metadata when
present. `Store.Entity` records the requested search category; it is not proof
that an app supports a particular device.

`Query.Developer` applies a case-insensitive substring filter locally, after
Apple applies `Limit`. An empty filter returns all received listings. The default
limit is 50 and the maximum is 200. Each operation makes one request; callers may
display pages from that returned slice, but the client does not promise remote
pagination or a complete search of the store. `Lookup` remains available when
the numeric App Store ID is already known.

An empty search succeeds; a missing lookup returns `ErrNotFound`. `StatusError`
exposes HTTP status and `Retry-After`. Inject `HTTPClient` for a custom transport,
caching or instrumentation. Requests have a default 15-second deadline and an
8 MiB response limit. Retry and rate-limit policy belong to the caller.

The [executable authoring example](publicappstoreidentity/example_test.go) checks
for ambiguity, takes a selected bundle ID, and validates and serializes an
existing typed payload. The essential assignment is:

```go
payload := &ddm.AppSettings{Allowed: &ddm.AppSettingsAllowed{
    AllowedApps: []string{selected.BundleID},
}}
err := payload.Validate(target)
```

Use `DeniedApps` when the author chooses denial. Supply the actual target's OS,
version, channel and enrollment capabilities to the generated validator.

## Apple app identifiers

`appleappidentity.Search("Safari")` returns `Safari` and `com.apple.mobilesafari`.
Search matches names or bundle IDs without case sensitivity, while preserving
the identifier's original case in the result. An empty search returns the whole
catalogue. `Lookup` requires the exact, case-sensitive bundle ID.

The bundled catalogue records its `SourceURL` and `ReviewedOn` date. It contains
Apple's published iPhone and iPad entries, including both preinstalled and
downloadable apps. It does not describe which apps are installed or available on
a particular device or OS release, and its IDs must not be assumed to apply to
macOS. Queries work offline. Maintainers review the source and update the
snapshot and date together; a new OS release does not require a version switch.

## Native macOS application identity

```go
identity, err := appidentity.Inspect(ctx, "/Applications/Example.app")
```

Inspection reads the bundle's main executable or a standalone binary. It uses
`/usr/bin/lipo -archs` to discover architectures dynamically and
`/usr/bin/codesign` to inspect and verify each slice. Install Apple's Command Line
Tools if the host cannot run `lipo`. Inspection does not launch the app or traverse
embedded helpers.

Each architecture reports its CDHash, SigningID, TeamID, designated requirement,
signature status and signing category. Bundle IDs come from `Info.plist` and may
differ from signing IDs. A missing team identifier stays empty. For Apple's
`TeamID` payload field, the schema specifies `*APPLE*` for Apple binaries with no
team identifier; callers must establish the Apple category before making that
substitution.

Signature status is `valid`, `invalid` or `unsigned`. Category is `Apple`,
`DeveloperID`, `AppStore` or `unknown`, based on signing-requirement verification.
A valid ad-hoc signature may have an unknown category. Inspection does not infer
Enterprise or TestFlight signing, or establish Gatekeeper acceptance or
notarization. Review the facts before choosing a payload's `SigningState`.

The author selects the match:

| Payload field(s) | Authoring consideration |
| --- | --- |
| `CDHash` | Identifies exact signed code. Include each architecture; an app update can change the hashes. |
| `TeamID` and `SigningID` | Identifies an app's signing identity across code updates. |
| `TeamID` | Can match other apps signed by that team. |
| `PathPrefix` | Adds an explicitly chosen path constraint. The inspected path does not automatically become a prefix rule. |
| `SigningState` | Adds the selected signing-state constraint supported by the target schema. |

The [native authoring example](appidentity/example_test.go) inspects an app and
populates `AllowedBinaries` using each verified architecture's CDHash. Callers
can use the same observations for `DeniedBinaries`. No matching mode is selected
by inspection.

Operations have a 30-second deadline and bound tool metadata to 1 MiB. All
architectures are reported, or inspection fails. Other platforms return
`ErrUnsupported` without starting a process. Tool and parsing failures are
errors; unsigned and invalid signatures remain visible in successful reports.
Identity records have JSON tags so callers can retain or export selected facts.
Reports describe the inspected file at that time; callers are responsible for
the provenance and freshness of imported records.

## References

- [Apple iTunes Search API](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/)
- [Apple iPhone and iPad app bundle IDs](https://support.apple.com/en-euro/guide/deployment/depece748c41/web)
- [Apple code-signing hashes](https://developer.apple.com/documentation/technotes/tn3126-inside-code-signing-hashes)
- [Pinned App Settings schema](../../../third_party/apple-device-management/current/declarative/declarations/configurations/app.settings.yaml)
- [Stop hunting for bundle IDs and CDHashes](https://cantscript.com/posts/stop-hunting-for-bundle-ids--cdhashes/)
