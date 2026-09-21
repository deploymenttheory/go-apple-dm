# Apple protocol helpers

These root-library helpers use generated wire types and existing dependencies.
Examples compile with the test suites. None invokes an OS utility at runtime.

| Purpose | API and example | Apple reference |
| --- | --- | --- |
| Managed Apple Account JWT | [`enroll.MAIDToken`](../../devicemanagement/mdmprotocol/enroll/maid.go), [GetToken handler](../../server/service/maid_example_test.go) | [Get Token](https://developer.apple.com/documentation/devicemanagement/get-token) |
| ADE account password hash | [`ade.PasswordHash`](../../devicemanagement/mdmprotocol/enroll/ade/password.go), [commands](../../devicemanagement/mdmprotocol/enroll/ade/password_example_test.go) | [Hash fields](https://developer.apple.com/documentation/devicemanagement/passwordhash/salted-sha512-pbkdf2-data.dictionary) |
| FileVault response decryption | [`cms.DecryptEnvelope`](../../devicemanagement/mdmprotocol/cms/envelope.go), [response handling](../../devicemanagement/mdmprotocol/cms/envelope_example_test.go) | [RotateFileVaultKey result](https://developer.apple.com/documentation/devicemanagement/rotatefilevaultkeyresponse/rotateresult-data.dictionary) |
| Server bypass codes | [`activationlock.Generate` and `Hash`](../../devicemanagement/mdmprotocol/activationlock/bypass.go), [DEP request](../../devicemanagement/mdmprotocol/activationlock/example_test.go) | [Bypass code algorithm](https://developer.apple.com/documentation/devicemanagement/creating-and-using-bypass-codes) |
| Package/app manifests | [`manifest.Build`](../../devicemanagement/mdmprotocol/manifest/manifest.go), [macOS and iOS examples](../../devicemanagement/mdmprotocol/manifest/example_test.go) | [Installing packages](https://developer.apple.com/documentation/devicemanagement/installing-packages) |

## Managed Apple Accounts and ADE passwords

`MAIDToken` uses the ADE server's registered RSA certificate/key and `server_uuid`
from AccountDetail. It emits RS256 with `iss`, `iat`, a fresh random UUID `jti`
and `service_type=com.apple.maid`. The signing key may implement `crypto.Signer`.
Use the existing authenticated GetToken handler and advertise `com.apple.mdm.token`
in the MDM profile. Unknown token services receive HTTP 400. AXM ES256 credentials
are for a different API. The existing DEP keypair workflow generates its RSA
certificate in Go; registering that public certificate with Apple remains a portal
step. Keep its registered key rather than generating an unrelated JWT certificate.

`PasswordHash` creates PBKDF2-HMAC-SHA512 using 32 random salt bytes and an explicit
positive iteration count. Its 128-byte derived value follows Apple's example;
Apple's field description does not establish a universal derived-value length.
Calibrate iterations for your deployment. The returned XML plist is embedded as
data in AccountConfiguration or SetAutoAdminPassword. The latter requires the GUID
of an administrator actually created during ADE, not its short name.

## Automatic FileVault encryption certificates

[`replycerts.Manager`](../../server/replycerts/replycerts.go) supplies encryption
identities for both the escrow-profile workflow and explicit rotation commands.
Generation uses Go's cryptography packages; no OpenSSL, Keychain or other OS
certificate tool is required. The reference server requires encrypted persistent
SQL storage and retains each identity before queueing its command.

### macOS 26 escrow with a bootstrap token

Apple documents automatic rotation when an MDM installs an escrow profile on a
Mac with an existing personal recovery key and a bootstrap token. This path does
not require collecting a user's password. See [macOS 26 enterprise changes](https://support.apple.com/en-us/124963)
and the [FileVault escrow payload](https://developer.apple.com/documentation/devicemanagement/fderecoverykeyescrow).

1. Confirm macOS 26 or later, FileVault enabled, an existing personal recovery
   key and bootstrap-token availability. Inspect installed profiles for an
   existing escrow payload before changing escrow ownership.
2. Submit the following JSON to the authenticated admin route
   `POST /admin/v1/enrollments/device/DEVICE-ID/filevault/escrow`:

   ```json
   {
     "CommandUUID": "b9d42163-249f-41ed-8873-ed397393965e",
     "ProfileIdentifier": "com.example.filevault.escrow",
     "Location": "Your organisation's recovery service"
   }
   ```

   Choose a fresh command UUID and an identifier belonging to your deployment.
   This requires `enqueueCommand.InstallProfile` authorization and an enabled device enrollment.
   `Location` is Apple's user-facing description of where the key is escrowed.
3. The server atomically retains a new certificate/key and stable payload UUIDs,
   then queues an `InstallProfile` command. Its system profile contains
   `com.apple.security.pkcs1` and `com.apple.security.FDERecoveryKeyEscrow`; the
   escrow payload references the certificate in that same profile. Inspect the
   returned `Queued`/`Skipped` result, request a push and await acknowledgement.
4. Retrieve the encrypted key through `SecurityInfo` with the required enrollment
   access rights. Apple also documents extraction by local administrative software
   from `/var/db/FileVaultPRK.dat`. Use the retained command UUID with
   `Manager.Recipient`, then `cms.DecryptEnvelope`, and store the recovered key in
   encrypted persistent storage. Acknowledgement alone does not prove key recovery.

The route prepares and queues the profile. The caller performs the prerequisite
checks, push, acknowledgement tracking, retrieval and secret persistence. A retry
of preparation reuses the identity; queue submission still follows the existing
command-UUID conflict semantics. Keep the private key while the profile or any
encrypted recovery keys depend on it. Removing the profile does not restore the
previous recovery key.

Validate remote `SecurityInfo` retrieval separately from local-file extraction;
the enrollment must grant that command's access right. Neither profile acceptance
nor CMS decryption establishes that the recovered key can unlock the disk. See
the [live acceptance checks](../testing/bench.md#apple-management-feature-checks).

### Explicit rotation command

The reference server's command-enqueue workflow automatically fills the encryption
certificate for `RotateFileVaultKey`. Omit `ReplyEncryptionCertificate` for a
personal rotation and `NewCertificate` for an institutional rotation.
The command still requires Apple's `FileVaultUnlock` credentials; for APFS personal
rotation, `Password` is a FileVault-enabled user's password. macOS does not provide
a pre-existing password file. The bootstrap-token escrow flow above is a separate
operation, not a credential substitute in this command. See Apple's
[unlock fields](https://developer.apple.com/documentation/devicemanagement/rotatefilevaultkeycommand/command-data.dictionary/filevaultunlock-data.dictionary).

1. Decode the command and bind its UUID and enrollment identity to a workflow.
2. Generate a distinct RSA-2048 key and self-signed X.509 certificate entirely in
   Go, using the existing certificate generator.
3. Persist the certificate, private key and command digest in encrypted SQL
   protocol state before queueing the completed command. Storage failure prevents
   queueing. Repeated/concurrent preparation reuses the persisted identity.
4. Retain the exact identity for delayed replies and restarts. A different command
   receives a different key automatically; no manual certificate-file setup is needed.
5. After an authenticated response, use
   [`replycerts.Manager.Recipient`](../../server/replycerts/replycerts.go) and
   `cms.DecryptEnvelope` to recover `EncryptedNewRecoveryKey` as bytes. Persist the
   recovered secret securely before explicitly forgetting a settled binding.

External encryption certificates are rejected; a retry may include the identical
certificate the workflow already generated. Embedders that queue directly through
`service.Core` call `replycerts.Manager.Prepare` before enqueueing.

Certificates are valid for one year. Bound keys are not replaced or deleted based
on certificate expiry: that would make delayed responses undecryptable. A failed
queue attempt may retain a prepared binding for retry. The secret-store retention
decision belongs to the caller after command settlement and recovery of all
dependent encrypted material. Private keys have no
administrative export route. The existing DEP, enrollment and HTTPS provisioning
workflows also generate key material in Go; Apple-issued certificate registration
and issuance retain their documented external steps.

`DecryptEnvelope` accepts BER/DER CMS with a recipient certificate and matching
RSA `crypto.Decrypter`, including external key implementations. It reports parse,
recipient, unsupported-algorithm and decryption errors without printing secrets.
It does not authenticate the sender. Tests include independently generated
[OpenSSL fixtures](../../devicemanagement/mdmprotocol/cms/testdata/envelopes/README.md);
the test run only reads those fixtures and does not invoke OpenSSL.

## Bypass codes and manifests

Store a generated bypass code before enabling Activation Lock with its hash.
Unlock uses the code. `Hash` accepts Apple's canonical server-code layout only;
device-returned bypass codes remain opaque. The final character encodes three
un-padded bits, unlike ordinary Base32. Hashing uses Apple's fixed PBKDF2-HMAC-SHA256
parameters, verified against [independent vectors](../../devicemanagement/mdmprotocol/activationlock/testdata/README.md).

`manifest.Build` takes the actual asset reader, HTTPS URL and explicit bundle
identifier/version/title. It streams SHA-256 as a whole-file digest (`chunkSize=0`)
or explicit chunk digests, returning byte count separately. It does not inspect
archives, host files, emit new MD5 values or emit removed manifest fields. Publish
exactly the bytes hashed. Use InstallEnterpriseApplication for macOS packages and
InstallApplication with a hosted manifest for enterprise iOS/iPadOS apps.

Independent vectors and fixtures check encoding and local interoperability.
Live acceptance needs Apple-registered credentials, eligible enrollment and actual
installation assets. Follow the [feature checks](../testing/bench.md#apple-management-feature-checks)
and record any untested operations explicitly.
