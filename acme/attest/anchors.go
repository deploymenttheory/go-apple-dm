package attest

import (
	"crypto/x509"
	"encoding/pem"
	"sync"
)

// Apple publishes this root at https://www.apple.com/certificateauthority/private/
// as Apple_Enterprise_Attestation_Root_CA.pem. Every Managed Device
// Attestation chain ends here, whether it arrives through ACME or through a
// DeviceInformation response. Apple issues the leaf from an intermediate
// (Apple Enterprise Attestation Sub CA 1 at the time of writing) which the
// device sends alongside the leaf, so only the root is embedded.
//
//	Subject: CN=Apple Enterprise Attestation Root CA, O=Apple Inc., C=US
//	Serial:  42c0c2bb2c727c5c5eabf6f1a66f1fac5d798737
//	Valid:   2022-02-16 to 2047-02-20
//	SHA-256: ccf59ef8fcb3017d97f8b5fa6fa90e7a3f9283f76b55ac6cf6eda8b8b949f05b
const appleEnterpriseAttestationRootCAPEM = `-----BEGIN CERTIFICATE-----
MIICJDCCAamgAwIBAgIUQsDCuyxyfFxeq/bxpm8frF15hzcwCgYIKoZIzj0EAwMw
UTEtMCsGA1UEAwwkQXBwbGUgRW50ZXJwcmlzZSBBdHRlc3RhdGlvbiBSb290IENB
MRMwEQYDVQQKDApBcHBsZSBJbmMuMQswCQYDVQQGEwJVUzAeFw0yMjAyMTYxOTAx
MjRaFw00NzAyMjAwMDAwMDBaMFExLTArBgNVBAMMJEFwcGxlIEVudGVycHJpc2Ug
QXR0ZXN0YXRpb24gUm9vdCBDQTETMBEGA1UECgwKQXBwbGUgSW5jLjELMAkGA1UE
BhMCVVMwdjAQBgcqhkjOPQIBBgUrgQQAIgNiAAT6Jigq+Ps9Q4CoT8t8q+UnOe2p
oT9nRaUfGhBTbgvqSGXPjVkbYlIWYO+1zPk2Sz9hQ5ozzmLrPmTBgEWRcHjA2/y7
7GEicps9wn2tj+G89l3INNDKETdxSPPIZpPj8VmjQjBAMA8GA1UdEwEB/wQFMAMB
Af8wHQYDVR0OBBYEFPNqTQGd8muBpV5du+UIbVbi+d66MA4GA1UdDwEB/wQEAwIB
BjAKBggqhkjOPQQDAwNpADBmAjEA1xpWmTLSpr1VH4f8Ypk8f3jMUKYz4QPG8mL5
8m9sX/b2+eXpTv2pH4RZgJjucnbcAjEA4ZSB6S45FlPuS/u4pTnzoz632rA+xW/T
ZwFEh9bhKjJ+5VQ9/Do1os0u3LEkgN/r
-----END CERTIFICATE-----
`

var (
	anchorsOnce sync.Once
	anchors     []*x509.Certificate
)

// AppleAnchors returns the default trust anchor for Managed Device
// Attestation, the Apple Enterprise Attestation Root CA. The slice is a
// fresh copy each call so a caller may append its own anchors for testing
// without reaching the next caller.
func AppleAnchors() []*x509.Certificate {
	anchorsOnce.Do(func() {
		block, _ := pem.Decode([]byte(appleEnterpriseAttestationRootCAPEM))
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			panic("attest: embedded Apple certificate: " + err.Error())
		}
		anchors = []*x509.Certificate{cert}
	})
	return append([]*x509.Certificate(nil), anchors...)
}
