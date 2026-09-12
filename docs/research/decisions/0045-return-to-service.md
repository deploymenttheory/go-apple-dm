# 0045: Return to Service

## Context

A device in Return to Service mode asks for configuration through a check-in message. Enabling its response can erase and re-enroll the device.

## Decision

`service.Config.ReturnToService` is an optional policy handler. The service first applies enrollment authorization. No handler or a nil response produces `Enabled: false`. When the handler enables Return to Service and omits its bootstrap token, the service reads the escrowed token and fills it if available. A handler-supplied token is preserved.

The handler can set
`ReturnToService.ShouldRetryEnrollment` for eligible iOS 27 devices. A nil pointer
omits the key and retains Apple's false default; pointers to false and true are
serialized explicitly. Eligibility includes the enclosing Return to Service
supervision and ADE requirements. This is caller-selected policy. The reference server does not enable retries
automatically.

## Rationale

The protocol has a disabled response suitable for an unconfigured service. Attaching an available escrowed token supports Apple's app-preservation behavior without requiring every policy callback to repeat the lookup.

## Constraints

The device initiates this check-in; it is not a server-initiated command. Without a bootstrap token, an enabled response permits full erasure without app preservation. The reference server's `DM_RETURN_TO_SERVICE` option is disabled by default. Deployment admission must establish that the device is eligible.

## Verification

A test sends every generated check-in type through decoding and service dispatch. Return to Service tests cover disabled defaults, known identity requirements, stored and supplied tokens, and storage failures. End-to-end scenarios cover enabled and disabled server configuration.

The `schema_seed_os_27` service tests, required by `make test`, verify retry omission, false and true in the
actual plist response, stored versus handler-supplied bootstrap tokens, and the
generated platform/version restrictions.

## References

- [server/service/returntoservice_test.go](../../../server/service/returntoservice_test.go)
- [server/service/checkin.go](../../../server/service/checkin.go)
- [server/e2e/returntoservice_test.go](../../../server/e2e/returntoservice_test.go)
- <https://developer.apple.com/documentation/devicemanagement/check-in>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm`, `mdm/checkin.go`, `service/service.go`
- `jessepeterson/kmfddm`
- `fleetdm/fleet`, `server/mdm/nanomdm`
