# 0023: DDM adapters and the internal wire contract

## Context

Declarative device management is an extension of MDM. Running its engine in another process is a deployment choice; Apple does not specify that internal connection.

## Decision

`inproc` serves the engine directly. `proxyclient` and `proxyserver` exchange the original Apple check-in plist through `POST /v1/declarative-management`. Enrollment identity is resolved from that message. The adapters share `DMResponse` and preserve device-facing status and body semantics, including 404 for an unknown declaration and empty 200 for status.

Both adapters require HTTPS and independent HMAC-SHA256 keys of at least 32 bytes.
The versioned request envelope authenticates method, request target, content type,
timestamp, random nonce and body. Five-minute freshness and shared atomic replay
records retained for ten minutes prevent reuse across replicas. Response MACs
bind to the request envelope, status, content type and body. Both sides bound
messages; the client has a timeout and refuses redirects. Mutual TLS and bearer
checks are additional library options.

## Rationale

Forwarding exact bytes avoids a second enrollment representation and preserves the signed request content. Status authentication prevents a signed empty response from being substituted under another status.

## Constraints

The caller supplies a shared replay store. The reference server uses encrypted
SQL protocol state and requires native TLS on its DDM role. The explicit
programmatic `AllowInsecureForTests` exception accepts only literal loopback
HTTP. This protocol does not implement NanoMDM's `-dm` header contract.

## Verification

Adapter tests compare in-process and proxied responses, exact forwarded bytes, channel resolution, signatures, stale envelopes, cross-replica replay, request/response substitution, limits and timeouts. The split-deployment end-to-end scenario uses the project's container.

## References

- [server/ddmadapter](../../../server/ddmadapter)
- [server/service](../../../server/service)
- <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- <https://developer.apple.com/documentation/devicemanagement/check-in>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@4948319`, `service/dmhook/dmhook.go`, `-dm`, `cmd/nanomdm/main.go`, `docs/operations-guide.md`
- `jessepeterson/kmfddm@4b75a76`, `http/ddm/ddm.go`, `http/http.go`
- `micromdm/nanohub@3d73c1a`, `ddmadapter/ddmadapter.go`
