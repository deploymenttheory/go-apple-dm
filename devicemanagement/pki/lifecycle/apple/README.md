# Bundled Apple authorities

Public CA certificates, obtained from Apple's certificate authority downloads.
Bundle reviewed 2026-09-13. These are trust anchors/intermediates, not vendor or
customer identities. Their private keys are not part of this repository.

| File | Official download | SHA-256 of DER |
| --- | --- | --- |
| AppleIncRootCertificate.pem | https://www.apple.com/appleca/AppleIncRootCertificate.cer | `b0b1730ecbc7ff4505142c49f1295e6eda6bcaed7e2c68c5be91b5a11001f024` |
| AppleWWDRCAG3.pem | https://www.apple.com/certificateauthority/AppleWWDRCAG3.cer | `dcf21878c77f4198e4b4614f03d696d89c66c66008d4244e1b99161aac91601f` |
| AppleAAI2CA.pem | https://www.apple.com/certificateauthority/AppleAAI2CA.cer | `d3496f4b73cd67aab9f2fcb1d5aa41f8dc457769c455c792b70ddb19e92023d6` |

Updates require explicit review of the source and fingerprint. Imports perform
normal issued-chain signature, purpose, validity, and trust checks. Applications
can override `lifecycle.Trust.Apple` with their own reviewed authority bundle.
