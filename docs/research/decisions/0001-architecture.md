# 0001: Library-first architecture with a generated schema core

## Context

Applications need reusable Apple device management protocol components and an example of how to assemble them into a server.

## Decision

The root Go module provides generated schema types, protocol handling, certificate services, Apple service clients, storage contracts, in-memory implementations, and a simulator. The `server` module depends on the library and supplies SQL backends, orchestration, HTTP adapters, and administrative tools. Library code and tests do not import the server module.

Service methods take explicit contexts. Hooks wrap operations, and a typed event bus supports subscribers such as audit and webhook sinks. Declarative device management uses the MDM transport and enrollment identity. The declaration engine can run in-process or through the authenticated internal adapter described in record 0023.

## Rationale

Separate modules let consumers use protocol packages without the server's database drivers and authorization dependencies. Generated types and runtime support metadata keep protocol modeling tied to the pinned Apple schema. Shared storage contract suites define backend behavior.

## Constraints

The reference server has no management UI, inventory product, or fleet policy system. Its internal split-deployment protocol is specific to this project; it does not implement NanoMDM's `-dm` header contract. Hardware compatibility requires testing on Apple devices.

## Verification

The import-layout tests enforce module and tier boundaries. Service tests cover hooks and events; storage contract suites cover enrollment, command delivery, certificates, and migration. Schema regeneration is checked by `make verify`.

## References

- [mdmprotocol/mdm](../../../devicemanagement/mdmprotocol/mdm)
- [mdmprotocol/ddm](../../../devicemanagement/mdmprotocol/ddm)
- [server/service](../../../server/service)
- [storage](../../../devicemanagement/storage)
- [internal/layout](../../../internal/layout)
- <https://developer.apple.com/documentation/devicemanagement>
- <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm`, `service/service.go`, `storage/*.go`, `mdm/checkin.go`, `mdm/command.go`, `mdm/mdm.go`
- `jessepeterson/kmfddm`, `storage/*.go`, `ddm/*.go`
- `fleetdm/fleet`, `server/mdm/nanomdm/README.md`, `docs/Contributing/architecture/mdm/apple-declarative-device-management.md`
- `deploymenttheory/go-sdk-appleservices`, `device_management/**`
