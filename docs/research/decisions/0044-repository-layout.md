# 0044: Repository layout — layered tiers and a separate reference-server module

## Context

Package paths describe dependency responsibilities, and library consumers should not need the assembled server's drivers and administrative policy dependencies.

## Decision

The root module contains protocol packages, certificate services, Apple service clients, foundational utilities, storage contracts and in-memory backends. The server module contains SQL implementations, service orchestration, HTTP adapters, sinks, administration and binaries. Production and test imports cannot create a library dependency on the server module.

Tier tests check downward imports across foundation, schema, protocol, PKI, Apple clients, storage, simulator and server layers, with composition/generator packages handled explicitly. Test scaffolding is exempt. `paging` provides shared cursor types without domain storage dependencies; `pki/pushcert` is a standard-library leaf.

## Rationale

The module boundary separates embedding costs from runnable composition. Moving cross-domain adapters out of low-level packages keeps directory-level dependencies acyclic.

## Constraints

One exact upward exception remains: `mdmprotocol/enroll/ade` imports `appleplatformservices/gdmf` for its lookup vocabulary and version comparison. The test records this edge explicitly. Workspace builds use `go.work`; the server's local `replace` resolves the sibling library checkout.

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
