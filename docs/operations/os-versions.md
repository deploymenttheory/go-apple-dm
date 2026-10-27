# OS versions and feature availability

`devicemanagement/osversion` owns dotted OS versions, parsing, construction,
comparison and named macOS major release constants. It has no schema or protocol
dependencies and can be used throughout the library and server.

`devicemanagement/schema/support` owns feature availability. Its `Target.Version`
and `OSSupport.Introduced`, `Deprecated` and `Removed` fields use
`osversion.Version` directly. `Entry.Check` evaluates those boundaries together
with the target OS, management channel and enrollment requirements. A zero
`Deprecated` or `Removed` version means the feature has no such bound; it is not a
release 0 boundary. Generated payload `Validate` methods use this metadata alongside
payload constraints. The metadata comes from Apple's per-key `supportedOS` blocks,
inherited field by field from each enclosing key, as described in the
[schema generator decision](../research/decisions/0003-schema-generator.md).

For example, this program checks a device's observed version against the generated
availability of an inventory command:

```go
package main

import (
    "log"

    "github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
    "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
    "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func main() {
    version, err := osversion.Parse("26.4.1")
    if err != nil {
        log.Fatal(err)
    }
    target := support.Target{
        OS:      support.MacOS,
        Version: version,
        Channel: support.ChannelDevice,
    }
    command := &commands.DeviceInformation{
        Queries: []string{"OSVersion", "BuildVersion"},
    }
    if err := command.Validate(target); err != nil {
        log.Fatal(err)
    }
}
```

Use `osversion.New(osversion.MacOS26, 4, 0)` for a known macOS 26.4 floor.
Named major constants preserve the actual release numbering; releases 15 and 26
are distinct, and the constants imply no intermediate releases. Retain minor and
patch components: `osversion.New(osversion.MacOS10, 15, 4)` represents 10.15.4.
A version contains no platform identity; `support.Target.OS` supplies that context.

Use `Parse` for observed or user-supplied strings and handle its error. `MustParse`
panics on malformed input and is intended for fixed literals in tests and tables.
The zero `Version` means unspecified; a target with an unspecified version does
not establish that a feature's release floor has been met. The reference server
restricts unknown inventory as described in the [mixed-OS fleet decision](../research/decisions/0052-mixed-os-fleets.md).

## Support tiers

Support is guaranteed from macOS 26 onwards; the schema contracts and acceptance
scenarios target macOS 26 and later. Earlier macOS releases are best effort: they
may function but are not actively tested. The tiers do not change availability
checks. Every feature's introduction, deprecation and removal boundaries apply at
every version, so a best-effort device is offered only what its version supports
and a guaranteed device is refused a feature that requires a later release.

## Availability issues

`support.Result.Rule` names the requirement a check applied, such as
`introduced`, `removed`, `deprecated` or `not-available`. To read every
availability issue for a payload, including deprecation warnings that
`Validate` does not return, validate into a collector:

```go
c := validation.New(target)
payload.ValidateInto(c)
if err := c.Err(); err != nil {
    // Blocking errors; validation.AvailabilityIssues(err) returns their keys.
}
for _, issue := range c.Availability() {
    log.Printf("%s %s %s %s introduced=%s removed=%s",
        issue.Path, issue.OS, issue.Rule, issue.Severity, issue.Introduced, issue.Removed)
}
```

`issue.Path` is the key's schema key path, which `support.Lookup` accepts, and
an issue never contains configuration values. A zero `Introduced`, `Deprecated` or
`Removed` version is an absent bound. A removed key is an error; a deprecated
key is a warning, and the payload is still delivered. A key or value without
availability data, such as a declaration payload key the generated type does not
define or an unknown `DeviceInformation` query, is also delivered and is reported
as a warning with rule `unvalidated` and no versions.

## Inputs and validation

Use `osversion.Version` in application fields and public interfaces. Keep the
`support` import for targets, OS identifiers and availability checks. Test parsing
failures with `errors.Is(err, osversion.ErrVersion)`; do not match error strings.
Availability describes schema support, while installation or execution also depends
on the device's actual state and platform requirements.

Implementation: [version primitives](../../devicemanagement/osversion),
[availability evaluation](../../devicemanagement/schema/support), and
[DDM target filtering](../../devicemanagement/mdmprotocol/ddm/compatibility.go).
Apple defines the input availability metadata in its
[schema format](https://github.com/apple/device-management/blob/release/docs/schema.md).
The [server release guide](server-releases.md) covers independent module validation.
