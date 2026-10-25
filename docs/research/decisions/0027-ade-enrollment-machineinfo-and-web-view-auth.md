# 0027: Automated Device Enrollment: MachineInfo, the enrollment endpoint, and web view authentication

## Context

Automated Device Enrollment can submit signed `MachineInfo` or use a web view for user authentication before obtaining an enrollment profile.

## Decision

`ade` accepts bounded CMS data from documented request carriers, verifies one signer against configured Apple device anchors and validates presence rules. The default Apple device-chain path ignores certificate validity windows; strict validity and audit options are explicit. A store retains parsed data per serial. Optional DEP lookup and profile hooks support enrichment and admission.

The software update gate evaluates caller-owned minimum-OS policy before device
capability. Nil and nonrequired policies are inactive. A required policy rejects
unknown platforms, tvOS and watchOS before comparing versions. On supported
platforms, devices that already meet the target proceed even without update
capability; devices needing an update require both capability and an OS supporting
the enrollment response: iOS/iPadOS 17+, macOS 14+, or visionOS 26+. Apple's
[deployment guide](https://support.apple.com/guide/deployment/install-and-enforce-software-updates-depd30715cbb/1/web/1.0)
and [enrollment update protocol](https://developer.apple.com/documentation/devicemanagement/deploying-software-updates-using-declarative-management)
define those limits. Generated schema availability does not enable a tvOS gate.

`ade.Bound.Parsed` exposes the exact validated request evidence for the browser
handoff. Its pointer and reachable maps, slices and certificates are borrowed
read-only data, aliased by the in-memory store. Callers snapshot the evidence into
expiring, one-use authentication state bound to the original serial and UDID.
Callbacks validate that retained evidence before `Finish`. `Resume` retrieves the
latest record for a serial and cannot bind an original authentication session.

The OIDC relying party uses authorization code flow, S256 PKCE, nonce validation and expiring one-use state bound to the supplied device data. Completion returns through the profile hook.

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

Signed `MachineInfo` is not Managed Device Attestation or proof of organizational ownership. Audit mode permits unverified input. A GDMF lookup failure is logged and enrollment proceeds. The reference composition stores MachineInfo in its existing shared protocol store.
An ADE browser handoff retains the original CMS and validated signer certificate
inside its browser grant, with the same expiration and atomic one-use consumption.
SQL records are sealed by the existing storage keyring. Callback completion checks
the retained signature, serial and UDID; it does not reload a later request's
per-serial record. Missing, corrupt, expired, unverified or mismatched browser
evidence fails closed. The reference minimum-version settings are literal targets;
no dynamic latest-version policy or tvOS gate is composed. Replicas need the same state backend, issuer configuration and admission policy. SAML is not implemented.

## Verification

ADE/CMS tests cover carriers, size limits, attributes, chains, presence rules and error responses.
Memory, SQLite, PostgreSQL and MySQL contracts cover shared evidence, expiry,
corruption, serial/UDID mismatches, replay and rollback. Production route tests
complete browser authentication on a second build and after closing the initiating
build, then perform SCEP issuance and MDM check-in. The same callback composition
rejects invalid evidence without issuance. Literal requirements cover supported
responses, current and disabled devices, unknown products and unavailable updates. OIDC tests cover state replay, expiry, nonce/audience/signature checks and redirects. Simulator and end-to-end scenarios exercise profiles, software-update responses and web authentication.

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
