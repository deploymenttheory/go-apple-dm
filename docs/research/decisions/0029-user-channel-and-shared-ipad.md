# 0029: User channel, multiple users, and Shared iPad

Status: proposed
Date: 2026-09-02
Phase: 6

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (`UserAuthenticate`, `TokenUpdate` with `UserID`, `UserShortName`, `UserLongName`, `NotOnConsole`, `EnrollmentUserID`, `ManagedAppleID`)
- Doc: <https://developer.apple.com/documentation/devicemanagement/implementing-device-management> (user channel commands on macOS; Shared iPad user sessions)
- Doc: <https://developer.apple.com/documentation/devicemanagement/profile> (`is_multi_user` for Shared iPad requires `com.apple.mdm.per-user-connections`)
- YAML: `third_party/device-management/mdm/checkin/userauthenticate.yaml`, `tokenupdate.yaml`, `authenticate.yaml`, `checkout.yaml`; `mdm/commands/*.yaml` `supportedOS.*.userchannel` and `sharedipad` metadata (which request types the user channel accepts); `mdm/profiles/com.apple.mdm.yaml` (`ServerCapabilities` `com.apple.mdm.per-user-connections`)

## References read

- `micromdm/micromdm@904493b` `mdm/checkin.go:26-30` (410), `platform/user/worker.go:61-95`, `platform/device/worker.go:171-192`, `platform/queue/queue.go:57-117`
- `fleetdm/fleet@b44343c` `server/service/apple_mdm.go:5247-5250` (410), `:4791-4808` (push to the first user), `server/mdm/nanomdm/mdm/type.go:68-98` (enrollment types incl. `SharediPad`)
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/public_views/mdm.py`, `models.py` (`EnrolledUser`)
- Records 0004 (check-in core), 0016 (UserAuthenticate challenge state), 0020 (DDM membership per channel)

## Known pitfalls found

- MicroMDM `mdm/checkin.go:26-30` and Fleet `apple_mdm.go:5247-5250`: `UserAuthenticate` always 410, so network and Shared iPad users never get a channel.
- MicroMDM `platform/user/worker.go:76-86`: a new `UserID` deletes the device's other users; `device/worker.go:171-192`: a user-channel `CheckOut` un-enrols the device; `queue.go:57-117`: the queue is keyed by "UDID" with the user id in the same slot and no channel validation; `checkin_event.go:135` misspelt DM case drops user-channel DDM data.
- Fleet `apple_mdm.go:4791-4808`: pushes go to the first enabled user enrollment chosen by `created_at`; `NotOnConsole` unused; no Shared iPad handling outside the type resolver.
- nanomdm #8 (UserAuthenticate rejection), #202 (MySQL user table schema).
- Zentral `public_views/mdm.py:488-492` `UserAuthenticate` always 410; `models.py:1489` `EnrolledUser.user_id` globally unique so a `UserID` reused on another device re-parents the row; `public_views/mdm.py:398-399` the Shared iPad sentinel `UserID` is dropped and later user-channel Connects abort; `ManagedAppleID` from `TokenUpdate` never parsed.

## What they do

- **MicroMDM**: rejects `UserAuthenticate`; one user per device; push keyed by `UserID`; commands addressed by id string.
- **Fleet**: rejects `UserAuthenticate`; user-scoped profiles and declarations to the first user channel; `CertificateList` per channel.
- **Zentral**: `EnrolledUser` rows per device, per-command `verify_channel_and_device`, user-channel commands, per-user push, `awaiting_configuration` seeded at `Authenticate` and cleared by `DeviceConfigured`.

## What we do better

1. `UserAuthenticate` policy is per enrollment through the existing `UserAuthenticateHandler` (0016): accept with an empty 200, digest challenge, or decline with 410; the default accepts, so network users and Shared iPad users get a channel. The `AuthToken` from the digest flow gates the user channel's `TokenUpdate` when the policy issued one.
2. Many users per device: user channels are keyed by (`ParentID`, `UserID`) as they already are in `storage.EnrollmentStore`; a new `UserID` never removes another; `NotOnConsole`, `UserShortName`, `UserLongName`, and `EnrollmentUserID` are stored; a user-channel `CheckOut` disables only that user and publishes `CheckedOut` with the user id; a device `CheckOut` cascades (already the case).
3. Commands carry their channel: `Core.Enqueue` validates each target's channel against the request type's `userchannel` and `sharedipad` support metadata from `schema/commands` (`support.Target`), so a device-only request type addressed to a user channel is `ErrInvalid` at enqueue time and never reaches the device; push after enqueue goes to the exact channel targeted, not the first user.
4. Shared iPad: `is_multi_user` in the DEP profile (0026) requires and the builder adds `com.apple.mdm.per-user-connections`; the Shared iPad user check-in (`UserID` `FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF`, the user identified by `UserShortName`) resolves to `mdm.ChannelSharedIPadUser` on the device (already in `mdm.Enrollment.Resolve`), and a device channel with such a user is treated as a Shared iPad for support checks; `UserList`, `LogOutUser`, `SettingsCommand` for Shared iPad are enqueued on the device channel per the metadata, and per-user settings on the user channel.
5. DDM on the user channel: the 0020 scope rule (a set assigned to a device does not apply to its user channels) is kept, and `service.Hook` cleanup on user `CheckOut` clears only that channel (0020 claim already tested); the simulator's `User` gains `UserAuthenticate` (digest and empty) and Shared iPad session helpers.
6. The e2e harness enrols a macOS user channel through `UserAuthenticate` and `TokenUpdate`, enqueues a user-channel command and a device-only command addressed to the user (rejected), pushes, and receives the result on the right channel (E2E-013).

## Verified by

1. `service.TestUserAuthenticatePolicy/{AcceptEmpty,Digest,Decline410,DefaultAccepts,TokenGatesTokenUpdate}` (would fail on MicroMDM and Fleet, both 410).
2. `storagetest.RunEnrollmentSuite/{ManyUsersPerDevice,ReusedUserIDOnOtherDevice,UserCheckOutDisablesOnlyUser,UserFieldsRoundTrip}` (the first would fail on MicroMDM), `service.TestCheckOut/UserChannel`.
3. `service.TestEnqueue/{ChannelValidatedAgainstMetadata,DeviceOnlyToUserRejected,SharedIPadOnly}` (a device-only type to the user channel would be queued on MicroMDM); pushes are addressed by the exact `EnrollmentID` already (`push.Notifier`), so no separate test is needed.
4. `enroll.TestSharedIPadCapability`, `service.TestSharedIPad/{LoggedInUserChannel,DeviceScopedCommands}`, `simulator.TestSharedIPadUser`. `ManagedAppleID` is not carried by any check-in message (checked against `mdm/checkin/*.yaml`); it is stored from the enrollment flow (0028 claim 6).
5. `ddm.TestServiceHook/UserCheckOutClearsOnlyUser` (exists), `simulator.TestUserAuthenticate/*`.
6. `e2e.TestE2E_UserChannel` (E2E-013), `e2e.TestE2E_SharedIPad` (E2E-020).

## Rejected alternatives

- Declining `UserAuthenticate` by default (references): loses network and Shared iPad users; the policy hook keeps 410 available.
- Routing commands by an id string (MicroMDM): channel validation at enqueue needs the typed `EnrollmentID`.
- Pushing to the first user channel (Fleet): the enqueue already knows the channel.
