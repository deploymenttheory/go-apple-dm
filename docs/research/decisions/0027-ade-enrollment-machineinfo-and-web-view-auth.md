# 0027: Automated Device Enrollment: MachineInfo, the enrollment endpoint, and web view authentication

## Context

Automated Device Enrollment can submit signed `MachineInfo` or use a web view for user authentication before obtaining an enrollment profile.

## Decision

`ade` accepts bounded CMS data from documented request carriers, verifies one signer against configured Apple device anchors and validates presence rules. The default Apple device-chain path ignores certificate validity windows; strict validity and audit options are explicit. A store retains parsed data per serial. Optional DEP lookup and profile hooks support enrichment and admission.

The software update gate uses a minimum-OS policy and device capability flags. The OIDC relying party uses authorization code flow, S256 PKCE, nonce validation and expiring one-use state bound to the supplied device data. Completion returns through the profile hook.

The reference server publishes unauthenticated HTTPS `/MDMServiceConfig` with
the configured DEP enrollment URL and a URL serving base64-DER HTTPS trust
anchors. `DM_ENROLL_TLS_ANCHOR_FILE` selects those anchors independently of the
Apple device-signature anchors and client-identity issuer. Private HTTPS also
advertises a trust profile containing only root-certificate payloads. Publicly
trusted HTTPS publishes an empty anchor array and omits the trust-profile URL.
This implements Apple's service-configuration contract alongside the separate
[account-driven discovery flow](0028-account-driven-enrollment-and-service-discovery.md).

`enroll.MAIDToken` signs the GetToken reply with the ADE server's registered RSA
certificate/key and server UUID. It uses RS256, explicit issuance time, a fresh
UUID `jti`, and `service_type=com.apple.maid`; the caller authenticates the request
and advertises `com.apple.mdm.token`. Registration and Apple acceptance cannot be
established by local signature verification.

`ade.PasswordHash` returns generated password-hash plist data for
AccountConfiguration or SetAutoAdminPassword. It uses PBKDF2-HMAC-SHA512, 32 random
salt bytes and a positive caller-selected iteration count. The 128-byte derived
value follows Apple's example, not a normative length requirement. Password
changes require the GUID of the administrator created during ADE. Callers choose
iteration policy and protect credentials; the helper does not create accounts.

## Rationale

Separating cryptographic verification, device enrichment, admission and profile composition lets consumers select their own ownership rules. Bound state connects the browser result with the enrollment request.

## Constraints

Signed `MachineInfo` is not Managed Device Attestation or proof of organizational ownership. Audit mode permits unverified input. A GDMF lookup failure is logged and enrollment proceeds. The reference composition retains MachineInfo in memory but stores browser handoff state in its shared protocol store. Replicas need the same state backend, issuer configuration and admission policy. SAML is not implemented.

## Verification

ADE/CMS tests cover carriers, size limits, attributes, chains, presence rules and error responses. OIDC tests cover state replay, expiry, nonce/audience/signature checks and redirects. Simulator and end-to-end scenarios exercise profiles, software-update responses and web authentication.

Service-configuration tests cover public and private HTTPS, trust separation,
invalid certificates and URLs, and agreement between anchors and the trust
profile. These checks and manual enrollment do not prove ADE Setup Assistant
activation; that requires an assigned device and Apple Business Manager or
Apple School Manager.

## References

- [GetToken contract](https://developer.apple.com/documentation/devicemanagement/get-token)
- [AccountConfiguration example](https://developer.apple.com/documentation/devicemanagement/account-configuration-command)
- [Password hash fields](https://developer.apple.com/documentation/devicemanagement/passwordhash/salted-sha512-pbkdf2-data.dictionary)
- [SetAutoAdminPassword fields](https://developer.apple.com/documentation/devicemanagement/setautoadminpasswordcommand/command-data.dictionary)
- [mdmprotocol/enroll/ade](../../../devicemanagement/mdmprotocol/enroll/ade)
- [mdmprotocol/enroll/webauth](../../../devicemanagement/mdmprotocol/enroll/webauth)
- [appleplatformservices/gdmf](../../../devicemanagement/appleplatformservices/gdmf)
- [server/internal/app/enroll.go](../../../server/internal/app/enroll.go)
- <https://developer.apple.com/documentation/devicemanagement/machineinfo>
- <https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views>
- <https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired>
- <https://developer.apple.com/documentation/devicemanagement/profile>
- <https://developer.apple.com/documentation/devicemanagement/providing-information-about-your-device-management-service>

Reference source identifiers and paths (relative to the named project):

- `fleetdm/fleet@b44343c`, `server/mdm/apple/deviceinfo.go`, `server/service/apple_mdm.go`, `CheckMDMAppleEnrollmentWithMinimumOSVersion`, `server/service/handler.go`, `server/mdm/apple/gdmf/api.go`
- `korylprince/dep-webview-oidc@bc3fa71`, `header/header.go`, `service/http.go`, `store/mem`, `docs/Architecture.md`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/public_views/dep.py`
- `micromdm/micromdm@904493b`, `mdm/enroll/endpoint.go`, `pkg/crypto/apple.go`
