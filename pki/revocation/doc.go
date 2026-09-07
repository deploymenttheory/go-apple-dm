// Package revocation records issued certificates and publishes issuer-signed status.
//
// # Why
//
// Certificate pinning is not revocation. This optional registry gives CA depots,
// SCEP, ACME and MDM a common durable status check without choosing when an
// organization should revoke a device. Unknown certificates remain distinct from
// issued and revoked certificates. Issuer keys stay behind crypto.Signer.
//
// # References
//
//   - docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - RFC 5280 sections 5 and 6; RFC 6960; RFC 8555 section 7.6
//   - Zentral conf/mdm/docker/nginx/conf.d/zentral-clicertauth.conf
//   - MicroMDM server/devicecert.go
package revocation
