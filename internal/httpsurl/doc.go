// Package httpsurl validates service URLs before callers send credentials or
// device data to them.
//
// # Design
//
// Parse requires an absolute HTTPS URL with a hostname and rejects user
// information, fragments and opaque URLs. Errors omit the input because it may
// contain credentials. URL validation does not establish trust in the server;
// callers remain responsible for TLS verification and redirect policy.
//
// # References
//
//   - URL parsing: https://pkg.go.dev/net/url#Parse
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
package httpsurl
