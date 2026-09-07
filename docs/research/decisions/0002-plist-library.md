# 0002: plist encoding and decoding

## Context

MDM requests and responses use property lists. Untrusted input requires format detection and resource limits.

## Decision

`mdmprotocol/plist` wraps `github.com/micromdm/plist` for XML and binary encoding and decoding. The wrapper provides format detection and byte and XML nesting limits. Protocol decoding dispatches on message type and retains the original request bytes.

## Rationale

A shared wrapper gives protocol callers one codec and one place to configure input bounds. Supporting both formats allows the same typed messages to handle XML and binary inputs.

## Constraints

Callers must use the bounded decode path for untrusted input. The wrapper's limits are implementation controls, not additional Apple wire fields.

## Verification

Plist tests cover format detection, malformed input and limits. Protocol fuzz targets exercise check-in and response decoding.

## References

- [mdmprotocol/plist](../../../mdmprotocol/plist)
- [mdmprotocol/mdm](../../../mdmprotocol/mdm)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>

Reference source identifiers and paths (relative to the named project):

- `micromdm/plist`, `NewXMLDecoder`, `NewBinaryDecoder`, `Unmarshaler`
- `DHowett/go-plist`
- `micromdm/nanomdm`, `mdm/checkin.go`, `checkinUnmarshaller`, `MessageType`
- `fleetdm/fleet`, `go.mod`, `micromdm/plist`, `howett.net/plist`
- `deploymenttheory/go-sdk-appleservices`, `internal/plistenc`
