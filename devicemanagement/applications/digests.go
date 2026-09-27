package applications

import (
	"crypto/md5" // #nosec G501 -- Apple manifests and imported package metadata require legacy MD5; SHA-256 and signer trust protect integrity.
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
)

// Digests holds explicit algorithms. SHA3512 is SHA3-512, not SHA-512. HashType
// and HashValue optionally select one of MD5, SHA256, SHA512 or SHA3-512. Empty
// fields in metadata impose no expectation. Content contains all computed hashes
// and selects SHA256 as its canonical hash. MD5 is compatibility data only.
type Digests struct {
	MD5       string `json:"md5,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SHA3512   string `json:"sha3512,omitempty"`
	SHA512    string `json:"sha512,omitempty"`
	HashType  string `json:"hashType,omitempty"`
	HashValue string `json:"hashValue,omitempty"`
}

// Validate checks lengths, encoding, algorithm names and contradictory expectations.
func (d Digests) Validate() error {
	for _, f := range []struct {
		name, s string
		size    int
	}{
		{"md5", d.MD5, md5.Size},
		{"sha256", d.SHA256, sha256.Size},
		{"sha3512", d.SHA3512, 64},
		{"sha512", d.SHA512, sha512.Size},
	} {
		if f.s != "" && !validDigest(f.s, f.size) {
			return invalidField(f.name)
		}
	}
	if d.HashType == "" && d.HashValue == "" {
		return nil
	}
	size := 0
	switch d.HashType {
	case "MD5":
		size = md5.Size
	case "SHA256":
		size = sha256.Size
	case "SHA512", "SHA3-512":
		size = 64
	default:
		return invalidField("hashType")
	}
	if !validDigest(d.HashValue, size) {
		return invalidField("hashValue")
	}
	if explicit := d.forType(d.HashType); explicit != "" && !strings.EqualFold(explicit, d.HashValue) {
		return fmt.Errorf("%w: conflicting hashValue", ErrInvalid)
	}
	return nil
}

// validDigest checks hexadecimal encoding and the algorithm-specific byte length.
func validDigest(s string, size int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == size
}

// forType selects the digest for an explicit algorithm without conflating SHA families.
func (d Digests) forType(algorithm string) string {
	switch algorithm {
	case "MD5":
		return d.MD5
	case "SHA256":
		return d.SHA256
	case "SHA512":
		return d.SHA512
	case "SHA3-512":
		return d.SHA3512
	}
	return ""
}

// Match checks every supplied expectation against independently computed digests.
func (d Digests) Match(actual Digests) error {
	if err := d.Validate(); err != nil {
		return err
	}
	for _, f := range []struct{ name, want, got string }{
		{"md5", d.MD5, actual.MD5},
		{"sha256", d.SHA256, actual.SHA256},
		{"sha3512", d.SHA3512, actual.SHA3512},
		{"sha512", d.SHA512, actual.SHA512},
		{"hashValue", d.HashValue, actual.forType(d.HashType)},
	} {
		if f.want != "" && !strings.EqualFold(f.want, f.got) {
			return fmt.Errorf("%w: %s", ErrIntegrity, f.name)
		}
	}
	return nil
}

type digestWriter struct{ md5, sha256, sha3512, sha512 hash.Hash }

// newDigestWriter initializes all supported algorithms for one streaming pass.
func newDigestWriter() *digestWriter {
	return &digestWriter{md5: md5.New(), sha256: sha256.New(), sha3512: sha3.New512(), sha512: sha512.New()} // #nosec G401 -- MD5 is computed for Apple manifest compatibility alongside strong digests.
}

// Write feeds the same bytes to every digest algorithm.
func (d *digestWriter) Write(b []byte) (int, error) {
	return io.MultiWriter(d.md5, d.sha256, d.sha3512, d.sha512).Write(b)
}

// sum returns measured digests with SHA-256 as the canonical content identity.
func (d *digestWriter) sum() Digests {
	sha := hex.EncodeToString(d.sha256.Sum(nil))
	return Digests{MD5: hex.EncodeToString(d.md5.Sum(nil)), SHA256: sha, SHA3512: hex.EncodeToString(d.sha3512.Sum(nil)), SHA512: hex.EncodeToString(d.sha512.Sum(nil)), HashType: "SHA256", HashValue: sha}
}
