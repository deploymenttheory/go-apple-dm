// Package proxyserver is the ddm role's ingress for check-ins forwarded by
// the mdm role.
//
// # Why
//
// It serves exactly one route, POST /v1/declarative-management, whose body
// is the DeclarativeManagement check-in plist as the device sent it, and
// answers with Apple's status and body so the mdm role can relay them
// unchanged. The enrollment is resolved from the forwarded message with the
// same decoder the device path uses, never from a header. Callers are
// authenticated by any combination of an HMAC over the body (RecvKey), a
// verified TLS client certificate (ClientCAs), and a bearer token or other
// check (Auth); every response body is signed with SendKey when set,
// whatever the status; rejections carry no detail. The handler is mounted
// by internal/app on the ddm role and runs in the container CI builds.
//
// # References
//
//   - Decision record 0023: docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Decision record 0025: docs/research/decisions/0025-reference-server-roles-and-container.md
//   - Threat model: docs/security/threat-model.md (trust boundary 5)
//   - E2E scenarios: docs/testing/e2e-scenarios.md (E2E-010)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
package proxyserver
