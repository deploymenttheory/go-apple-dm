# 0002: plist encoding and decoding

Status: accepted
Date: 2026-09-01
Phase: 0

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (XML plist request bodies)
- Doc: <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>

## References read

- `micromdm/plist` README, `NewXMLDecoder`, `NewBinaryDecoder`, `Unmarshaler`
- `DHowett/go-plist` README
- `micromdm/nanomdm` `mdm/checkin.go` (`checkinUnmarshaller` single-pass dispatch on `MessageType`)
- `fleetdm/fleet` `go.mod` (imports both `micromdm/plist` and `howett.net/plist`)
- `deploymenttheory/go-sdk-appleservices` `internal/plistenc` (hand-rolled XML encoder, encode only)

## Known pitfalls found

- Fleet carries two plist libraries plus the legacy `groob/plist` path, which drifts.
- go-sdk-appleservices hand-rolled an encoder and therefore cannot decode device responses at all.
- Devices can send binary plists; a decoder that only speaks XML fails on those.

## What they do

- **NanoMDM**: `micromdm/plist`, single-pass dispatch via `UnmarshalPlist(func(any) error)`, `Raw` bytes retained on every message.
- **Fleet**: `micromdm/plist` for protocol code, `howett.net/plist` elsewhere.
- **go-sdk-appleservices**: internal encode-only writer.

## What we do better

1. One library, `github.com/micromdm/plist`, wrapped in a local `plist/` package that adds `DetectFormat`, `MaxBytes`, `MaxDepth`, and dispatch helpers, so the choice is swappable in one place and untrusted input is bounded.
2. Both XML and binary accepted on every decode path; fuzz targets cover both.

## Verified by

1. `TestPlistLimits`, `TestDetectFormat` (phase 1).
2. `FuzzCheckinDecode`, `FuzzResponseDecode` seeded with XML and binary fixtures (phase 2).

## Rejected alternatives

- `howett.net/plist` only: no interop advantage and the reference fixtures use micromdm/plist conventions.
- Hand-rolled encoder: proven dead end in the SDK.
