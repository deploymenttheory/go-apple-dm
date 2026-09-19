# 0044: Repository layout — layered tiers and a separate reference-server module

## Context

Package paths describe dependency responsibilities, and library consumers should not need the assembled server's drivers and administrative policy dependencies.

## Decision

The root module contains protocol packages, certificate services, Apple service clients, foundational utilities, storage contracts and in-memory backends beneath `devicemanagement/`. This directory is a package namespace within the existing module. The server module contains SQL implementations, service orchestration, HTTP adapters, sinks, administration and binaries. Production and test imports cannot create a library dependency on the server module.

The `devicemanagement/utility/` namespace discovers application identities for
configuration authors through Go APIs.
Its tier sits above simulator and below server, with an additional prohibition on
storage and simulator imports. Public App Store Identity searches public listings,
appleappidentity queries a dated snapshot of Apple's iPhone and iPad app catalogue,
appidentity reads native or portable signing facts, and appartifact discovers
applications inside distribution archives. Portable readers use go-macos-pkg and
go-apfs-v2; they do not execute uploaded code or claim signature trust. Callers select applications
and matching criteria, then populate the existing generated payload types.
Discovery does not choose policy or run during Blueprint compilation.
Lower tiers cannot depend on utility packages.

Library-only JSON, CBOR and SCEP helpers live under `devicemanagement/internal/`.
The shared `internal/httpsurl` remains at the repository root so both modules can
import it. Generator and architecture tooling also remain at the root. The schema
output lives under `devicemanagement/schema/`, including its provenance and exported-name lock.

Tier tests check downward imports across foundation, schema, protocol, PKI, Apple clients, storage, simulator, utility and server layers, with composition/generator packages handled explicitly. Test scaffolding is exempt. `devicemanagement/paging` provides shared cursor types without domain storage dependencies. `devicemanagement/pki/pushcert` has no dependencies on other repository packages; it uses `howett.net/plist` for vendor CSR envelopes and remains independent of the push client.

## Rationale

The module boundary separates embedding costs from runnable composition. Moving cross-domain adapters out of low-level packages keeps directory-level dependencies acyclic.

## Constraints

One exact upward exception remains: `devicemanagement/mdmprotocol/enroll/ade` imports `devicemanagement/appleplatformservices/gdmf` for its lookup vocabulary and version comparison. The test records this edge explicitly. Workspace builds use `go.work`; the server's declared dependency resolves a published root library revision without a replacement directive.

## Verification

`internal/layout` loads both modules, checks tier edges and populated tiers, and rejects directory-level cycles. Additional tests constrain event and push-certificate dependencies. Generator verification checks that module layout does not disturb generated output.

## References

- [internal/layout](../../../internal/layout)
- [go.mod](../../../go.mod)
- [server/go.mod](../../../server/go.mod)

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm`, `api certverify cli cmd cryptoutil http mdm push service storage test tools`
- `jessepeterson/kmfddm`, `cmd ddm http jsonpath logkeys notifier storage test tools`
- `micromdm/nanodep`, `albc cli client cmd cryptoutil godep http log proxy storage sync tokenpki tools`
- `micromdm/micromdm`, `cmd dep mdm pkg platform server tools vpp workflow`
- `pkg/{activationlock,crypto,httputil}`, `platform/{apns,command,dep,device,profile,queue,...}`
