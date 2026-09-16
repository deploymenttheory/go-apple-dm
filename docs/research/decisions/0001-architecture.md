# 0001: Library-first architecture with a generated schema core

## Context

Applications need reusable Apple device management protocol components and an example of how to assemble them into a server.

## Decision

The root Go module provides generated schema types, protocol handling, certificate services, Apple service clients, storage contracts, in-memory implementations, and a simulator. The `server` module depends on the library and supplies SQL backends, orchestration, HTTP adapters, and administrative tools. Library code and tests do not import the server module.

Service methods take explicit contexts. Hooks wrap operations. Typed publisher/coordinator contracts support transactional event capture; SQL reference applications use a persistent event store for audit/webhook delivery. The in-process bus supports ephemeral subscribers. Declarative device management uses the MDM transport and enrollment identity. The declaration engine can run in-process or through the authenticated internal adapter described in record 0023.

Protocol helpers stay in the root library: Managed Apple Account JWTs and ADE
password hashes under `mdmprotocol/enroll`, CMS recovery-key decryption under `cms`,
server bypass codes under `activationlock`, and SHA-256 installation manifests under
`manifest`. They return protocol values; callers own authentication, secret storage
and side effects. Apps and Books HTTP licensing is a library client; notification
hosting, reconciliation and installation remain caller responsibilities.

The server owns administrative status projection, CLI profile inspection and
FileVault encryption-identity persistence before enqueueing. These boundaries keep
SQL drivers, Cedar and CLI behavior out of reusable helpers. See the
[helper guide](../../operations/protocol-helpers.md),
[licensing decision](0053-apps-and-books-licensing.md) and
[encryption-identity decision](0054-filevault-encryption-identities.md).

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
