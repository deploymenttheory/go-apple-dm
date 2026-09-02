# 0032: Managed Device Attestation: parsing, verification, and policy

Status: proposed
Date: 2026-09-02
Phase: 7

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/acmecertificate>
  (the `Attest` key, and the flow: the device orders with the `ClientIdentifier` as the
  `permanent-identifier`, the server answers with a `device-attest-01` challenge, the device
  replies with a WebAuthn attestation statement, and "in the attestation certificate the value
  of the freshness code OID is the SHA-256 hash of the `token` from the `device-attest-01`
  challenge")
- Doc: <https://developer.apple.com/documentation/devicemanagement/deviceinformationresponse>
  (`QueryResponses.DevicePropertiesAttestation`: the array of DER certificates, leaf first,
  rooted at the Apple Enterprise Attestation Root CA, and the table of custom OIDs; and
  `DeviceAttestationNonce`, capped at 32 bytes, with the seven-day device-side cache)
- Doc: <https://support.apple.com/guide/deployment/managed-device-attestation-dep28afbde6a/web>
- Apple certificate authority: <https://www.apple.com/certificateauthority/private/>
  (Apple_Enterprise_Attestation_Root_CA.pem)
- YAML: `third_party/device-management/mdm/commands/information.device.yaml`
  (`DevicePropertiesAttestation`, `DeviceAttestationNonce`, and the authoritative OID list with
  the OS versions that introduced each)
- YAML: `third_party/device-management/mdm/profiles/com.apple.security.acme.yaml`
  (`Attest` requires `HardwareBound`; the hardware support table; `Attest` is watchOS 10.0 while
  the payload is watchOS 9.0)
- YAML: `third_party/device-management/declarative/declarations/assets/credentials/acme.yaml`
  (the same flow for the DDM credential, and the divergent macOS guidance discussed below)

## References read

- `brandonweeks/nanoca@df2dba6c` `verifiers/apple/apple.go`, `webauthn.go`, `handlers.go`,
  `certutil/certutil.go` (the `AttestationVerifier` seam, the two-key attestation object, the
  freshness comparison against the raw extension value, the RFC 4043 and RFC 4108 otherName
  builders, and re-verification of the stored attestation at finalize)
- `smallstep/certificates@bb481fbf` `acme/challenge.go`, `acme/order.go`,
  `authority/provisioner/acme.go` (`doAppleAttestationFormat`, the identifier comparison, the
  attested-key fingerprint carried on the authorization, and the provisioner's
  `attestationFormats` and `attestationRoots` settings)
- `fleetdm/fleet@e1bbd21c` `server/mdm/acme/internal/service/challenge.go`,
  `internal/types/acme.go`, `server/mdm/acme/testhelpers/helpers.go` (the two-stage CBOR decode
  with `cbor.RawMessage`, the `cbor.Wellformed` pre-check, `badAttestationStatement`, and the
  serial-to-enrollment and DEP-assignment gating)
- `hslatman/ios-acme-simulator@8373a8f9` `main.go` (the device side: the attestation leaf must
  carry the same public key as the CSR, the extension values are raw bytes, and `x5c` is
  `[leaf, intermediate]` with the root left out of band)
- `zentralopensource/zentral@902e596c` `zentral/contrib/mdm/cert_issuer_backends/__init__.py`,
  `crypto.py` (the per-model capability gate for `Attest` and `HardwareBound`, and reading the
  attested serial and UDID off an issued identity certificate)
- Record 0027 (ADE MachineInfo, which established the embedded-Apple-anchor pattern this
  package follows) and record 0008 (the certificate authority abstraction the issued identity
  goes through).

## Known pitfalls found

- `smallstep/certificates` `acme/challenge.go:894-900`: the freshness comparison is guarded by
  `if len(data.Nonce) != 0`, so an attestation leaf carrying no freshness extension skips the
  check entirely and is accepted. The extension is the only thing tying an attestation to a
  particular challenge.
- `smallstep/certificates` `acme/order.go:188-204`: the attested-key comparison is guarded by
  `if fingerprint != ""`, so it is skipped whenever the authorization carries no fingerprint.
- `smallstep/certificates` `acme/challenge.go:1390-1397`: every extension value is read as
  `string(ext.Value)`, including `1.2.840.113635.100.8.13.1` and `.13.3`, which Apple documents
  as DER integers.
- `brandonweeks/nanoca` `verifiers/apple/apple.go`: no comparison of the CSR key with the
  attested key anywhere (`grep PublicKey` finds none in `handlers.go`, `certutil/`, or the
  verifier), and no comparison of the ordered identifier with the attested device. Three of the
  ten documented OIDs are extracted.
- `fleetdm/fleet` `server/mdm/acme/internal/service/challenge.go`: carries the TODO "Should we
  do any validation on leaf.PublicKey? Apple docs on validation calls out 'Retain the public key
  in the attestation leaf certificate for a later validation.' So unsure if we should persist
  it, or what the later validation might be." The later validation is the key binding at
  finalize, and it is not done.
- `smallstep/certificates` docs and `reference_projects.md`: step-ca "trusts any Apple device
  without extra policy"; its `AuthorizeOrderIdentifier`
  (`authority/provisioner/acme.go:394-426`) has no case for `permanent-identifier`, so
  configuring any x509 policy rejects device-attest orders outright while configuring none
  leaves no device allowlisting at all.
- Apple's own documents disagree about macOS: the ACME payload YAML says `HardwareBound` and
  `Attest` are supported from macOS 14 on Apple silicon, while the DDM credential YAML says "On
  macOS, this is a required key. Set the value to `false`" for both. The profile path is the one
  Fleet ships against with both set true, so the divergence is recorded rather than resolved.

## What they do

- **nanoca**: pluggable `AttestationVerifier` keyed on the statement format; decodes the
  attestation object into `map[string]any`; checks the freshness code before the chain; verifies
  the chain against an embedded Apple root with `ExtKeyUsageAny`; extracts serial and UDID as
  raw bytes and maps them to RFC 4043 and RFC 4108 identifiers; stores the raw CBOR on the
  challenge and re-verifies it at finalize; derives the certificate's SANs from the attestation
  rather than from the order, which makes the missing identifier comparison less dangerous than
  it looks.
- **step-ca**: one `deviceAttest01Validate` with a format switch; verifies the chain against the
  embedded Apple root or the provisioner's configured roots; compares the freshness code only
  when the extension is present; requires the ordered identifier to equal either the attested
  UDID or the attested serial; carries the attested key's fingerprint on the authorization and
  compares it with the CSR key at finalize when it is set; has no per-device policy hook.
- **Fleet**: scopes ACME under a per-enrollment path; two-stage CBOR decode with a
  well-formedness pre-check; requires the freshness code and the attested serial to equal the
  enrollment's host identifier; gates issuance on the serial having a DEP assignment; rewrites
  the certificate subject; answers `badAttestationStatement`; does not compare the CSR key with
  the attested key.
- **ios-acme-simulator**: the device side, and the clearest statement of the invariant: the
  attestation leaf is minted over the same public key as the CSR.

## What we do better

1. The freshness code is required, not conditional. An attestation whose leaf carries no
   freshness extension fails exactly as one carrying the wrong code does, so an attestation
   produced for a different challenge cannot be replayed into this one by stripping the
   extension.
2. The attested key is always compared with the key being certified, by marshalled
   SubjectPublicKeyInfo and in constant time, with no path that skips the comparison. This is
   the "later validation" Apple's guidance names and that all three servers omit or make
   optional.
3. Every extension is parsed by its documented encoding: bare UTF-8 for the identity and version
   values, DER integers for the System Integrity Protection and kernel extension statuses, with
   Apple's polarity applied (SIP 0 means enabled, kernel extensions 0 means none allowed). All
   ten documented OIDs are extracted, against three in nanoca and four in step-ca.
4. A malformed extension is an error rather than a value: a DER integer that does not parse, or
   that has trailing bytes, fails the attestation instead of yielding a plausible-looking
   result. An empty value is treated as absent, which is what Apple documents for a property its
   servers could not verify.
5. Absent identity is handled rather than stumbled over. Apple omits the serial number and UDID
   for a user enrollment, so `Properties.Identified` reports the case and the policy hook
   decides, instead of the order failing later with a message about no identifiers.
6. The chain is verified before anything is read from the leaf, so no property from an untrusted
   certificate reaches a policy decision or a log line.
7. The trust anchor is verified at its source. The embedded root was fetched from
   `apple.com/certificateauthority/private` and its SHA-256 recorded in the source, rather than
   copied out of another project's repository.
8. One verifier serves both Apple surfaces. The same code reads the ACME attestation object and
   the `DevicePropertiesAttestation` chain, so the MDM command path cannot drift from the
   enrollment path.

## Verified by

1. `attest.TestVerifyFreshness/MissingExtensionFails` and `/AnotherChallengeFails` (prove claim
   1; step-ca's guard would accept the first because the extension is absent).
2. `attest.TestVerifyBindsTheAttestedKey` (proves claim 2; nanoca and Fleet issue for the
   unrelated key because neither compares it, and step-ca skips the comparison when the
   authorization carries no fingerprint).
3. `attest.TestParseObjectReadsEveryDocumentedProperty` (proves claim 3; reading
   `1.2.840.113635.100.8.13.1` as a string yields `"\x02\x01\x00"` rather than a boolean, so the
   reference behaviour cannot report SIP at all).
4. `attest.TestMalformedExtensions/truncated_integer`,
   `/trailing_bytes_after_integer`, and `/BlankIsAbsent` (prove claim 4).
5. `attest.TestUserEnrollmentHasNoIdentity` (proves claim 5).
6. `attest.TestVerifyChain/ForeignAuthority`, `/Expired`, `/MissingIntermediate`, and
   `/DefaultAnchorsAreApple` (prove claims 6 and 7; the last shows the shipped default trusts
   Apple alone).
7. `attest.TestParseChain` and `e2e.TestE2E_DeviceAttestation` (prove claim 8: the same verifier
   accepts the DeviceInformation form).
8. `attest.FuzzParseObject` and `cbor.FuzzUnmarshal` (no panic on any input, and everything
   accepted re-encodes to the bytes that arrived, which is what lets a stored attestation be
   re-verified at finalize).
9. `attest.TestParseObjectWithoutAttestation` (a statement with no `x5c` is a policy question,
   reported as `ErrNoAttestation`, not a parse failure).

## Rejected alternatives

- A CBOR module (`fxamacker/cbor`, which all three references use): the plan of record admits no
  new module dependencies. `internal/cbor` reads the small subset the format needs, rejects
  everything else, and is fuzzed; the general decoder would be more attack surface than the
  format requires.
- Reading the attestation with a WebAuthn library: Apple's object has no `authData` and none of
  the WebAuthn ceremony applies. The format name and the `x5c` chain are the whole of it.
- Treating a missing serial number as a failure: it would reject every user enrollment, which
  Apple documents as normal.
- Requiring the attestation to be bound to the ACME account key: Apple hashes the challenge
  token alone, not the RFC 8555 key authorization, so the binding does not exist to check. The
  server binds the device through the one-time client identifier instead (record 0031).
- Pinning Apple's intermediate: Apple rotates it and sends it in the chain. Pinning the root and
  accepting the intermediate the device presents is what Apple's documentation describes.
- Enforcing a maximum attestation age beyond the certificate's own validity: Apple's device-side
  cache returns an attestation up to seven days old for the MDM command path, so an age limit
  would reject genuine responses. The freshness code, which is per-challenge for ACME, is the
  binding that matters.
