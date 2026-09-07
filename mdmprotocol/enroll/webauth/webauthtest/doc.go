// Package webauthtest supplies an OpenID Connect provider and browser-flow
// helpers for enrollment tests.
//
// # Design
//
// The fake serves discovery and ES256/RS256 JWKS, records authorization
// requests, and validates code verifiers and client credentials at the token
// endpoint. Faults cover invalid nonce/audience/times, denied access and
// endpoint failures. It implements the authorization-code flow with S256 for
// reproducible relying-party and enrollment tests, not a general identity
// provider.
//
// # References
//
//   - Decision record 0027: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Decision record 0028: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views
//   - OpenID Connect Core 1.0: https://openid.net/specs/openid-connect-core-1_0.html
//   - OpenID Connect Discovery 1.0: https://openid.net/specs/openid-connect-discovery-1_0.html
//   - RFC 6749 (OAuth 2.0), RFC 7636 (PKCE), RFC 7515 (JWS), RFC 7517 (JWK)
package webauthtest
