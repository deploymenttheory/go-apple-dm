package activationlock

import (
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestAppleBypassVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Raw  string `json:"raw"`
		Code string `json:"code"`
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		raw, _ := hex.DecodeString(v.Raw)
		if encode([16]byte(raw)) != v.Code {
			t.Fatal("Apple encoding differs")
		}
		if got, err := Hash(v.Code); err != nil || got != v.Hash {
			t.Fatal("independent hash differs", err)
		}
	}
	for _, code := range []string{"", strings.Repeat("Z", 31), "ZZZZZ-ZZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZ8", "00000_00000-0000-0000-0000-0000", "00000-00000-0000-0000-0000-000B"} {
		if _, err := Hash(code); !errors.Is(err, ErrCode) {
			t.Fatal("invalid accepted")
		}
	}
	code, hash := Generate()
	again, _ := Generate()
	got, err := Hash(code)
	if err != nil || got != hash || code == again {
		t.Fatal("generation", err)
	}
}
