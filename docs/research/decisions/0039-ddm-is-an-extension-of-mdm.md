# 0039: Declarative device management within the MDM enrollment

## Context

Declarative device management uses MDM enrollment, identity and check-in transport. Its declaration engine can nevertheless have a separate deployment lifecycle.

## Decision

The protocol core decodes `DeclarativeManagement` as a check-in message. The service dispatches it through `DMHandler`; the declaration engine depends on MDM types without a reverse engine dependency in the core.

Declaration changes are persisted transactionally. The notifier builds `DeclarativeManagement` commands and sends them through the normal service enqueue path, retaining target checks, hooks and events. Administrative write wrappers can wake the notifier; the engine has no callback into command dispatch.

## Rationale

This dependency direction follows the shared protocol transport and avoids a serving-to-dispatch cycle. Persistent change rows support both in-process and split deployments. Administrative resources can expose MDM and DDM operations according to the process's configured components.

## Constraints

Roles describe deployment topology, not distinct Apple enrollment protocols. Split-process cleanup and state updates are not a distributed transaction. Administrative route availability follows component ownership.

## Verification

Layout tests enforce import direction. Application tests verify that DDM commands publish normal command events and audit records and that writes wake the notifier. Notifier tests cover coalescing and retries; end-to-end tests cover the split hop and CLI route coverage.

## References

- [mdmprotocol/mdm](../../../mdmprotocol/mdm)
- [mdmprotocol/ddm](../../../mdmprotocol/ddm)
- [server/ddmsync](../../../server/ddmsync)
- [server/ddmadapter](../../../server/ddmadapter)
- <https://developer.apple.com/documentation/devicemanagement/leveraging-the-declarative-management-data-model-to-scale-devices>
- <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management>
- <https://developer.apple.com/documentation/devicemanagement/declarative-management>
- <https://developer.apple.com/documentation/devicemanagement/declarativemanagementcommand>

Reference source identifiers and paths (relative to the named project):

- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7`
- `zentral/contrib/mdm/artifacts.py`, `commands/declarative_management.py`, `commands/scheduling.py`
- `commands/base.py`, `declarations/`, `workers.py`, `models.py`
- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`
- `server/mdm/apple/commander.go`, `server/mdm/apple/reconcile.go`
- `server/service/apple_mdm_declarations_batched.go`, `server/service/apple_mdm.go`
- `server/mdm/nanomdm/service/service.go`, `server/mdm/nanomdm/service/nanomdm/dm.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151`
- `notifier/notifier.go`, `notifier/foss/foss.go`, `notifier/foss/dm.go`, `notifier/cmd_dm.go`
- `http/api/api.go`, `http/api/declarations.go`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667`
- `ddmadapter/ddmadapter.go`, `ddmadapter/service.go`, `enqueue/enqueue.go`, `nanohub.go`
- `cmd/nanohub/nanohub.go`
