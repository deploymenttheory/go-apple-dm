# Security and Apple device-management conformance audit

Date: 11 September 2026. Scope: the Go library and reference server, including shared storage contracts and protocol adapters. Reviewed baseline: `bbb779a29d99adddf36c95de8db698afa125ea26`; remediation is in the accompanying change set. Apple's device-management schema remains pinned to `67045e2fa06f528b196c01edee6a8bf88b844beb`. No schema or dependency upgrade is part of this change.

Seven findings were corrected: three security defects and four protocol/conformance defects. The most consequential security changes authenticate SCEP responses and replace single-DES message encryption. The macOS correction provides usable declarative ACME credentials with Apple's required flags, using authorization from an existing enrollment. Initial ACME enrollment continues to require attestation by default.

Severity is an engineering assessment of impact and prerequisites, not a CVSS score. Conformance severity reflects operational impact; a malformed payload is not automatically an exploitable security vulnerability.

| ID | Finding | Severity | Status |
| --- | --- | --- | --- |
| SEC-01 | SCEP client accepted unauthenticated certificate responses | High, with control of the response or unauthenticated discovery | Fixed |
| SEC-02 | SCEP message generation inherited single-DES encryption | Medium, conditional on access to encrypted SCEP messages | Fixed |
| SEC-03 | Deleted declarations could remain available through expanded snapshots | Medium, with declaration expansion enabled | Fixed |
| CONF-01 | Bootstrap-token removal was rejected and support was not advertised | Medium | Fixed |
| CONF-02 | Declarative ACME credentials omitted Subject and used invalid Mac flags | Medium | Fixed |
| CONF-03 | Mac MDM UDID was treated as the attested ProvisioningUDID | Medium | Fixed |
| CONF-04 | OTA bootstrap profiles used the configuration-profile container shape | Low | Fixed |

## Method and boundaries

The review combined source inspection, independent wire decoding, adversarial regression tests, race-enabled tests, storage integration tests, schema verification, dependency analysis and static security scanning. Areas examined included SCEP/ACME issuance and renewal, attestation and identity binding, enrollment admission and authentication, profile serialization, DDM snapshot lifecycle, bootstrap-token storage, revocation, request bounds and transport behavior. Existing authentication, replay, storage and process tests were rerun as part of validation.

SCEP response substitution and retrieval of deleted expanded declarations were reproduced before the fixes. Conformance findings were checked against Apple's published schema or protocol documentation, rather than inferred solely from a round trip through this library's own encoder and decoder. The SCEP algorithm test inspects the CMS algorithm identifiers on actual client requests and server responses.

This is a source and automated-test assessment. No physical Apple device, production deployment, external Apple service or customer enrollment was exercised. The schema pin is a reproducible conformance baseline, not a claim of coverage of every subsequently published Apple requirement. Clean scanner results do not establish absence of vulnerabilities.

## Findings and remediation

### SEC-01: SCEP client response trust and transaction binding

**Evidence and impact.** The client previously accepted the certificate carried by a CertRep without authenticating its signer against the intended CA/RA, matching the transaction and nonce, verifying the requested public key, or validating the issued chain. The regression server constructed a rogue response using only the request's public signer certificate. Before remediation, the client returned an attacker-issued certificate for a different key despite an explicitly supplied trusted recipient. Automatic CA discovery over HTTP also allowed a substituted recipient to receive the encrypted enrollment challenge. A network attacker needs access to an unprotected exchange, compromised endpoint, or otherwise untrusted discovery; correctly authenticated HTTPS prevents an ordinary network intermediary from making these substitutions.

**Fix.** [Client enrollment](../../pki/scep/client.go) now requires verified HTTPS for automatic discovery, or an independently trusted CA/RA bundle supplied by the caller. It validates bundle certificates, restricts CertRep signers to that bundle, checks message type/status, transaction ID and recipient nonce, selects the certificate matching the CSR key, and validates its chain and validity. It decodes the complete certificate bundle instead of assuming a nonempty first entry. CA-selected subject names remain supported.

The client also refuses redirects, bounds oversized responses explicitly, and supplies a 30-second timeout when the supplied HTTP client has none. These are related transport hardening changes. Public `GetCACert` remains a fetch operation; fetching a bundle alone does not authenticate it.

**Verification.** [Client security tests](../../pki/scep/client_security_test.go) cover rogue responses, wrong transaction/nonce/key/issuer, expired certificates, reflected requests, trusted RA responses, verified versus unverified discovery, and redirects. These checks implement the trust and transaction requirements described in [RFC 8894](https://www.rfc-editor.org/rfc/rfc8894).

### SEC-02: Weak SCEP message-generation defaults

**Evidence and impact.** The SCEP dependency delegates encryption and signing to PKCS#7 defaults: single DES and SHA-1. Single DES offers only a 56-bit key space, weakening confidentiality of the CSR and its challenge password if an adversary obtains the encrypted message. The review did not demonstrate challenge recovery or unauthorized issuance. TLS and the reference server's expiring, CSR-bound issuance grants reduce exposure but do not make this algorithm choice conformant. RFC 8894 requires AES-128-CBC and SHA-256 support and prohibits single DES; SHA-1 is a permitted legacy option, not the same defect. See [RFC 8894 §2.9](https://www.rfc-editor.org/rfc/rfc8894#section-2.9).

**Fix.** [Per-message CMS construction](../../internal/scepwire/wire.go) explicitly uses AES-128-CBC, a fresh content key and IV, and SHA-256 signatures. It retains the protocol's RSA key transport and requires RSA recipients of at least 2048 bits. No process-global dependency setting is changed. Client and server reject non-AES envelopes, and the server rejects unexpected SCEP message types before attempting CSR decryption. Capability advertisement matches the AES policy; legacy SHA-1 verification remains supported.

**Verification.** [Wire-level tests](../../pki/scep/crypto_security_test.go) independently inspect the emitted digest and encryption identifiers, complete an enrollment, and reject an independently generated single-DES request and a CertRep submitted as a request.

### SEC-03: Stale expanded declarations survived deletion

**Evidence and impact.** With a custom declaration Expander configured, an enrollment snapshot contains expanded declaration bytes, potentially including per-device private values. The declaration endpoint returned those cached bytes before checking whether the underlying declaration still existed. An enrolled device could retrieve a deleted declaration until its snapshot was refreshed. This does not expose another enrollment's data, and the reference server's default configuration without an Expander does not trigger this path.

**Fix.** [Declaration serving](../../mdmprotocol/ddm/serve.go) verifies the backing declaration version before returning an expansion. Deletion now produces the normal not-found response. Updates still serve the version already advertised in the device manifest. Generated status subscriptions have an explicit synthetic marker, and legacy subscription snapshots are regenerated from current state instead of returning a deleted administrator override.

**Verification.** The [direct regression](../../mdmprotocol/ddm/deletion_security_test.go) and [shared backend suite](../../storage/ddm/ddmtest/deletion.go) cover deletion, preservation of advertised versions on update, persisted snapshots after engine recreation, deleted subscription overrides, and legacy synthetic snapshots.

### CONF-01: Bootstrap-token removal and capability advertisement

**Evidence and impact.** Empty tokens were rejected by storage, retaining previously escrowed data after a valid removal request. Reference enrollment profiles also omitted the bootstrap-token server capability despite implementing the check-ins. Apple's [SetBootstrapToken schema](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/mdm/checkin/setbootstraptoken.yaml) defines missing or zero-length data as removal and requires capability advertisement.

**Fix.** [In-memory storage](../../storage/inmem/inmem.go) deletes the token, and [SQL storage](../../server/sqlstore/sqlcommon/store.go) clears its column. Both accept repeated removal, record the change time, and retain enrollment existence and disabled-state checks. [Reference profiles](../../server/internal/app/enroll.go) advertise `com.apple.mdm.bootstraptoken`.

**Verification.** The [storage contract suite](../../storage/storagetest/suite.go) covers nil/empty clearing and enrollment gates. [Service tests](../../server/service/service_test.go) verify omitted and empty wire values and subsequent GetBootstrapToken responses.

### CONF-02: macOS declarative ACME credentials

The original assessment used the pinned YAML's blanket Mac restriction. Apple's
current [ACME credential documentation](https://developer.apple.com/documentation/devicemanagement/acmecredential)
permits hardware-bound keys on Apple silicon and T2; Apple silicon also supports
attestation. The handler uses hardware-aware selection and a short-lived grant
bound to the existing enrolled identity and requested assurance. Required Subject,
platform/version validation, current-identity checks, revocation, expiry and
single-order claiming remain enforced. The [hardening record](apple-enterprise-hardening-2026-09-11.md)
contains the source comparison, implementation and validation details.

### CONF-03: Mac MDM and attestation identifiers were conflated

**Evidence and impact.** The reference server placed the MDM UDID into the binding checked against the attestation UDID. On macOS, Apple's attestation OID `1.2.840.113635.100.8.9.2` represents ProvisioningUDID, a distinct identifier. Valid Mac attestation could therefore be rejected. This is an availability/conformance defect, not a demonstrated attestation bypass. See Apple's [DeviceInformation schema](https://github.com/apple/device-management/blob/67045e2fa06f528b196c01edee6a8bf88b844beb/mdm/commands/information.device.yaml).

**Fix.** [ACME bindings](../../pki/acme/acme.go) preserve MDM identity separately in `MDMUDID`; the existing `UDID` field retains its attestation meaning. Mac enrollment and replacement use the serial number as the available hardware binding. Admission and issuance provenance retain the MDM identifier. Unknown products do not cause an MDM identifier to be treated as hardware proof.

**Verification.** Binding tests cover Mac, known non-Mac and unknown products, serialization compatibility, and missing Mac serial numbers. The Mac enrollment fixture now deliberately uses different MDM and provisioning identifiers.

### CONF-04: OTA Profile Service root shape

**Evidence and impact.** The OTA builder wrapped a Profile Service payload inside a `Configuration` profile and array. Apple's bootstrap format instead uses the outer type `Profile Service` and a dictionary for PayloadContent. This can prevent OTA bootstrap. See [Apple's OTA guide, Listing 2-5](https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/profile-service/profile-service.html).

**Fix.** A [typed Profile Service representation](../../mdmprotocol/profile/service.go) supports the correct root shape and keeps it mutually exclusive with configuration payloads. The [OTA builder](../../mdmprotocol/enroll/ota.go) uses it. Parsing rejects malformed container types; validation requires an HTTPS endpoint without embedded credentials or a fragment and valid device-attribute entries.

**Verification.** [Independent plist and signed-profile tests](../../mdmprotocol/profile/service_test.go) inspect the outer dictionary and reject malformed content. Existing OTA tests now assert the actual Apple wire structure.

## Verification record

The following checks were run during remediation. The final affected-package and fault-injection reruns include the last error-contract correction.

| Check | Result |
| --- | --- |
| Targeted SCEP, ACME, Mac credential, profile and lifecycle regressions | Passed |
| Full library and server unit suites with race detector | Completed; the sole failure was the initial-enrollment rejection contract, corrected and verified by the affected-package and fault-injection reruns |
| PostgreSQL 17, MySQL 8.4 and SQLite integration suites with race detector | Passed |
| End-to-end, in-process acceptance and built-process acceptance suites with race detector | Passed, including split topology |
| `make verify` generated-schema and exported API gate | Passed |
| `govulncheck ./...`, separately with `GOWORK=off` in both modules | Passed: zero reachable vulnerabilities and zero affected imported packages |
| Standalone gosec scans of both modules | Passed: no findings or package-loading errors; 299 library files and 161 server files |
| golangci-lint 2.13.2, fixes disabled, changes compared with HEAD | Passed in both modules |
| All 11 existing fuzz targets, 20 seconds per target | Passed |

The dependency scan also reports module-only advisory [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) for `golang.org/x/crypto/openpgp`. That package is not imported by this project. It is recorded as dependency inventory information, not as a reachable project vulnerability. Earlier baseline unit, storage, E2E, in-process acceptance, fuzz and schema checks also passed; the reproduced semantic defects illustrate the limits of those existing checks.

Detailed command logs for this local run use `/tmp/dm-remediation-*-final.log`, `/tmp/dm-remediation-gosec-*-final.json`, `/tmp/dm-security-vuln-{library,server}.log` and `/tmp/dm-security-verify.log`. The full unit invocation was `go test -race -count=1 ./... ./server/...`; 89 packages passed and the bench package reported the rejection-contract failure. The corrected affected packages passed in `/tmp/dm-remediation-targeted-final.log`, and that bench scenario passed in `/tmp/dm-remediation-bench-final.log`. The successful acceptance reruns are `/tmp/dm-remediation-acceptance-final.log` and `/tmp/dm-security-process-final.log`. `make test-acceptance` uses a race-enabled test harness and normally built server binaries. These logs are temporary local evidence, not repository dependencies.

## Compatibility and remaining validation

- HTTP SCEP clients must now supply trusted recipients explicitly. RA-only pins also need issuer roots. Automatic discovery with TLS verification disabled, redirect-based SCEP endpoints, non-AES envelopes and RSA recipients below 2048 bits are rejected. SCEP pending enrollment/polling remains unsupported by this client; it returns an error rather than an identity.
- The bootstrap-token storage contract now treats nil and empty values as removal. Third-party storage implementations should run the shared contract suite and implement the same semantics. Existing issued MDM profiles acquire the advertised capability when regenerated and installed through the deployment's normal profile lifecycle.
- The new ACME binding field and credential grants require no SQL schema migration. Upgrade every server replica together so the same identity and authorization rules apply. Previously minted Mac identifiers retain their original binding; reissue affected profiles/codes to obtain corrected bindings. A credential document must be fetched again after its five-minute code expires.
- A Mac secondary credential obtained through the scoped exception has no hardware-attestation assurance. Its code remains a bearer capability during its short lifetime. Protect transport and the existing device identity; deployments requiring fresh hardware properties for every issued key must retain that stronger policy and require an Apple silicon credential that requests and proves attestation.
- Bootstrap-token clearing removes the active stored value. Database backup retention, snapshots and physical data erasure remain deployment responsibilities. Declaration deletion prevents subsequent serving; it cannot withdraw bytes a device already obtained.
- Validate the changed flows on supported physical Apple hardware before release: Mac attested ADE enrollment with distinct provisioning identity, declarative secondary certificate issuance and renewal, bootstrap-token escrow/removal, and OTA profile installation. Automated tests use synthetic attestations and protocol clients and do not prove Apple's OS accepts every interaction.

Operational details are also updated in [enrollment security operations](../operations/enrollment-security.md). No production state was modified during this audit.
