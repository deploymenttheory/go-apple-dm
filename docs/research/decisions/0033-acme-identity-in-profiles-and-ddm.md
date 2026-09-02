# 0033: ACME identity in enrollment profiles, declarative credentials, and the reference server

Status: accepted
Date: 2026-09-02
Phase: 7

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/acmecertificate>
  (`DirectoryURL`, `ClientIdentifier`, `KeyType`, `KeySize`, `HardwareBound`, `Attest`,
  `Subject`, `SubjectAltName`, `UsageFlags`, `ExtendedKeyUsage`, `KeyIsExtractable`,
  `AllowAllAppsAccess`; the rule that `Attest` requires `HardwareBound`; and the hardware
  support table naming A11 Bionic and later, all M series, and Apple silicon Macs)
- Doc: <https://developer.apple.com/documentation/devicemanagement/assetcredentialacme>
- Doc: <https://developer.apple.com/documentation/devicemanagement/securityidentity>
- Doc: <https://developer.apple.com/documentation/devicemanagement/deviceinformationcommand>
  (`DeviceAttestationNonce`: up to 32 bytes, and "Requests for new attestations are rate
  limited. If it is fewer than 7 days since the system generated an attestation, the device
  returns the cached attestation rather than generating a new one.")
- YAML: `third_party/device-management/mdm/profiles/com.apple.security.acme.yaml`
- YAML: `third_party/device-management/declarative/declarations/assets/credential.acme.yaml`
  (the `Reference` to a JSON document, and `Authentication.Type` of `MDM`, "a request that uses
  MDM semantics, which includes the device-identity certificate")
- YAML: `third_party/device-management/declarative/declarations/assets/credentials/acme.yaml`
- YAML: `third_party/device-management/declarative/declarations/configurations/security.identity.yaml`
  (`CredentialAssetReference` accepting `com.apple.asset.credential.acme`)
- YAML: `third_party/device-management/mdm/commands/information.device.yaml`

## References read

- `fleetdm/fleet@e1bbd21c` `server/mdm/apple/apple_mdm.go`
  (`acmeEnrollmentProfileMobileconfigTemplate`: `Attest` true, `HardwareBound` true,
  `KeySize` 384, `KeyType` `ECSECPrimeRandom`, and the MDM payload's
  `IdentityCertificateUUID` pointing at the ACME payload) and
  `articles/testing-apple-device-attestation-without-a-commercial-ca.md`
  ("Hardware-bound ACME certificates don't appear in the macOS keychain … They're visible only
  via MDM's `CertificateList` command")
- `zentralopensource/zentral@902e596c` `zentral/contrib/mdm/payloads.py`,
  `cert_issuer_backends/__init__.py` (`test_acme_payload`, which decides per model whether
  `HardwareBound` and `Attest` are usable), `declarations/cert_asset.py` (the
  `com.apple.asset.credential.acme` asset and its fallback to SCEP), and
  `public_views/mdm.py` (`ACMECredentialView`, the JSON document the asset points at)
- Records 0009 (the enrollment profile builder), 0020 and 0021 (the declarative engine and its
  assets), 0025 (the reference server's roles), 0031 and 0032 (the ACME server and attestation).

## Known pitfalls found

- Apple's own documents disagree about macOS. The ACME payload YAML says `HardwareBound` and
  `Attest` are supported from macOS 14 on Apple silicon; the declarative credential YAML says
  "On macOS, this is a required key. Set the value to `false`" for `HardwareBound` and "On
  macOS, set this key, if present, to `false`" for `Attest`. Fleet ships the profile path with
  both true, which is evidence that the profile documentation is the current one.
- `zentralopensource/zentral` `cert_issuer_backends/__init__.py:51`: a per-model capability
  table decides whether a device can attest, keyed on the Secure Enclave generation. Getting it
  wrong means a profile that installs and then fails, which is why our builder validates the
  combinations rather than trusting the caller.
- Fleet's field report: an ACME identity that is hardware bound does not appear in the macOS
  keychain, and `security find-identity` does not list it. The only way to see it is the MDM
  `CertificateList` command. An operator who cannot find the certificate has not lost it.
- `zentralopensource/zentral` `crypto.py:297` reads the attested serial number and UDID from the
  **subject** of the issued identity certificate. That only works if the issuing CA copies them
  there, which ours does not: we keep the attested properties in the certificate record instead,
  where they came from Apple rather than from a name someone chose.
- Apple's device-side attestation cache is seven days. A `DeviceInformation` query with a fresh
  nonce may still come back with the old freshness code, so the MDM command path cannot treat a
  mismatched freshness code as an attack the way the ACME path can.

## What they do

- **Fleet**: one mobileconfig template with the ACME payload hard-coded to an attested P-384
  key, the client identifier filled from the host's hardware serial, and a per-enrollment ACME
  path. The certificate subject is rewritten by the server at finalize.
- **Zentral**: an `ACMEIssuer` model whose fields map one to one onto the payload keys, a
  capability gate that downgrades `Attest` and `HardwareBound` per model, a declarative
  `com.apple.asset.credential.acme` asset that falls back to a SCEP asset for devices that
  cannot do ACME, and a view that serves the credential JSON.

## What we do better

1. The profile builder validates Apple's rules before a profile is signed: an RSA key cannot be
   hardware bound, a hardware bound key must be a 256 or 384 bit curve, `Attest` requires
   `HardwareBound`, and an RSA modulus must be a multiple of eight between 1024 and 4096. A
   profile that breaks one of these installs cleanly and then fails on the device, where the
   reason is much harder to see.
2. The client identifier is minted per device at the moment the profile is composed, carrying
   the serial number and UDID the server expects, rather than being the serial number itself.
   Every enrollment path gets the same treatment: automated device enrollment binds to the
   `MachineInfo` serial and UDID, and account-driven enrollment, which knows a person rather
   than a device, is bound as unidentified on purpose.
3. The declarative credential is served with MDM authentication and identifies the device by the
   certificate it presents, so the client identifier in it is bound to the device that asked. A
   document served by URL alone would be a client identifier anyone could fetch.
4. The attested device properties are stored with the issued certificate, so the admin API can
   answer which hardware holds an identity from what Apple attested rather than from a subject
   name. Zentral reads the same facts from the certificate subject, which is only as good as the
   CA that wrote it.
5. Both Apple surfaces use one verifier. The `DeviceInformation` attestation and the ACME
   attestation are read by the same code, so the command path cannot drift from the enrollment
   path, and the seven-day device cache is handled where it matters rather than being mistaken
   for a replay.
6. SCEP remains the default and ACME is a setting. A deployment moves when its hardware and its
   trust anchors are ready, not when it upgrades, and the ACME endpoints are mounted either way
   so a declarative credential can use them while enrollment profiles still carry SCEP.

## Verified by

1. `enroll.TestACMEProfileValidation` and its `ValidCombinations` subtest (proves claim 1;
   ten refusals and three accepted combinations).
2. `enroll.TestACMEProfileRoundTrip`, `app.TestACME/ProfileBindsTheDevice`, and
   `e2e.TestE2E_ACMEAttest` (prove claim 2: the identifier in an ADE profile carries that
   device's serial, and an attestation from another device is refused).
3. `app.TestACME/CredentialRequiresDeviceIdentity` and `e2e.TestE2E_ACMEDDMIdentity` (prove
   claim 3: the credential endpoint refuses a request with no device certificate, and the
   identifier it mints is bound to the device that presented one).
4. `app.TestACME/AdminListsCertificatesByDevice` (proves claim 4).
5. `e2e.TestE2E_DeviceAttestation` (proves claim 5, including the cached-attestation case).
6. `app.TestACME/Env` and `app.TestACME/SCEPRemainsTheDefault` (prove claim 6).

## Rejected alternatives

- Following the declarative credential YAML's macOS guidance and forcing `HardwareBound` false
  on Macs: it would disable attestation on exactly the hardware that supports it best. The
  profile documentation and Fleet's shipping behaviour both say otherwise, and the divergence is
  recorded here instead.
- A per-model capability table like Zentral's: it needs maintaining for every new Apple product
  and is wrong the moment one ships. A device that cannot attest sends a statement with no
  chain, which the server can answer for itself, so the decision is made from what the device
  actually did rather than from a table of what it should be able to do.
- Making ACME the default identity: an existing deployment's trust anchors, its hardware, and
  its enrollment profiles all have to move together. SCEP stays the default until an operator
  says otherwise.
- Putting the attested serial number in the certificate subject, as Zentral reads it: a subject
  is whatever the issuer wrote. The attested properties are kept with the certificate record,
  where their provenance is Apple's attestation.
- Serving the declarative credential without authentication: the document contains a client
  identifier, which Apple calls an anti-replay code, and a URL is not a secret.
