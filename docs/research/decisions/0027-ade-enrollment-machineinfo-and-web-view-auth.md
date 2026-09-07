# 0027: Automated Device Enrollment: MachineInfo, the enrollment endpoint, and web view authentication

## Context

Automated Device Enrollment can submit signed `MachineInfo` or use a web view for user authentication before obtaining an enrollment profile.

## Decision

`ade` accepts bounded CMS data from documented request carriers, verifies one signer against configured Apple device anchors and validates presence rules. The default Apple device-chain path ignores certificate validity windows; strict validity and audit options are explicit. A store retains parsed data per serial. Optional DEP lookup and profile hooks support enrichment and admission.

The software update gate uses a minimum-OS policy and device capability flags. The OIDC relying party uses authorization code flow, S256 PKCE, nonce validation and expiring one-use state bound to the supplied device data. Completion returns through the profile hook.

## Rationale

Separating cryptographic verification, device enrichment, admission and profile composition lets consumers select their own ownership rules. Bound state connects the browser result with the enrollment request.

## Constraints

Signed `MachineInfo` is not Managed Device Attestation or proof of organizational ownership. Audit mode permits unverified input. A GDMF lookup failure is logged and enrollment proceeds. The reference composition stores MachineInfo and browser handoff state in memory; replicas need affinity or injected shared stores. SAML is not implemented.

## Verification

ADE/CMS tests cover carriers, size limits, attributes, chains, presence rules and error responses. OIDC tests cover state replay, expiry, nonce/audience/signature checks and redirects. Simulator and end-to-end scenarios exercise profiles, software-update responses and web authentication.

## References

- [mdmprotocol/enroll/ade](../../../mdmprotocol/enroll/ade)
- [mdmprotocol/enroll/webauth](../../../mdmprotocol/enroll/webauth)
- [appleplatformservices/gdmf](../../../appleplatformservices/gdmf)
- [server/internal/app/enroll.go](../../../server/internal/app/enroll.go)
- <https://developer.apple.com/documentation/devicemanagement/machineinfo>
- <https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views>
- <https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired>
- <https://developer.apple.com/documentation/devicemanagement/profile>

Reference source identifiers and paths (relative to the named project):

- `fleetdm/fleet@b44343c`, `server/mdm/apple/deviceinfo.go`, `server/service/apple_mdm.go`, `CheckMDMAppleEnrollmentWithMinimumOSVersion`, `server/service/handler.go`, `server/mdm/apple/gdmf/api.go`
- `korylprince/dep-webview-oidc@bc3fa71`, `header/header.go`, `service/http.go`, `store/mem`, `docs/Architecture.md`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/public_views/dep.py`
- `micromdm/micromdm@904493b`, `mdm/enroll/endpoint.go`, `pkg/crypto/apple.go`
