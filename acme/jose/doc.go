// Package jose parses, verifies and produces the JSON Web Signatures and
// JSON Web Keys an ACME server exchanges with its clients: the flattened
// JWS serialisation, the protected header ACME insists on, EC and RSA
// public keys in JWK form, and RFC 7638 key thumbprints.
//
// # Why
//
// Every ACME request after the directory and the new-nonce fetch is a POST
// whose body is a JWS, and the server has to make up its mind about that
// body before it knows anything else about the request: which key signed
// it, whether the signature holds, whether the nonce and the URL the client
// claims to be addressing are the ones it actually addressed. Phase 7 of
// the plan of record adds the ACME CA that issues MDM identity certificates
// against device-attest-01, and this package is the layer underneath it.
//
// RFC 8555 section 6.2 narrows JOSE hard, and the narrowing is the point:
// the flattened serialisation only, no unprotected header, no detached
// payload, every parameter the server trusts inside the protected header,
// and exactly one of jwk (new accounts) or kid (everything after). Parse
// enforces that shape so the handlers above it never have to ask whether a
// field they are reading was authenticated. Thumbprint exists because the
// key authorisation a challenge is answered with is built from it.
//
// The package deliberately leaves out everything ACME does not need. There
// is no JWE, no general or compact serialisation, no MAC algorithm and no
// alg of "none": a request that asks for one of those is malformed, not a
// request to be handled differently. Nonce lifetime, account lookup by kid,
// URL binding to the request that carried it, and the attestation formats
// themselves belong to the acme package above; this one only reports what
// the header said and whether the signature over it verifies. It carries no
// dependency beyond the standard library.
//
// # References
//
//   - Decision record 0031: docs/research/decisions/0031-acme-server-and-attestation.md
//   - Plan of record: docs/research/implementation_plan.md (phase 7)
//   - RFC 8555 (ACME), section 6.2: https://www.rfc-editor.org/rfc/rfc8555#section-6.2
//   - RFC 7515 (JSON Web Signature): https://www.rfc-editor.org/rfc/rfc7515
//   - RFC 7517 (JSON Web Key): https://www.rfc-editor.org/rfc/rfc7517
//   - RFC 7518 (JSON Web Algorithms): https://www.rfc-editor.org/rfc/rfc7518
//   - RFC 7638 (JWK Thumbprint): https://www.rfc-editor.org/rfc/rfc7638
//   - RFC 7797 (unencoded payloads, rejected here): https://www.rfc-editor.org/rfc/rfc7797
package jose
