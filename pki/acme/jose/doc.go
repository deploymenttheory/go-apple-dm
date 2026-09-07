// Package jose parses, verifies and produces the JWS and JWK forms used by ACME.
//
// # Design
//
// Parse accepts flattened JWS with a protected header and exactly one of jwk or
// kid. EC and RSA public keys and RFC 7638 thumbprints support account
// authentication. The package rejects unsupported serializations, unprotected
// headers, MAC algorithms and alg none, and bounds input before verification.
//
// Nonce lifetime, account lookup and matching the protected url to a published
// endpoint belong to the ACME layer. The package uses the standard library and
// does not implement JWE or general WebAuthn authentication.
//
// # References
//
//   - Decision record 0031: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0031-acme-server-and-state-store.md
//   - RFC 8555 (ACME), section 6.2: https://www.rfc-editor.org/rfc/rfc8555#section-6.2
//   - RFC 7515 (JSON Web Signature): https://www.rfc-editor.org/rfc/rfc7515
//   - RFC 7517 (JSON Web Key): https://www.rfc-editor.org/rfc/rfc7517
//   - RFC 7518 (JSON Web Algorithms): https://www.rfc-editor.org/rfc/rfc7518
//   - RFC 7638 (JWK Thumbprint): https://www.rfc-editor.org/rfc/rfc7638
//   - RFC 7797 (unencoded payloads, rejected here): https://www.rfc-editor.org/rfc/rfc7797
package jose
