// Package webauthtest is a fake OpenID Connect provider for tests of the
// enrollment web view: discovery, JWKS with ES256 and RS256 keys, an
// authorization endpoint that records what the relying party sent, a
// token endpoint that checks the PKCE verifier and client credentials,
// scripted failures, and a web-view-like client that follows the
// redirects the way the device does.
//
// # Why
//
// The webauth relying party must be exercised against a provider that
// misbehaves on cue: a wrong nonce, an expired id_token, a foreign
// audience, an unknown key id, access_denied with a description, a token
// endpoint outage, and discovery that advertises plain http. No public
// provider does that on demand, and the reference implementations test
// against mocks that skip PKCE and nonce entirely. Phase 6 of the plan of
// record needs this fake for webauth's own tests, for the ADE and
// account-driven handlers, and for the simulator's web view enrollment
// (decision record 0027, claim 6). It deliberately implements only the
// authorization code flow with S256 and leaves out refresh tokens, the
// userinfo endpoint, and every other response type.
//
// # References
//
//   - Decision record 0027: docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Decision record 0028: docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views
//   - OpenID Connect Core 1.0: https://openid.net/specs/openid-connect-core-1_0.html
//   - OpenID Connect Discovery 1.0: https://openid.net/specs/openid-connect-discovery-1_0.html
//   - RFC 6749 (OAuth 2.0), RFC 7636 (PKCE), RFC 7515 (JWS), RFC 7517 (JWK)
package webauthtest
