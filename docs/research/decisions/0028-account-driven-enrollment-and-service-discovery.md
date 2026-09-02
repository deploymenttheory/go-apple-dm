# 0028: Account-driven enrollment and service discovery

Status: proposed
Date: 2026-09-02
Phase: 6

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment>
- Doc: <https://developer.apple.com/documentation/devicemanagement/get-.well-known-com.apple.remotemanagement> (`model-family`, `user-identifier`, `WellKnown{Servers[{Version, BaseURL}]}`)
- Doc: <https://developer.apple.com/documentation/devicemanagement/implementing-the-simple-authentication-account-driven-enrollment-flow> (steps 1 to 12: redirect without query parameters, Apple's fallback discovery, `mdm-byod`/`mdm-adde`, signed plist body `LANGUAGE`/`PRODUCT`/`VERSION`, 401 `WWW-Authenticate: Bearer method="apple-as-web", url=...`, `user-identifier` on the web-auth URL, 308 to `apple-remotemanagement-user-login://authentication-results?access-token=`, second POST with `Authorization: Bearer`)
- Doc: <https://developer.apple.com/documentation/devicemanagement/implementing-the-oauth2-authentication-account-driven-enrollment-flow> (`method="apple-oauth2"`, `authorization-url`, `token-url`, `redirect-url` with scheme `apple-remotemanagement-user-login`, `client-id`, `scope`, `login_hint`, authorization code grant, refresh tokens)
- Doc: <https://developer.apple.com/documentation/devicemanagement/implementing-the-enrollment-sso-flow>, <https://developer.apple.com/documentation/devicemanagement/enrollmentssodocument>
- YAML: `third_party/device-management/mdm/errors/well-known.failed.yaml` (403 `com.apple.well-known.failed`), `mdm/profiles/com.apple.mdm.yaml` (`AssignedManagedAppleID`, `EnrollmentMode` `BYOD|ADDE`, `RequiredAppIDForMDM`, `ManagedAppleID` removed in iOS 18/macOS 15), `mdm/checkin/gettoken.yaml` and `tokenupdate.yaml` (`EnrollmentID` required and `UDID` forbidden for user enrollments), `other/esso.yaml`

## References read

- `vbnin/Apple-JSON-discovery-server@da4599b` `default-ssl.conf`, `json_files/`, `README.md`
- `fleetdm/fleet@b44343c` `server/service/handler.go:1425-1455` (service discovery), `server/service/apple_mdm.go:2785-2882` (bearer flow, 400 for unsupported products), `ee/server/service/apple_mdm.go`, `ee/server/service/mdm.go:1039-1061` (single-use challenge), `server/mdm/apple/apple_mdm.go:1332-1412` (BYOD profile), `docs/Contributing/product-groups/mdm/apple-account-driven-user-enrollment.md`
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/public_views/user.py` (308 hand-back, discovery JSON)
- Record 0026 (DEP account-driven discovery assignment), 0009 (enrollment profile builder), 0016 (user auth state)

## Known pitfalls found

- Apple-JSON-discovery-server `default-ssl.conf:28-48`: routing by substring on the raw query string; `user-identifier` ignored; no rejection path; no `HEAD`/method handling.
- Fleet `handler.go:1425-1455`: serves `mdm-byod` for every platform and does not serve `/.well-known/com.apple.remotemanagement` itself (relies on ABM assignment); `#41066` account-driven device enrollment for macOS unsupported; `apple_mdm.go:2871-2882` non-iOS answered 400; `ee/server/service/apple_mdm.go:40-96` the challenge is consumed on the profile fetch and then re-read on `Authenticate`, so a failed first check-in cannot be retried; `apple_mdm.go:7248-7256` renewal may send an empty `AssignedManagedAppleID`; `lifecycle.go:150-158` `EnrollmentID` overloads UUID and serial; `godep/account_driven_enrollment.go:35-37` remove endpoint and error table left as TODO; `#19329`, `#30871`, `#32152`.

- Zentral `public_views/user.py:24-77`: discovery ignores `user-identifier` and always answers `mdm-byod`; the first POST body is never read (no OS gate, no ACME selection); every unauthenticated POST mints a new session; a replayed bearer is an unhandled 500; the token never expires; `models.py:2514` the managed Apple ID is assumed equal to the realm email; `public_views/mdm.py:204-206` `EnrollmentID` written into the serial and UDID columns.

## What they do

- **Apple-JSON-discovery-server**: static JSON per `model-family` (Mac and RealityDevice get `mdm-adde`, iPhone and iPad `mdm-byod`), `Content-Type: application/json`, 200.
- **Fleet**: per-token discovery URL assigned to ABM hourly; PKCS7 body decoded without serial or UDID; 401 with `method="apple-as-web"`; SAML web view mints a one-hour single-use challenge; `apple-remotemanagement-user-login://authentication-results?access-token=`; second POST consumes the challenge and returns the signed BYOD profile (`EnrollmentMode: BYOD`, `AssignedManagedAppleID` from the IdP, `ServerCapabilities` gains `UserEnrollment`, `PayloadScope: User`, no `AccessRights`); the bearer reappears on `Authenticate` and selects the team.
- **Zentral**: discovery behind a secret path; 308 hand-back.

## What we do better

1. `enroll/discovery.Handler` serves `GET`/`HEAD /.well-known/com.apple.remotemanagement`: exact parsing of `model-family` and `user-identifier`, a `Router(ctx, Request{ModelFamily, UserIdentifier, RawQuery}) ([]Server, error)` hook returning entries with `Version` (`mdm-byod` or `mdm-adde`) and an absolute `https` `BaseURL` (a `StaticRouter` table covers the common case), the response body exactly `{"Servers":[{"Version":...,"BaseURL":...}]}` with `Content-Type: application/json` and `Cache-Control: no-store`, 405 for other methods, and rejection as 403 `com.apple.well-known.failed` (JSON or plist by `Accept`), never a bare 404. A redirect helper re-attaches the query parameters Apple drops.
2. Apple's fallback discovery is supported through `dep.Client.AssignAccountDrivenEnrollmentDiscovery` (0026 claim 9) so a deployment without control of the organisation domain still works; the record documents that the URL is public.
3. The first POST's signed plist (`LANGUAGE`, `PRODUCT`, `VERSION`, and the MachineInfo keys when present) is parsed and verified like ADE (0027), so the software update gate and identity selection run for account-driven enrollment too; the 401 challenge is stateless (the body carries no device identity), so retries cost nothing. `enroll/accountdriven.Handler` implements both documented flows behind one `Authenticator` interface: `apple-as-web` (our web-auth page hook, `user-identifier` prefilled, `webauth` OIDC from 0027 or a custom page, 308 to `apple-remotemanagement-user-login://authentication-results?access-token=`) and `apple-oauth2` (we are the OAuth 2 authorization server for a public client: authorization endpoint, token endpoint with authorization code and refresh grants, `login_hint`, `state` echoed, `redirect-url` under the `apple-remotemanagement-user-login` scheme, 308 completion). The 401 header is built by one function whose parameters are validated (`https` only).
4. Tokens are two-tiered, constant-time compared, and clock-injected: a replayed or expired access token is 401 with a fresh `WWW-Authenticate`, never 500; the access token handed to the device is opaque, single-use for the profile fetch, and short-lived; the enrollment profile then carries a separate long-lived enrollment token (in `ServerURL` as a query parameter and stored on the enrollment) that authorises `Authenticate` and `TokenUpdate`, so a retried check-in never fails because the challenge was consumed. Refresh tokens rotate.
5. The BYOD and ADDE profiles come from the 0009 builder with the account-driven keys enforced: `EnrollmentMode` matches the discovery `Version` (`BYOD` or `ADDE`), `AssignedManagedAppleID` is always set (from the authenticated identity, never empty on renewal), `ServerCapabilities`, `AccessRights`, and scope stay the builder's per-mode responsibility, and `EnrollmentMode`/`AssignedManagedAppleID` are immutable across profile updates. macOS account-driven device enrollment is supported, not rejected.
6. Identity: user enrollments are keyed by `EnrollmentID` as its own field (`mdm.EnrollmentID` already carries the channel); `UDID` and `SERIAL` absent by design; the managed Apple ID is stored on the enrollment from the authenticated identity; per-account-driven defaults are a hook, not a team column.
7. The simulator gains `Device.AccountDrivenEnroll(ctx, userIdentifier)` running discovery, the first POST, the 401 parse, either authentication flow against the fake authenticator, and the second POST; it asserts `EnrollmentMode` and `AssignedManagedAppleID` in the received profile.

## Verified by

1. `discovery.TestHandler/{GoldenBody,ModelFamilyTable,Head,MethodNotAllowed,RejectWellKnownFailedJSON,RejectWellKnownFailedPlist,RedirectKeepsQuery,HTTPSBaseURLOnly}` (the exact-parse case would fail on Apple-JSON-discovery-server).
2. `dep.TestEndpoints/AccountDrivenDiscovery{Assign,Fetch,Remove}` (0026).
3. `accountdriven.TestFirstPost/{BodyParsed,ParserPolicyRelayed}`, `accountdriven.TestFlow/AppleAsWeb/{FirstPost401,HeaderParams,WebAuthPrefill,HandBack308,SecondPostProfile}`, `accountdriven.TestFlow/AppleOAuth2/{Header,AuthorizationCode,TokenEndpoint,LoginHint,StateEchoed,Refresh,BadCodeRejected}`, `accountdriven.TestHeader/HTTPSOnly`.
4. `accountdriven.TestTokens/{AccessTokenSingleUse,AccessTokenExpires,EnrollmentTokenAuthorisesCheckin,RetriedCheckinSucceeds}` (the retry case would fail on Fleet), `accountdriven.TestTokens/RefreshRotates`.
5. `accountdriven.TestProfile/{BYOD,ADDE,ModeMatchesVersion,ManagedAppleIDRequired,ImmutableOnUpdate,MacADDESupported}` (macOS would fail on Fleet; empty ID on renewal would fail on Fleet).
6. `storagetest.RunEnrollmentSuite/UserEnrollmentByEnrollmentID`, `service.TestAuthenticate/UserEnrollmentHasNoUDID`.
7. `simulator.TestAccountDrivenEnroll/*`, `e2e.TestE2E_ServiceDiscovery` (E2E-012: Mac routed to `mdm-adde`, iPhone to `mdm-byod`, each enrols through its flow), `e2e.TestE2E_AccountDrivenOAuth2` (E2E-019).

## Rejected alternatives

- Static JSON files (Apple-JSON-discovery-server): no per-user routing and no rejection path.
- One flow only (Fleet's SAML page): Apple documents two; the OAuth 2 flow needs no web page at all for IdPs that speak OAuth 2.
- Consuming the challenge on the profile fetch and reusing it for check-in (Fleet): a second token tier removes the retry failure.
- Overloading UUID and serial with `EnrollmentID` (Fleet): the enrollment id is already a first-class key here.
