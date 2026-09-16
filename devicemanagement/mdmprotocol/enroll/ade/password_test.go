package ade

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/other"
)

func TestPasswordHashVector(t *testing.T) {
	salt := make([]byte, 32)
	for i := range salt {
		salt[i] = byte(i)
	}
	// Python 3 hashlib.pbkdf2_hmac('sha512', b'correct horse', bytes(range(32)), 1234, 128).
	const want = "2d7a3b547445f76996dc1fe0a2c660b3dcd6a7477610a416c05c30f30261e66d2f448b92a1facba24cb7d8eb4cc7067853019468d064cb5b07c93251ede4d614155c660dae21176c8abd69cf36bc457ef37ad33f85ffd1777eb199e4ac1df91571c15a2246e56db783b03de1f0e68054075498c841a0b900c85b09a572e258b6"
	data, err := passwordHash([]byte("correct horse"), salt, 1234)
	if err != nil {
		t.Fatal(err)
	}
	var hash other.PasswordHash
	if err := plist.Unmarshal(data, &hash); err != nil {
		t.Fatal(err)
	}
	h := hash.SALTEDSHA512PBKDF2
	if hex.EncodeToString(h.Entropy) != want || h.Iterations != 1234 || !bytes.Equal(h.Salt, salt) {
		t.Fatal("independent vector mismatch")
	}
	for _, command := range []any{&commands.AccountConfiguration{AutoSetupAdminAccounts: []commands.AccountConfigurationAutoSetupAdminAccounts{{ShortName: "admin", PasswordHash: data}}}, &commands.SetAutoAdminPassword{GUID: "ADE-created-user-GUID", PasswordHash: data}} {
		wire, err := plist.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := plist.Unmarshal(wire, &raw); err != nil {
			t.Fatal(err)
		}
		inner, ok := raw["passwordHash"].([]byte)
		if !ok {
			inner = requireType[[]byte](t, requireType[map[string]any](t, requireType[[]any](t, raw["AutoSetupAdminAccounts"])[0])["passwordHash"])
		}
		if !bytes.Equal(inner, data) {
			t.Fatal("inner plist was not embedded as data")
		}
	}
	first, err := PasswordHash([]byte("test"), 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PasswordHash([]byte("test"), 1)
	if err != nil || bytes.Equal(first, second) {
		t.Fatal("random salt", err)
	}
	if err := plist.Unmarshal(first, &hash); err != nil || len(hash.SALTEDSHA512PBKDF2.Salt) != 32 || len(hash.SALTEDSHA512PBKDF2.Entropy) != 128 {
		t.Fatal("sizes", err)
	}
	for _, iterations := range []int{0, -1} {
		if _, err := PasswordHash(nil, iterations); !errors.Is(err, ErrPasswordHash) {
			t.Fatal(err)
		}
	}
}
