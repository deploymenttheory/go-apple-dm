# 0009: Configuration profiles and the enrollment profile builder

Status: accepted
Date: 2026-09-01
Phase: 3

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/profile-specific-payload-keys> (TopLevel, CommonPayloadKeys)
- Doc: <https://developer.apple.com/documentation/devicemanagement/mdm> (MDM payload: IdentityCertificateUUID, Topic, ServerURL, CheckInURL, SignMessage, AccessRights, ServerCapabilities)
- Doc: <https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles>
- YAML: `mdm/profiles/TopLevel.yaml`, `CommonPayloadKeys.yaml`, `com.apple.mdm.yaml`, `com.apple.security.scep.yaml`, `com.apple.security.root.yaml`

## References read

- `jessepeterson/cfgprofiles` (hand-written profile and payload structs used by MicroMDM)
- `micromdm/micromdm@main` `platform/profile`, `mdm/enroll` (enrollment profile assembly, AccessRights constants)
- `deploymenttheory/go-sdk-appleservices` `device_management/mdm/profile.go` (emit-only builder; no signing; payload UUIDs derived from content)
- `hslatman/mobileconfig-signer` (CMS attached signing)

## Known pitfalls found

- Payload UUIDs derived from content change when content changes, which makes devices treat an updated profile as a different payload; stable UUIDs are needed for in-place updates.
- Signed profiles are CMS *attached* signatures (content inside), unlike `Mdm-Signature`, which is detached; mixing them up yields a profile the device rejects.
- `AccessRights` is a bit mask that Apple documents only in prose; omitting it defaults to 8191 (all rights) on some OS versions but not others.
- The SCEP payload's `Subject` is an array of arrays of pairs, a shape that is easy to get wrong.

## What they do

- **cfgprofiles**: typed `Profile{PayloadContent []any}` with a handful of payload structs; marshalled with micromdm/plist.
- **MicroMDM**: builds the enrollment profile from SCEP plus MDM payloads with constants for AccessRights.

## What we do better

1. `profile.Profile` composes any generated payload (`schema/profiles`) with the common keys; identifiers and UUIDs are explicit and stable, generated once by the caller (UUIDv7 helper) rather than derived from content.
2. `profile.Sign` produces the attached CMS signature devices expect; `profile.Parse` reads signed or unsigned profiles back into typed payloads through the registry.
3. `enroll.Profile` builds the MDM enrollment profile with SCEP or a pre-issued identity (PKCS #12), an optional root certificate payload, typed `AccessRights` bits, `ServerCapabilities`, `SignMessage` on by default, and validates the result against the schema before returning it.

## Verified by

1. `profile.TestBuildAndParse`, `profile.TestStableUUIDs`.
2. `profile.TestSignAttached` (round trip through `cms` attached verification).
3. `enroll.TestEnrollmentProfile`, `e2e.TestE2E_SCEPEnrollPush`.

## Rejected alternatives

- Content-derived UUIDs: breaks in-place updates.
- Hand-written payload structs: the generated ones already cover all 127 payloads with validation.
