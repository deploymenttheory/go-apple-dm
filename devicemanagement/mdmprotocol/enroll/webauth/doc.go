// Package webauth implements an OpenID Connect relying party for enrollment
// browser authentication.
//
// # Design
//
// The authorization-code flow uses S256 PKCE, nonce validation and expiring
// one-use state bound to caller-supplied enrollment data. Provider discovery and
// JWKS are cached; ID-token verification accepts ES256 and RS256 and checks
// issuer, audience, times and nonce. Endpoints and redirects require HTTPS, and
// response bodies are bounded.
//
// Authorizer decides admission and Complete serves the result; the package does
// not build profiles. StateStore is injectable, with an in-memory implementation
// supplied. Replicated deployments need shared state or session affinity for the
// browser handoff. SAML, userinfo and refresh-token flows are outside this
// relying party's scope.
//
// # References
//
//   - Decision record 0027: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Decision record 0028: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0028-account-driven-enrollment-and-service-discovery.md
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
