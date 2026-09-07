# 0023: DDM adapters and the internal wire contract

## Context

Declarative device management is an extension of MDM. Running its engine in another process is a deployment choice; Apple does not specify that internal connection.

## Decision

`inproc` serves the engine directly. `proxyclient` and `proxyserver` exchange the original Apple check-in plist through `POST /v1/declarative-management`. Enrollment identity is resolved from that message. The adapters share `DMResponse` and preserve device-facing status and body semantics, including 404 for an unknown declaration and empty 200 for status.

The internal protocol supports HMAC-SHA256, mutual TLS and optional bearer authentication. Request MACs cover the body; response MACs cover status and body. Both sides bound messages, and the client has a timeout. The reference server requires request and response HMAC keys for a split deployment.

## Rationale

Forwarding exact bytes avoids a second enrollment representation and preserves the signed request content. Status authentication prevents a signed empty response from being substituted under another status.

## Constraints

Library adapter authentication options require explicit configuration. HMAC authenticates but does not encrypt traffic; use TLS or a protected network. The protocol has no nonce/replay store and does not implement NanoMDM's `-dm` header contract. The reference server does not expose every library mTLS/bearer option.

## Verification

Adapter tests compare in-process and proxied responses, exact forwarded bytes, channel resolution, signatures, status tampering, limits and timeouts. The split-deployment end-to-end scenario uses the project's container.

## References

- [server/ddmadapter](../../../server/ddmadapter)
- [server/service](../../../server/service)
- <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- <https://developer.apple.com/documentation/devicemanagement/check-in>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@4948319`, `service/dmhook/dmhook.go`, `-dm`, `cmd/nanomdm/main.go`, `docs/operations-guide.md`
- `jessepeterson/kmfddm@4b75a76`, `http/ddm/ddm.go`, `http/http.go`
- `micromdm/nanohub@3d73c1a`, `ddmadapter/ddmadapter.go`
