# Device management library

These packages belong to the root module, `github.com/deploymenttheory/go-apple-dm`.
There is no separate `devicemanagement` module. The reference server remains in
[`../server/`](../server/).

| Folder | Responsibility |
| --- | --- |
| [`appleplatformservices/`](appleplatformservices/) | Apple service clients and their test helpers |
| [`mdmprotocol/`](mdmprotocol/) | MDM, DDM, enrollment, codecs and events |
| [`pki/`](pki/) | Certificate issuance, validation and revocation |
| [`schema/`](schema/) | Generated Apple types, validation and source provenance |
| [`storage/`](storage/) | Storage contracts, memory implementations and contract suites |
| [`simulator/`](simulator/) | Device simulator for embedding and tests |
| [`clock/`](clock/), [`paging/`](paging/), [`state/`](state/) | Clock, pagination and protocol state abstractions |
| [`ratelimit/`](ratelimit/), [`secrets/`](secrets/), [`telemetry/`](telemetry/) | Quotas, secret handling and instrumentation |
| [`testpki/`](testpki/) | Shared certificate test utilities |
| [`internal/`](internal/) | Private canonical JSON, CBOR and SCEP implementations |

Shared URL validation remains in `../internal/httpsurl/` because both the library
and server use it. Schema generation and repository checks remain in the root
`cmd/admgen/`, `internal/schemagen/` and `internal/layout/` directories.

## Updating existing imports

Insert `/devicemanagement` after the root module path for the public package
folders listed above. For example:

```go
import "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
import "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
```

Select a root module revision containing these paths with `go get
github.com/deploymenttheory/go-apple-dm@REVISION`. Old package locations do not have
forwarding aliases. Package names and exported signatures retain their existing
names; the relocation does not change wire formats, stored data or encryption
derivation identifiers. Server package imports continue to use the `/server/`
module path. Local development uses the root `go.work`.

See the [architecture guide](../docs/architecture.md) for dependency boundaries and
the [contributing guide](../CONTRIBUTING.md) for generation and verification commands.
