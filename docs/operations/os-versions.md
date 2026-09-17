# OS versions and feature availability

`devicemanagement/osversion` owns dotted OS versions, parsing, construction,
comparison and named macOS major release constants. It has no schema or protocol
dependencies and can be used throughout the library and server.

`devicemanagement/schema/support` owns feature availability. Its `Target.Version`
and `OSSupport.Introduced`, `Deprecated` and `Removed` fields use
`osversion.Version` directly. `Entry.Check` evaluates those boundaries together
with the target OS, management channel and enrollment requirements. Generated
payload `Validate` methods use this metadata alongside payload constraints.

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

## Migrating from the support version API

The version exports in `schema/support` have been removed. Add the
`devicemanagement/osversion` import and replace the old names as follows:

| Removed API | Replacement |
|---|---|
| `support.Version` | `osversion.Version` |
| `support.ParseVersion(s)` | `osversion.Parse(s)` |
| `support.MustVersion(s)` | `osversion.MustParse(s)` |
| `support.V(major, minor, patch)` | `osversion.New(major, minor, patch)` |
| `support.ErrVersion` | `osversion.ErrVersion` |

Keep the `support` import where code uses targets, OS identifiers or availability
checks. Update explicit version types in fields, function signatures, composite
literals and tests. Check malformed-version errors with
`errors.Is(err, osversion.ErrVersion)`; parser error text uses the `osversion:`
prefix. Version components, comparison, formatting and zero-value behavior are
unchanged by removal of the aliases and wrappers. No wire or stored data migration
is needed.

The root library must be published before a standalone server can resolve these
APIs through its declared dependency. Follow the
[release sequencing](../testing/macos27-prep-validation.md#release-sequencing)
before publishing the server.
