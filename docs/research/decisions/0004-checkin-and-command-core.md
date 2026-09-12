# 0004: Check-in and command protocol core

## Context

Device and user channels share the check-in and command transport but use different enrollment identifiers.

## Decision

Check-in decoding resolves generated `devicemanagement/schema/checkin` messages in a single pass and retains raw bytes. `NewCommand` wraps typed payloads with `RequestType` and an uppercase UUIDv7 `CommandUUID`. Response decoding uses the command registry when the request type is known.

`Enrollment.Resolve` represents device, user, Shared iPad user, User Enrollment device, and User Enrollment user channels. Shared iPad uses Apple's all-`F` `UserID` sentinel and `UserShortName`; User Enrollment uses `EnrollmentID` and `EnrollmentUserID`. Invalid combinations and missing identifiers return errors.

## Rationale

Explicit channel types support command-target validation and separate device and user state. Retained bytes allow signature verification and forwarding without re-encoding.

## Constraints

Decoding establishes message shape and identity fields, not authorization. Certificate, bearer and enrollment policy checks belong to the service and enrollment layers.

## Verification

Protocol tests cover generated message dispatch, channel resolution, typed command/response round trips, limits and malformed input. Service tests pair the generated check-in registry with dispatch coverage.

## References

- [mdmprotocol/mdm](../../../devicemanagement/mdmprotocol/mdm)
- [server/service](../../../server/service)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>
- <https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@main`, `mdm/checkin.go`, `mdm/command.go`, `mdm/mdm.go`, `mdm/type.go`, `service/nanomdm/service.go`
- `fleetdm/fleet@main`, `pkg/mdm/mdmtest/apple.go`
