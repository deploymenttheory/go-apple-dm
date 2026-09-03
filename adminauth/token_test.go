package adminauth_test

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/adminauth"
)

func TestMint(t *testing.T) {
	seen := make(map[adminauth.Token]bool)
	for range 200 {
		tok, err := adminauth.Mint()
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		if seen[tok] {
			t.Fatalf("Mint returned a duplicate: %s", adminauth.Redact(tok))
		}
		seen[tok] = true
		if !strings.HasPrefix(string(tok), adminauth.Prefix) {
			t.Fatalf("token has no prefix: %s", adminauth.Redact(tok))
		}
		if len(tok) != adminauth.TokenLen {
			t.Fatalf("token length = %d, want %d", len(tok), adminauth.TokenLen)
		}
		if !adminauth.Valid(tok) {
			t.Fatalf("a freshly minted token is invalid: %s", adminauth.Redact(tok))
		}
	}
}

// The checksum is what lets a mistyped or truncated credential be refused
// before it becomes a database round trip.
func TestValid(t *testing.T) {
	good, err := adminauth.Mint()
	if err != nil {
		t.Fatal(err)
	}
	s := string(good)

	// Flipping any character of the body breaks the checksum.
	t.Run("BodyTamperFails", func(t *testing.T) {
		for i := len(adminauth.Prefix); i < len(s)-6; i++ {
			b := []byte(s)
			if b[i] == 'a' {
				b[i] = 'b'
			} else {
				b[i] = 'a'
			}
			if adminauth.Valid(adminauth.Token(b)) {
				t.Fatalf("a token with byte %d changed still validated", i)
			}
		}
	})

	t.Run("Rejects", func(t *testing.T) {
		for name, tok := range map[string]string{
			"empty":          "",
			"prefix only":    adminauth.Prefix,
			"wrong prefix":   "xxxx_" + s[len(adminauth.Prefix):],
			"truncated":      s[:len(s)-1],
			"overlong":       s + "a",
			"bad checksum":   s[:len(s)-6] + "000000",
			"non alphabet":   s[:len(s)-1] + "!",
			"whitespace":     " " + s[1:],
			"no underscore":  strings.Replace(s, "_", "x", 1),
			"upper prefixed": strings.ToUpper(adminauth.Prefix) + s[len(adminauth.Prefix):],
		} {
			if adminauth.Valid(adminauth.Token(tok)) {
				t.Errorf("%s: validated %q", name, tok)
			}
		}
	})
}

func TestDigest(t *testing.T) {
	a, err := adminauth.Mint()
	if err != nil {
		t.Fatal(err)
	}
	b, err := adminauth.Mint()
	if err != nil {
		t.Fatal(err)
	}
	da, db := adminauth.Digest(a), adminauth.Digest(b)
	if len(da) != 64 {
		t.Fatalf("digest length = %d, want 64 hex characters", len(da))
	}
	if da == db {
		t.Fatal("two tokens share a digest")
	}
	if da != adminauth.Digest(a) {
		t.Fatal("Digest is not deterministic")
	}
	// The digest must not contain the token: a store that leaked one would be
	// storing the credential.
	if strings.Contains(da, string(a)[len(adminauth.Prefix):]) {
		t.Fatal("the digest contains the token body")
	}
}

func TestRedact(t *testing.T) {
	tok, err := adminauth.Mint()
	if err != nil {
		t.Fatal(err)
	}
	got := adminauth.Redact(tok)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("Redact = %q, want an ellipsis", got)
	}
	if len(got) >= len(tok) {
		t.Fatalf("Redact = %q, no shorter than the token", got)
	}
	// Short input must not panic or reveal more than it has.
	if got := adminauth.Redact("mdmt_"); got != adminauth.Prefix+"..." {
		t.Fatalf("Redact(short) = %q", got)
	}
	if got := adminauth.Redact(""); got != adminauth.Prefix+"..." {
		t.Fatalf("Redact(empty) = %q", got)
	}
}
