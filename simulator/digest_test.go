package simulator_test

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- RFC 2617 Digest requires MD5
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/deploymenttheory/go-apple-mdm/simulator"
)

func md5s(s string) string {
	sum := md5.Sum([]byte(s)) // #nosec G401 -- RFC 2617 Digest requires MD5
	return hex.EncodeToString(sum[:])
}

func TestHA1(t *testing.T) {
	t.Parallel()
	if got := simulator.HA1("alice", "mdm", "secret"); got != md5s("alice:mdm:secret") {
		t.Fatalf("HA1 = %s", got)
	}
}

func TestDigestResponse(t *testing.T) {
	t.Parallel()
	const challenge = `Digest realm="mdm", nonce="0123abcd", qop="auth", algorithm=MD5`
	got, err := simulator.DigestResponse(challenge, "alice", "secret", "/mdm/checkin", bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8}))
	if err != nil {
		t.Fatal(err)
	}
	cnonce := "0102030405060708"
	want := md5s(strings.Join([]string{md5s("alice:mdm:secret"), "0123abcd", "00000001", cnonce, "auth", md5s("POST:/mdm/checkin")}, ":"))
	for _, part := range []string{`username="alice"`, `realm="mdm"`, `nonce="0123abcd"`, `uri="/mdm/checkin"`, `qop=auth`, `nc=00000001`, `cnonce="` + cnonce + `"`, `response="` + want + `"`} {
		if !strings.Contains(got, part) {
			t.Fatalf("response %q lacks %q", got, part)
		}
	}
	// The scheme prefix is optional and the default random source works.
	if _, err := simulator.DigestResponse(`realm="mdm", nonce=abc`, "alice", "secret", "/mdm", nil); err != nil {
		t.Fatalf("without prefix: %v", err)
	}
}

func TestDigestResponseFailures(t *testing.T) {
	t.Parallel()
	bad := map[string]string{
		"no nonce":          `Digest realm="mdm", qop="auth"`,
		"unterminated":      `Digest realm="mdm, nonce="x"`,
		"junk after quote":  `Digest realm="mdm"x, nonce="x"`,
		"missing equals":    `Digest realm`,
		"empty name":        `Digest ="mdm"`,
		"bare scheme":       `Digest`,
		"only scheme space": `Digest `,
	}
	for name, ch := range bad {
		if _, err := simulator.DigestResponse(ch, "alice", "secret", "/mdm", nil); !errors.Is(err, simulator.ErrDigestChallenge) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	errRand := errors.New("entropy exhausted")
	if _, err := simulator.DigestResponse(`Digest realm="mdm", nonce="x"`, "alice", "secret", "/mdm", iotest.ErrReader(errRand)); !errors.Is(err, errRand) {
		t.Fatalf("rand failure: %v", err)
	}
}
