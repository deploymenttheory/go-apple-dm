package ddm

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"

	schemaddm "github.com/deploymenttheory/go-apple-mdm/schema/ddm"
)

// DeclarationRef names one declaration in a manifest.
type DeclarationRef struct {
	Kind        schemaddm.Kind
	Identifier  string
	ServerToken string
}

func compareRefs(a, b DeclarationRef) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Identifier, b.Identifier), cmp.Compare(a.ServerToken, b.ServerToken))
}

// SortRefs orders refs by (kind, identifier, token), the order every
// manifest and token computation uses.
func SortRefs(refs []DeclarationRef) []DeclarationRef {
	out := slices.Clone(refs)
	slices.SortFunc(out, compareRefs)
	return out
}

// DeclarationsToken derives the manifest token from the sorted refs:
// sha256 over each kind, identifier, and server token written with a
// 4-byte length prefix. It is independent of input order and of the wall
// clock, distinguishes ("ab","c") from ("a","bc"), and is 64 hex characters,
// within Apple's 64-octet guidance (decision record 0019).
func DeclarationsToken(refs []DeclarationRef) string {
	h := sha256.New()
	var n [4]byte
	write := func(s string) {
		binary.BigEndian.PutUint32(n[:], uint32(len(s))) // #nosec G115 -- identifiers and tokens are bounded far below 4 GiB
		h.Write(n[:])
		h.Write([]byte(s))
	}
	for _, r := range SortRefs(refs) {
		write(string(r.Kind))
		write(r.Identifier)
		write(r.ServerToken)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TokenFor derives a declaration's ServerToken from its canonical bytes:
// hex(sha256(canonical)), 64 characters.
func TokenFor(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
