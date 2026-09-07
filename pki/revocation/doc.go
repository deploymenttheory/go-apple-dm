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
// # References
//
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0047-enrollment-authentication-and-optional-security-services.md
//   - RFC 5280 sections 5 and 6; RFC 6960; RFC 8555 section 7.6
//   - Zentral conf/mdm/docker/nginx/conf.d/zentral-clicertauth.conf
//   - MicroMDM server/devicecert.go
package revocation
