# 0023: DDM adapters and the internal wire contract

Status: accepted
Date: 2026-09-02
Phase: 5

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (the `DeclarativeManagement` check-in message)
- YAML: `third_party/device-management/mdm/checkin/declarativemanagement.yaml` (`Endpoint`, `Data`, status codes: 200 JSON, 200 empty for `status`, 404 for an unknown declaration)

Framing: Apple defines the device-facing protocol (the check-in message with `Endpoint` and `Data`, the JSON responses, and the status codes). Apple defines nothing about splitting an MDM server into processes. The hop between our `mdm` role and our `ddm` role is therefore ours, and it carries Apple's message unchanged rather than any third party's header scheme.

## References read

- `micromdm/nanomdm@4948319` `service/dmhook/dmhook.go` (the `-dm` proxy), `cmd/nanomdm/main.go`, `docs/operations-guide.md`
- `jessepeterson/kmfddm@4b75a76` `http/ddm/ddm.go`, `http/http.go`
- `micromdm/nanohub@3d73c1a` `ddmadapter/ddmadapter.go` (in-process adapter between NanoMDM and KMFDDM)

## Known pitfalls found

- NanoMDM: the upstream URL is built with `ResolveReference`, so a `-dm` prefix without a trailing slash loses its last path segment.
- NanoMDM: the enrollment is identified by trusted `X-Enrollment-ID`, `X-Enrollment-Type`, and `X-Enrollment-ParentID` headers with no authentication of the caller.
- NanoMDM: every non-200 upstream status is relayed to the device as 500, so Apple's 404 (remove the declaration) never reaches the device.
- NanoMDM: no client timeout and no response body limit.
- NanoMDM: the HMAC is verified on the response only when configured, and nothing rejects an unsigned request on the other side.
- KMFDDM: the device-facing endpoints under `/` are unauthenticated and rely on the caller's headers; unknown declaration answers 404 but a malformed status body answers 500.
- NanoHUB: the in-process adapter couples the two code bases at the Go API level, so both must move together.

## What they do

- **NanoMDM `-dm`**: forwards `GET` (no `Data`) or `PUT` (with `Data`) to `<prefix>/<Endpoint>` with the `X-Enrollment-*` headers, `Content-Type: application/json`, and an optional base64 HMAC-SHA256 in `X-Hmac-Signature` over the request body; relays 200 bodies, maps everything else to 500.
- **KMFDDM**: serves those routes, trusts the headers, has no signature check.
- **NanoHUB**: calls KMFDDM's engine directly from NanoMDM's `DeclarativeManagement` service method.

## What we do better

1. `ddm/adapter/internal/proxywire` defines one route, `POST /v1/declarative-management`, whose body is the Apple check-in message itself as a plist (`application/x-apple-aspen-mdm-checkin`, the same bytes the device sent: `MessageType`, `UDID`, `EnrollmentID`, `EnrollmentUserID`, `UserID`, `UserShortName`, `UserLongName`, `Endpoint`, `Data`); the receiving side resolves the enrollment with `mdm.DecodeCheckin` and `Enrollment.Resolve` exactly as the device path does. No `X-Enrollment-*` headers, no URL rewriting.
2. Authentication is `X-MDM-Signature` (base64 HMAC-SHA256 over the body with a shared key) in both directions, mutual TLS (client certificate pinned by the `ddm` role), or both, plus an optional bearer token; an unsigned or wrongly signed request is 401 and the response is signed for every status.
3. Body limits on both sides (`MaxBody`, 413 at the server, an error at the client) and a client timeout (default 30s).
4. Apple's status and body are relayed verbatim: 200 JSON, the empty 200 for `status`, 404 for an unknown declaration so the device removes it, 400 for a malformed endpoint; 5xx and transport errors map to `CodeInternal`.
5. `service.DMHandler` gains the raw check-in (`*mdm.Checkin`) so `proxyclient` forwards the exact plist without re-encoding, and `inproc`, `proxyserver`, and `proxyclient` share one `DMResponse` shape so the same test table drives all three.
6. `ddm/adapter/inproc.Handler(engine)` is the single-process path: endpoint switch, JSON content type, 404 for unknown declaration or kind, 400 for a malformed endpoint or bad status, empty 200 for status, 500 only for an engine error.

## Verified by

1. `proxyserver.TestCheckin/DeviceChannel`, `/UserChannel`, `/Malformed400`, `/NotDeclarativeManagement400`, `/TooLarge413`, `proxyclient.TestForward/RawPlistForwarded`, `/ContentType`, `/AuthHeader` (prove claim 1; would fail on NanoMDM because the enrollment comes from headers and the path is rewritten).
2. `proxywire.TestSignVerify/OK`, `/Missing`, `/Wrong`, `proxyserver.TestSignature/Signed`, `/MissingRejected`, `/WrongKeyRejected`, `/ResponseSigned`, `/ResponseSignedOn404`, `proxyserver.TestAuth/Bearer`, `/MTLS`, `/Rejected`, `proxyclient.TestSignature/ResponseVerified`, `/BadResponseSignature`, `proxyclient.TestForward/Signed` (prove claim 2; would fail on KMFDDM because nothing rejects an unsigned request).
3. `proxywire.TestReadBody/TooLarge`, `proxyclient.TestTimeout`, `proxyclient.TestBodyLimit`, `proxyclient.TestTransportError`, `inproc.TestHandler/StatusTooLarge400` (prove claim 3; would fail on NanoMDM because there is no timeout or limit).
4. `proxyclient.TestRelay/200Body`, `/404Stays404`, `/EmptyStatus200`, `/400IsBadRequest`, `/5xxIsUpstreamError`, `/401IsUpstreamError`, `proxyserver.TestBackendErrors/404`, `/400`, `/500`, `inproc.TestHandler/Declaration404` (prove claim 4; would fail on NanoMDM because every non-200 becomes 500).
5. `proxyserver.TestHandleParity` (`inproc` and `proxyserver` answer the same table identically from one engine), `proxyclient.TestRoundTripThroughProxyServer` (a real `service.Core` through `proxyclient`, `proxyserver`, and the engine, relaying the same bodies and statuses), `proxyclient.TestHandler/EmptyURL`, `/BadURL`, `proxyserver.TestRoutes/OnlyPost`, `/WrongPath404`, `/WrongContentType415` (prove claim 5).
6. `inproc.TestHandler/Tokens`, `/Items`, `/Declaration`, `/Status`, `/BadEndpoint400`, `/EngineError500` (prove claim 6).
7. `e2e.TestE2E_DDMSplitDeployment` (E2E-010) runs the simulator through `proxyclient`, the network, and `proxyserver` in our own container (0025) and asserts the forwarded message equals what the simulator sent and that Apple's status codes come back unchanged.

## Rejected alternatives

- Importing NanoMDM packages or running a NanoMDM container for interop (user decision 2026-09-02): no dependence on NanoMDM or MicroMDM code beyond `micromdm/plist`; their repositories are read-only references.
- Implementing NanoMDM's `-dm` header contract for compatibility: it is not an Apple requirement, it identifies the enrollment by an unauthenticated header, and it cannot carry a 404 to the device.
- Forwarding a re-encoded JSON check-in: re-encoding could change the bytes a signature covers; the plist is forwarded as received.
- Signature on the response only (NanoMDM): the request is the one that mutates state.
- Custom URL per endpoint (`/tokens`, `/declaration/...`): the `Endpoint` string is already in the message and Apple owns its grammar; one route keeps the parser in one place.
