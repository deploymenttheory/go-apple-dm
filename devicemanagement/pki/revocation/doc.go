// Package revocation records issuance and irreversible revocation and publishes
// issuer-signed CRL and OCSP status.
//
// # Design
//
// The optional registry supplies CA, SCEP, ACME and MDM with a shared status
// source. Unknown, expired and revoked certificates remain distinct from valid
// issued certificates. Issuer keys are crypto.Signer values. CRL numbering and
// signed publication commit under an issuer lock, and revocation marks
// publication for refresh.
//
// The caller selects revocation policy, publication URLs and lifetimes and must
// persist issuance before returning certificates. Certificate pinning and
// account-driven association are separate controls; importing a certificate here
// creates neither. Certificate hold and remove-from-CRL are unsupported.
//
// # Errors
//
// ErrRevoked, ErrExpired, ErrUnknown and ErrInvalid are the catalogued client
// conditions DM-PKI-CERTIFICATE-REVOKED, DM-PKI-CERTIFICATE-EXPIRED,
// DM-PKI-ISSUER-UNKNOWN and DM-PKI-REVOCATION-INVALID: the administration
// API raises them for a revocation request, so a repeated revocation is a
// Conflict and a bad reason or certificate is the caller's. A device presenting
// such a certificate meets them as a bare status.
//
// # References
//
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - RFC 5280 sections 5 and 6; RFC 6960; RFC 8555 section 7.6
//   - Zentral conf/mdm/docker/nginx/conf.d/zentral-clicertauth.conf
//   - MicroMDM server/devicecert.go
package revocation
