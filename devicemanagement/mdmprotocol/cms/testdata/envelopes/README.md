# CMS interoperability fixtures

Generated with OpenSSL 3.6.4 on 2026-09-15. These are public, disposable test
keys and a synthetic recovery key. Certificates deliberately expire after one
day: decryption of delayed responses must remain possible after expiry.

Each certificate/key pair was made with:

```sh
openssl req -new -x509 -newkey rsa:2048 -nodes -keyout recipient.key.pem -out recipient.cert.pem -subj '/CN=PUBLIC TEST FIXTURE recipient' -days 1
```

The second pair substitutes `other`. `plaintext.txt` has no trailing newline.

```sh
openssl cms -encrypt -binary -in plaintext.txt -outform DER -out aes256.der -aes-256-cbc other.cert.pem recipient.cert.pem
openssl cms -encrypt -binary -in plaintext.txt -outform DER -out aes128.ber -aes-128-cbc -stream other.cert.pem recipient.cert.pem
openssl cms -encrypt -binary -in plaintext.txt -outform DER -out des3.der -des3 other.cert.pem recipient.cert.pem
openssl cms -encrypt -binary -in plaintext.txt -outform DER -out oaep.der -aes-256-cbc -recip recipient.cert.pem -keyopt rsa_padding_mode:oaep -keyopt rsa_oaep_md:sha256
```

`-stream` produces BER indefinite lengths even with `-outform DER`.
The multi-recipient files check certificate selection. OAEP selects only the
recipient certificate, permitting a missing-recipient check. These fixtures
establish OpenSSL interoperability, not a live FileVault rotation result.
