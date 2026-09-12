# 0029: User channel, multiple users, and Shared iPad

## Context

A device can have multiple user channels. Shared iPad and User Enrollment use channel identifiers and authentication behavior distinct from ordinary macOS users.

## Decision

User channels are keyed by parent device and user identity. User `CheckOut` disables only that channel; device lifecycle cleanup cascades to dependent users. Commands are validated against generated OS, version, channel and enrollment-mode metadata and pushes target the specified channel.

`UserAuthenticate` accepts by default, can decline with 410, or can use the digest handler. Optional `RequireUserAuth` requires completed digest state before ordinary macOS user-channel `TokenUpdate`. Shared iPad uses the all-`F` `UserID` sentinel and `UserShortName`; its profile includes the per-user-connections capability. DDM membership remains separate per channel.

## Rationale

Explicit channels prevent state, commands and configuration from being assigned to an unrelated user. Authentication policy remains configurable for environments with different user-management requirements.

## Constraints

Shared iPad and User Enrollment user channels do not perform `UserAuthenticate` and are exempt from that gate. The service does not validate a subsequent digest `AuthToken` carrier. Supervision, ADE status and user-approved MDM are assumed during target checks because the enrollment record does not track them; missing OS data also limits validation.

## Verification

Service and storage tests cover multiple users, reused user identifiers on different devices, channel-specific checkout, authentication policy and unsupported command targets. Simulator and end-to-end scenarios cover macOS user channels and Shared iPad.

## References

- [mdmprotocol/mdm](../../../devicemanagement/mdmprotocol/mdm)
- [server/service/userchannel_test.go](../../../server/service/userchannel_test.go)
- [server/service/checkin.go](../../../server/service/checkin.go)
- [simulator](../../../devicemanagement/simulator)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/implementing-device-management>
- <https://developer.apple.com/documentation/devicemanagement/profile>

Reference source identifiers and paths (relative to the named project):

- `micromdm/micromdm@904493b`, `mdm/checkin.go:26-30`, `platform/user/worker.go:61-95`, `platform/device/worker.go:171-192`, `platform/queue/queue.go:57-117`
- `fleetdm/fleet@b44343c`, `server/service/apple_mdm.go:5247-5250`, `:4791-4808`, `server/mdm/nanomdm/mdm/type.go:68-98`, `SharediPad`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/public_views/mdm.py`, `models.py`, `EnrolledUser`
