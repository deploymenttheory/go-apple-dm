# Apple enterprise hardening — source and validation record

This implementation treats Apple MDM/DDM interoperability as a requirement.
Apple protocol requirements and project security policy are distinguished below.
The Apple Developer documentation was checked on 11 September 2026, including
the DocC property descriptions and hardware tables. No generated Apple source
or pinned submodule content is edited.

## Apple payload requirements

| Context | Implemented behavior | Apple support |
| --- | --- | --- |
| SCEP | RSA-2048 default retained. Nonextractable keys by default on supported targets; Mac application-wide access defaults to false. No changes to SCEP envelope algorithms, advertised capabilities, challenge omission during authorized renewal, or PENDING behavior. | [SCEP payload content](https://developer.apple.com/documentation/devicemanagement/scep/payloadcontent-data.dictionary) defines RSA, 1024/2048/4096-bit keys and extractability. Mac extractability starts at 10.13.4; application access at 10.10. |
| ACME profile, macOS 13.1–13.x | Software key; explicit `Attest=false`. Known incompatible hardware/attestation combinations are rejected. | [ACMECertificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate) requires false hardware/attestation flags on older macOS. |
| ACME profile/credential, macOS 14+ Apple silicon | EC-256/384 hardware binding and attestation can be requested. | [ACMECertificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate) and [ACMECredential](https://developer.apple.com/documentation/devicemanagement/acmecredential), including their hardware tables. |
| ACME profile/credential, macOS 14+ T2 | Hardware-bound EC key allowed; `Attest=false`. Other Intel Macs use software keys. | The same Apple property descriptions distinguish key-generation support from attestation; the profile table lists T2 as ignoring attestation, and the credential table lists Intel. |
| Mac ACME profile key export | Default `KeyIsExtractable=false` and `AllowAllAppsAccess=false` on macOS 13.1+. Hardware-bound keys remain inherently nonexportable. | [ACMECertificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate) defines false as nonextractable and identifies these options as Mac-only. They are not added to the DDM credential document. |
| Mac PKCS#12 | Nonextractable by default on macOS 10.15+; application-wide access defaults to false on 10.10+. Explicit caller overrides remain available. | [CertificatePKCS12](https://developer.apple.com/documentation/devicemanagement/certificatepkcs12) and the pinned payload's per-key availability. |

The helper API is in [acme_target.go](../../mdmprotocol/enroll/acme_target.go).
The [reference selection](../../server/internal/app/acmetarget.go) uses known OS
and hardware context. Authenticated, tracked
[DeviceInformation](https://developer.apple.com/documentation/devicemanagement/deviceinformationresponse/queryresponses-data.dictionary)
reports can establish `IsAppleSilicon`; a false value does not prove T2.
Inventory callbacks can supply T2 capability. Unknown hardware is never inferred
from a model-name prefix. The [operations guide](../operations/enrollment-security.md)
describes unknown-target behavior and configuration requirements.

### Live documentation and pinned schema differences

The pinned schema is Apple commit
[`67045e2fa06f528b196c01edee6a8bf88b844beb`](https://github.com/apple/device-management/tree/67045e2fa06f528b196c01edee6a8bf88b844beb).
Its [ACME profile YAML](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/mdm/profiles/com.apple.security.acme.yaml)
describes extractability with the opposite polarity to the current Developer
page. Its [DDM ACME YAML](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/declarative/declarations/assets/credentials/acme.yaml)
retains the older blanket false Mac hardware/attestation instructions. The
implementation follows Apple's current Developer documentation. Generated
comments preserve the upstream YAML verbatim; schema validation alone cannot
resolve these prose differences. Handwritten policy and regression tests enforce
the current documented semantics.

## Issuance and credential protection

| Change | Why it is valid for Apple enrollment | Implementation and checks |
| --- | --- | --- |
| Reusable state-backed SCEP grants and certificate receipts | Apple's [OTA security overview](https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/OTASecurity/OTASecurity.html) permits one-time enrollment challenges. Binding a valid challenge to one CSR and retaining its issued DER preserves that authorization while allowing response/registration recovery. Exact-CSR retry is project policy; Apple's SCEP retry payload keys describe PENDING polling. | [Grants and CertificateIssuer](../../pki/scep/grants.go), [failure/concurrency tests](../../pki/scep/grants_test.go), [SQL replica contract](../../server/statestore/scep_test.go). Invalid CSR signatures do not reserve grants; authorization is checked again on retries; registration must succeed before returning a certificate. |
| Attestation cannot be downgraded per identifier | Apple's [ACME profile](https://developer.apple.com/documentation/devicemanagement/acmecertificate) uses the client identifier as an anti-replay value and describes Attest as requesting key/hardware evidence. Binding that request to its identifier is server authorization policy and introduces no extra Apple wire requirements. | `Binding.RequireAttestation` and the credential grant reject missing attestation at challenge and finalization even if other identifiers may be unattested. Existing Mac secondary software/T2 issuance retains its scoped enrolled-identity authorization. |
| Profile and credential responses use `Cache-Control: no-store` | Apple's [PKCS#12 payload documentation](https://developer.apple.com/documentation/devicemanagement/certificatepkcs12) warns that profile content is obfuscated rather than encrypted; SCEP profiles and ACME credential documents also carry authorization material. Preventing compliant caches from storing that material is project HTTP policy, with semantics from [RFC 9111 §5.2.2.5](https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.5). Apple does not mandate this header. | ADE, OTA, account-driven, administrative profile/replacement and ACME credential paths set the header before errors; credential authentication middleware is wrapped as well. |
| HTTPS-only Apple service endpoints | Apple documents HTTPS for [AxM OAuth](https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api), [DEP authentication](https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment), and [APNs HTTP/2 over TLS](https://developer.apple.com/documentation/usernotifications/establishing-a-connection-to-apns). Rejecting plaintext overrides protects OAuth/session/APNs credentials without altering SCEP device transport rules. | AxM/DEP validate at construction; APNs validates before loading credentials or building its HTTP client. Errors omit endpoint values. Local fixtures use verified HTTPS and explicit private roots. |

The existing certificate association, admission, revocation and profile
replacement controls remain required. Apple recommends unique device identities
and replacement of expiring enrollment profiles in
[Managing certificates](https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices).
Apple's [automatic certificate renewal guidance](https://support.apple.com/en-gb/101986)
excludes SCEP and MDM/OTA-installed profiles from that particular auto-renewal
mechanism. This change does not assume automatic renewal will repair an expired
enrollment. Replacement CSR/certificate retries preserve the active attempt and
reject changes in CSR or candidate certificate.

## Build and dependency controls

All external workflow actions are pinned to full commit SHAs. The remaining
`create-github-app-token@v3` and `download-artifact@v6` references were resolved
through the actions repositories' Git tag refs and pinned without a major-version
change. [Workflow security tests](../../internal/layout/workflow_security_test.go)
check both ordinary and reusable-action references; `make verify` runs the gate.
Local actions are allowed and container actions require SHA-256 digests.

GitHub's [supply-chain security guidance](https://github.blog/security/supply-chain-security/securing-the-open-source-supply-chain-across-github/)
supports immutable action references. Dependabot covers both `/` and `/server`
Go modules using the documented
[multiple-directory configuration](https://github.blog/changelog/2024-06-25-simplified-dependabot-yml-configuration-with-multi-directory-key-directories-and-wildcard-glob-support/).
These are repository security controls, not Apple device protocol requirements.

## Validation boundary

Validation includes the Apple hardware/OS matrix, key export defaults, source
comparison, exact-CSR retries and failures, cross-replica SQL issuance, endpoint
credential protection, replacement recovery, schema regeneration, workflow
validation, both-module race tests and simulator scenarios.

Completed checks on 11 September 2026:

| Check | Result |
| --- | --- |
| `make test` | Both Go modules passed with race detection and shuffled tests. |
| `make test-storage` with PostgreSQL and MySQL configured | SQLite, PostgreSQL and MySQL contracts passed, including concurrent SCEP issuance through separate store instances and replacement retries. |
| `make test-e2e` | SQLite simulator scenarios and in-process acceptance passed. |
| `make test-acceptance` | Separate server-process acceptance passed with verified TLS provider fixtures. |
| Focused security regressions | Passed with race detection: failed registration followed by exact-certificate retry, authorized challenge-free SCEP renewal, invalid trust/renewal configuration, invalid grant bindings, grant expiry/persistence failures and unavailable private trust. |
| `make verify` | Schema regeneration and immutable workflow-reference gate passed. Generated Apple files and pinned schema/submodule content are unchanged. |
| Both-module `golangci-lint run --fix=false`; `actionlint` | Passed with zero lint issues. |
| `make fuzz-smoke` | All 11 fuzz targets passed, 20 seconds per target. |
| `make coverage` | Passed: **95.63% overall**, with every non-exempt package at least 95%. The merged report uses current-source unit, SQL, E2E and focused security runs. |
| `govulncheck ./...` in each module | Zero reachable vulnerabilities and zero affected imported packages. |

The dependency scan reports module-only advisory
[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) for
`golang.org/x/crypto/openpgp`. The project does not import that package; this is
dependency inventory information, not a reachable project vulnerability.

Temporary local execution logs are `/tmp/apple-hardening-unit-final.log`,
`/tmp/apple-hardening-storage.log`, `/tmp/apple-hardening-e2e.log`,
`/tmp/apple-hardening-process.log`, `/tmp/apple-hardening-verify.log`,
`/tmp/apple-hardening-lint.log`, `/tmp/apple-hardening-fuzz.log` and
`/tmp/apple-hardening-coverage.log`. The merged coverage artifact is
`cover/merged.html`; these local artifacts are not repository dependencies.

Physical-device installation and Secure Enclave behavior require the existing
[Mac enrollment runbook](../operations/mac-enrollment-testing.md) on macOS
13.1/13.x, macOS 14+ Apple silicon and T2 hardware, plus supported iPhone/iPad
targets. No physical-device run or Apple certification is claimed by this work.
