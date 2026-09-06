// Package webauth is an OpenID Connect relying party for the enrollment
// web view: it starts an authorization code flow with PKCE and a nonce,
// verifies the returned id_token itself, and hands the authenticated
// claims, still bound to the device that opened the web view, to the
// caller's hooks.
//
// # Why
//
// Automated Device Enrollment can open a web view at configuration_web_url
// and the account-driven flows open one at the apple-as-web URL; either
// way the server has to authenticate a person before it serves the
// enrollment profile, and the web view is known to lose cookies. Phase 6
// of the plan of record therefore keys the whole exchange on the OAuth
// state parameter: 128 bits of randomness, single-use, expiring within
// minutes, and bound in a StateStore to the serial and UDID the caller
// parsed from MachineInfo (decision record 0027, claims 5 and 6; record
// 0028 reuses it for the apple-as-web page).
//
// This package owns the relying-party mechanics: the authorization
// request with response_type=code, PKCE S256, nonce and optional
// login_hint; the state lifecycle; the callback that parses error,
// error_description and error_uri (access_denied is 403, anything else
// from the provider 502); the code exchange with the code verifier and
// either client authentication method; discovery and JWKS fetching with a
// cache and refresh on an unknown key id; and id_token verification
// restricted to ES256 and RS256 with issuer, audience, expiry, issued-at
// and nonce checks. Every provider endpoint and the redirect URL must be
// https, response bodies are bounded, and the id_token is verified with
// the standard library only. It deliberately leaves out building or
// serving the enrollment profile (the Complete hook does that from
// mdmprotocol/enroll/ade or mdmprotocol/enroll/accountdriven), MachineInfo parsing, SAML, and
// SQL-backed state (a StateStore implementation elsewhere). The
// webauthtest subpackage is the fake provider the tests and the simulator
// drive.
//
// # References
//
//   - Decision record 0027: docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Decision record 0028: docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views
//   - Apple: https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile (configuration_web_url, anchor_certs)
//   - OpenID Connect Core 1.0: https://openid.net/specs/openid-connect-core-1_0.html (sections 3.1.2, 3.1.3)
//   - OpenID Connect Discovery 1.0: https://openid.net/specs/openid-connect-discovery-1_0.html
//   - RFC 6749 (OAuth 2.0): https://www.rfc-editor.org/rfc/rfc6749
//   - RFC 7636 (PKCE): https://www.rfc-editor.org/rfc/rfc7636
//   - RFC 7515 (JWS), RFC 7517 (JWK), RFC 7518 (JWA), RFC 7519 (JWT)
//   - RFC 9700 (OAuth 2.0 Security Best Current Practice): https://www.rfc-editor.org/rfc/rfc9700
package webauth
