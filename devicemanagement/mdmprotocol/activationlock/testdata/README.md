# Bypass-code vectors

Generated independently in Python 3 on 2026-09-15. Raw inputs are sixteen zero
bytes, bytes 0 through 15, and sixteen 255 bytes. SHA-256 PBKDF2 uses
`hashlib.pbkdf2_hmac('sha256', raw, bytes(4), 50000, 32)` and uppercase hex.

Encoding treats the raw input as 128 big-endian bits, divides it into 25 groups
of five bits and one group of three bits, and indexes Apple's alphabet
`0123456789ACDEFGHJKLMNPQRTUVWXYZ`. Dashes follow symbols 5, 10, 14, 18 and 22.
The last symbol's three bits are not padded to five bits. See
[Apple's reference algorithm](https://developer.apple.com/documentation/devicemanagement/creating-and-using-bypass-codes).
