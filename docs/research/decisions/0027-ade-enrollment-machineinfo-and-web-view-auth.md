# 0027: Automated Device Enrollment: MachineInfo, the enrollment endpoint, and web view authentication

Status: proposed
Date: 2026-09-02
Phase: 6

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/machineinfo> (keys, "CMS-signed with the device identity certificate ... should validate against the Apple Root CA")
- Doc: <https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views> (`configuration_web_url`, `x-apple-aspen-deviceinfo` header, `AnchorCerts`, `application/x-apple-aspen-config`, same last-two-labels host rule)
- Doc: <https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired> (403 body JSON or plist with matching `Content-Type`)
- Doc: <https://developer.apple.com/documentation/devicemanagement/profile> (`configuration_web_url`, `anchor_certs`, `await_device_configured`, `skip_setup_items`)
- YAML: `third_party/device-management/other/machineinfo.yaml` (typed `MachineInfo` incl. `MDM_CAN_REQUEST_PSSO_CONFIG`, `MANDATORY_SOFTWARE_UPDATE_REQUIRED`, `PAIRING_TOKEN` as data, `userenrollment` presence rules)
- YAML: `third_party/device-management/mdm/errors/softwareupdate.required.yaml`, `psso.required.yaml`, `unrecognized.device.yaml`
- YAML: `third_party/device-management/other/skipkeys.yaml` (Setup Assistant skip keys per OS)

## References read

- `fleetdm/fleet@b44343c` `server/mdm/apple/deviceinfo.go` (pinned Apple iPhone Device CA verification, authenticated attributes), `server/service/apple_mdm.go` (three MachineInfo lanes, `CheckMDMAppleEnrollmentWithMinimumOSVersion`, SSO web view), `server/service/handler.go` (SSO middleware), `server/mdm/apple/gdmf/api.go`
- `korylprince/dep-webview-oidc@bc3fa71` `header/header.go`, `service/http.go`, `store/mem`, `docs/Architecture.md`
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/public_views/dep.py` (MachineInfo in the session cookie, 403 software update)
- `micromdm/micromdm@904493b` `mdm/enroll/endpoint.go`, `pkg/crypto/apple.go`
- Record 0010 (OTA profile service, the Apple iPhone Device CA pool) and 0009 (enrollment profile builder)

## Known pitfalls found

- dep-webview-oidc `header/header.go:157-163`: signature checked over raw content, ignoring CMS authenticated attributes; hard-coded SHA-1 RSA; `http.go:131-137` reads `error` three times instead of `error_description`/`error_uri`; no PKCE, no `nonce`; `PAIRING_TOKEN` typed as string.
- Fleet `deviceinfo.go:55-98`: verification enforcement is a process-wide atomic flag with an audit mode, so tests disable it globally; `handler.go:1696-1716` the SSO middleware looks up the DB and GDMF twice and can redirect non-Apple clients in a loop; `#7515`; `apple_mdm.go:2871-2882` non-iOS products answered 400 to avoid SSO.
- Zentral `public_views/dep.py:239-262`: the parsed MachineInfo lives in a Django session cookie between the web view GET and later views; the web view is known to lose cookies.
- Zentral `public_views/dep.py:28-50,74-79`: `PAIRING_TOKEN`, `IMEI`, `MEID` dropped; platform inferred by substring on `PRODUCT`; `crypto.py:60` time checks disabled globally; `validators.py:53-60` a web-view realm without `await_device_configured` means the account configuration is silently never applied.
- MicroMDM `mdm/enroll/endpoint.go:76-79`: MachineInfo parsed then discarded; header form unread; decode errors are 500; the pinned CA verification checks one signer against an expired CA.

## What they do

- **Fleet**: MachineInfo from the header, a `deviceinfo` query parameter, or a `application/pkcs7-signature` body; pinned Device CA with a custom path builder that ignores validity; exactly one signer whose certificate must be in the bundle; `messageDigest` and `contentType` checked; software update gate only when `MDM_CAN_REQUEST_SOFTWARE_UPDATE`; 403 `{"code":"com.apple.softwareupdate.required","details":{"OSVersion":...}}`; SAML web view with the enrollment reference appended to `ServerURL`.
- **dep-webview-oidc**: OIDC code flow keyed by a single-use `state` in a TTL store, MachineInfo bound to the state, `Authorizer` hook picks the profile context, generators produce the profile, `application/x-apple-aspen-config` response.
- **Zentral**: min and max OS checks in the web view, `com.apple.softwareupdate.required` 403, session cookie for state.

## What we do better

1. `enroll/ade.MachineInfo` is the generated `schema/other.MachineInfo`; `ParseMachineInfo(r)` accepts the `x-apple-aspen-deviceinfo` header (standard or URL base64, padding tolerated), a `deviceinfo` query parameter, or a `application/pkcs7-signature` body, bounded at 64 KiB, and returns the typed struct with the origin. Presence rules from the YAML apply (`UDID`, `SERIAL` forbidden under user enrollment; `OS_VERSION` required from iOS 17 and macOS 14).
2. Verification uses the `cms` package with a new `VerifyAttached` that honours authenticated attributes when present (checking `messageDigest` and `contentType`) and content otherwise, dispatches on the digest and signature OIDs (SHA-1 and SHA-256 RSA accepted for this chain only), requires exactly one signer present in the bundle, and chains to a configurable pool that defaults to the Apple iPhone Device CA with the Apple Inc. Root as a second anchor, ignoring validity windows as Apple's chain requires. Verification is per handler, never a process-wide switch; an audit mode is a per-handler option that logs and continues.
3. `ade.Handler` serves the token-based ADE `POST` and the web view flow from one place: MachineInfo is verified, persisted per serial through `dep.DeviceStore` (joined to the DEP record when present), then a `ProfileHook(ctx, MachineInfo, Identity) (*enroll.Profile, error)` chooses and personalises the enrollment profile (SCEP challenge, `AssignedManagedAppleID`, `ServerURL` reference) which is CMS-signed and served as `application/x-apple-aspen-config`. Unknown signer is 403 `com.apple.unrecognized.device` when configured, malformed CMS is 400, decode errors are never 500.
4. Platform derives from a `PRODUCT` prefix table with an explicit unknown value, never substring matching. The software update gate is one function used by both lanes: skipped when `MDM_CAN_REQUEST_SOFTWARE_UPDATE` is false, decided by a `MinimumOS` policy hook given `PRODUCT`, `SOFTWARE_UPDATE_DEVICE_ID`, and `OS_VERSION`, answered with 403 and the `softwareupdate.required` body as JSON or plist chosen by the request's `Accept`, validated against the YAML; `MANDATORY_SOFTWARE_UPDATE_REQUIRED` and `MDM_CAN_REQUEST_PSSO_CONFIG` are surfaced to the hook so the `psso.required` response can be produced. GDMF lookup is behind an interface with a fake; a lookup failure lets enrollment proceed and is logged.
5. `enroll/webauth` is our own OIDC relying party for `configuration_web_url`: authorization code flow with PKCE (S256) and a `nonce`, discovery document and JWKS fetched with caching (ES256 and RS256), the `state` is 128-bit random, single-use, TTL-bound (default 5 minutes, hard maximum 8) and bound to the MachineInfo `SERIAL` and `UDID` in a `StateStore` (in-memory and SQL); no cookies are used; `error`, `error_description`, and `error_uri` are parsed; `access_denied` is 403, other IdP errors 502; every redirect target must be `https`. An `Authorizer(ctx, MachineInfo, Claims) (Decision, error)` hook decides and feeds the `ProfileHook`. SAML is out of scope (rejected below).
6. `webauth/webauthtest` is a fake OIDC provider (discovery, JWKS, `/authorize` recording `state`, `nonce`, `code_challenge`, `/token` checking `code_verifier`, scripted bad nonce, expired token, wrong audience, `access_denied`), and `ade/adetest` builds CMS-signed MachineInfo blobs from a test chain (leaf, test Device CA, test root; signed-attribute and content-only variants) so the simulator can enrol through ADE.
7. The simulator gains `Device.ADEEnroll(ctx, url)`: it posts MachineInfo, follows the 403 software update response as an error type, follows the web view redirects against the fake IdP, and installs the returned profile; `TokenUpdate` carries `AwaitingConfiguration` so `DeviceConfigured` can be tested.

## Verified by

1. `ade.TestParseMachineInfo/{Header,HeaderURLBase64,QueryParam,Body,TooLarge,PresenceRules,PairingTokenBytes}`.
2. `cms.TestVerifyAttached/{SignedAttributes,ContentOnly,TamperedContent,TwoSigners,SignerNotInBundle,BadMessageDigest,WrongContentType,SHA256}` (signed-attribute vector would fail on dep-webview-oidc), `ade.TestVerify/ChainIgnoresValidity`, `/UnknownRootRejected`, `/AuditModePerHandler` (would fail on Fleet's global flag).
3. `ade.TestHandler/{ProfileHookPersonalises,PersistedPerSerial,JoinedToDEPRecord,UnknownSignerUnrecognizedDevice,MalformedIs400,SignedProfileContentType}`, `ade.TestHandler/NoProfileWithoutMachineInfo`.
4. `ade.TestPlatformFromProduct/Table`, `ade.TestSoftwareUpdate/{Skipped,RequiredJSON,RequiredPlist,SchemaConformance,GDMFFailureProceeds,PSSORequired}`.
5. `webauth.TestFlow/{PKCEAndNonce,StateSingleUse,StateExpired,StateBoundToSerial,WrongNonce,AccessDeniedIs403,IdPErrorIs502,HTTPRedirectRejected,ErrorDescriptionParsed}` (the error parsing case would fail on dep-webview-oidc).
6. `webauthtest.TestProvider/*`, `adetest.TestSign/*`.
7. `simulator.TestADEEnroll/{Profile,SoftwareUpdateRequired,WebView}`, `e2e.TestE2E_DEPAssign` (E2E-011: fake DEP assigns, the device enrols through ADE with MachineInfo), `e2e.TestE2E_ADEWebViewAuth` (E2E-018: web view through the fake IdP, profile personalised with the IdP claim).

## Rejected alternatives

- Trusting the MachineInfo as attestation: it identifies the device to Apple's chain only; policy decisions stay in hooks and ACME attestation (phase 7) is the attestation path.
- Session cookies for web view state (Zentral): the web view loses cookies; `state` carries everything.
- SAML in the web view (Fleet): a full SAML implementation is out of scope; the `Authorizer` hook lets an integrator plug SAML behind their own endpoint, and OIDC covers the documented `ASWebAuthenticationSession` flows.
- Process-wide verification switch (Fleet): a per-handler option keeps tests honest.
