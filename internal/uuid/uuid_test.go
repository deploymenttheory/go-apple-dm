package uuid

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewV7(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	a, b := NewV7At(at), NewV7At(at.Add(time.Millisecond))
	if a.Version() != 7 || b.Version() != 7 {
		t.Fatalf("version = %d/%d", a.Version(), b.Version())
	}
	if a[8]&0xc0 != 0x80 {
		t.Fatal("variant bits")
	}
	if a.String() >= b.String() {
		t.Fatalf("v7 must sort by time: %s >= %s", a, b)
	}
	if a == b {
		t.Fatal("random tail should differ")
	}
	s := a.String()
	if len(s) != 36 || strings.ToUpper(s) != s || strings.Count(s, "-") != 4 {
		t.Fatalf("String = %q", s)
	}
	if NewV7().Version() != 7 {
		t.Fatal("NewV7")
	}
	parsed, err := Parse(strings.ToLower(s))
	if err != nil || parsed != a {
		t.Fatalf("Parse round trip: %v %v", parsed, err)
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "not-a-uuid", "0190A0F1-2B3C-7D4E-8F5A-6B7C8D9E0F1", "0190A0F1x2B3C-7D4E-8F5A-6B7C8D9E0F1A", "ZZZZZZZZ-2B3C-7D4E-8F5A-6B7C8D9E0F1A"} {
		if _, err := Parse(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}
