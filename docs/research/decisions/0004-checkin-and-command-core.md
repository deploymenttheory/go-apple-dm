# 0004: Check-in and command protocol core

Status: accepted
Date: 2026-09-01
Phase: 2

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in>
- Doc: <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>
- Doc: <https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses>
- YAML: `third_party/device-management/mdm/checkin/*.yaml`, `mdm/commands/*.yaml`

## References read

- `micromdm/nanomdm@main` `mdm/checkin.go`, `mdm/command.go`, `mdm/mdm.go`, `mdm/type.go`, `service/nanomdm/service.go`
- `fleetdm/fleet@main` `pkg/mdm/mdmtest/apple.go` (device side: Authenticate, TokenUpdate, Idle loop, NotNow, Error, user channel)

## Known pitfalls found

- NanoMDM hides `context.Context` inside `mdm.Request` and check-in handlers return `[]byte` for DDM; every derivative re-wraps it.
- NanoMDM `Command` keeps only `CommandUUID` and `RequestType`; typed payloads and responses are left to callers (NanoCMD grew `mdmcommands` to compensate).
- NanoMDM #71: re-enrollment (`Authenticate`) does not clear bootstrap and unlock tokens.
- Shared iPad user channel is identified by the sentinel `UserID` `FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF`, with the real user in `UserShortName`; user enrollments identify by `EnrollmentID` and `EnrollmentUserID` rather than `UDID`.

## What they do

- **NanoMDM**: single-pass plist dispatch on `MessageType` via `UnmarshalPlist`; `Raw` bytes retained; `Enrollment.Resolved()` derives device/user/shared iPad/user-enrollment ids with `device:user` composite ids; `CommandResults` parsed to `CommandUUID`, `Status`, `ErrorChain`; `Idle` is a status; `NotNow` passes `skipNotNow` to the queue.
- **Fleet test client**: signs every body with detached PKCS7 in `Mdm-Signature`; `Idle` posts `Status: Idle` with `Topic`, `UDID`, `EnrollmentID`; user channel adds `UserID`; DDM check-ins carry `Endpoint` and `Data`.

## What we do better

1. Check-in messages decode straight into the generated `schema/checkin` types (single pass, `Raw` retained), so every wire key is typed and validated against the schema.
2. Commands carry typed payloads from `schema/commands`; `NewCommand` injects `RequestType` and an RFC 9562 UUIDv7; `DecodeResponse` resolves the typed response through the registry when the RequestType is known.
3. `Enrollment.Resolve` models five channels explicitly (device, user, shared iPad user, user-enrollment device, user-enrollment user) with the parent id, and rejects malformed combinations.
4. Decoding applies the `plist` size and depth limits, and rejects unknown message types and missing enrollment ids with sentinel errors.

## Verified by

1. `TestDecodeCheckinTypes`, `TestDecodeCheckinRejects` (unknown type, limits, missing id).
2. `TestNewCommandRoundTrip`, `TestDecodeResponseTyped`, `TestDecodeResponseIdleAndError`.
3. `TestEnrollmentResolve` (table over all five channels and invalid mixes).
4. `FuzzDecodeCheckin`, `FuzzDecodeResponse`.

## Rejected alternatives

- Untyped `map[string]any` messages: loses validation and the generated support metadata.
- Two-pass decode (type then message): simpler but doubles parsing of every check-in.
