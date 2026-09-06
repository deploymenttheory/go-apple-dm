# 0045: Return to Service, and a test that pairs the schema with the server

Status: accepted
Date: 2026-09-04
Phase: post-9 (protocol coverage)

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in>
- YAML: `third_party/device-management/mdm/checkin/returntoservice.yaml`

## References read

- `micromdm/nanomdm` `mdm/checkin.go`, `service/service.go`: the check-in message set it accepts
- `jessepeterson/kmfddm`: no check-in surface of its own; forwards to NanoMDM
- `fleetdm/fleet` `server/mdm/nanomdm`: vendored NanoMDM, same message set

## Known pitfalls found

- **This repository, before this record.** `ReturnToService` has been in the generated check-in
  registry since the schema was pinned. It was decodable, documented, and covered by the
  round-trip conformance tests. `service.dispatchCheckin` had no case for it, so a supervised
  device that entered return-to-service mode and asked for its configuration was answered
  `unsupported message` with HTTP 400. Nothing failed; nothing reported it. The DEP client even
  models the enrollment-profile flag that puts a device into that mode
  (`dep.Profile.IsReturnToService`), so the repository knew the feature existed at one end and
  refused it at the other.
- NanoMDM and the projects that vendor it accept a fixed list of check-in messages written by
  hand, with no relationship to Apple's schema. A message Apple adds is silently unsupported
  there too, and there is nothing that would say so.

## What they do

- **NanoMDM**: a type switch over hand-written structs. New messages require someone to notice
  the release notes.
- **KMFDDM**, **Fleet**: inherit that set unchanged.

## What we do better

1. `ReturnToService` is answered. The handler is a seam on `service.Config`, like
   `DeclarativeManagement`, `GetToken` and `UserAuthenticate`, because whether a device may erase
   itself is deployment policy and not something a library decides.
2. The service attaches the escrowed bootstrap token when the policy leaves it empty. Apple's
   rule is that without the token the device erases fully and cannot preserve apps; the server is
   already holding the token from `SetBootstrapToken`, so a policy that never mentions it still
   gets app preservation rather than silently losing it.
3. **With no handler the answer is `Enabled: false`, not an error.** A missing handler must never
   be the difference between a device staying as it is and a device wiping itself, and a 501
   would leave the device with nothing to act on. The unconfigured server fails closed and says
   so in the log.
4. `TestEveryCheckinMessageIsHandled` pairs the generated registry against what the service
   actually dispatches, so the next message Apple adds arrives as a failing build rather than as
   a 400 in production. It sends a real check-in through the decoder and the dispatcher, so a
   case that falls through cannot satisfy it.

## Verified by

1. `service.TestEveryCheckinMessageIsHandled` (proves the gap is closed; removing the dispatch
   case reproduces the original failure verbatim: "Apple defines check-in ReturnToService and the
   generator emits it, but the service answers ... unsupported message ReturnToService").
2. `service.TestReturnToServiceAttachesTheStoredBootstrapToken`, and
   `TestReturnToServiceKeepsAHandlerSuppliedToken` proving the service fills the token in rather
   than overwriting a policy's own.
3. `service.TestReturnToServiceWithoutAHandlerAnswersDisabled`, and `e2e.TestE2E_ReturnToService`
   with `TestE2E_ReturnToServiceDisabledByDefault` (E2E-025, E2E-026) driving it through the
   assembled server with the device simulator.
4. `service.TestReturnToServiceRequiresAKnownEnrollment`: the message hands out a bootstrap
   token, so it sits behind the same certificate pinning as every other message after
   Authenticate.

## Rejected alternatives

- **Answer 501 when no handler is configured**: consistent with `GetToken`, and wrong here. There
  is no safe default token, but there is a safe default answer to "may I erase myself".
- **Require the policy to supply the bootstrap token**: honest about where the token comes from,
  and it makes forgetting it the easy path. The failure is silent and only visible as devices
  that lose their apps.
- **Read the dispatch switch with go/ast in the coverage test**: cheaper, and it would pass on a
  case that exists but falls through. The test drives the real path instead.
- **Implement return to service as a command rather than a check-in**: Apple defines it as a
  check-in the device initiates; the server cannot start it.
