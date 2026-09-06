package schemagen

import (
	"regexp"
	"testing"
)

func TestRegexSample(t *testing.T) {
	t.Parallel()
	patterns := []string{
		`^([0-9A-Fa-f]{2}:){5}([0-9A-Fa-f]{2})$`,
		`^([0-9A-Za-z]{6})|([0-9A-Za-z]{9})$`,
		`^[0-9A-Za-z]{8}-[0-9A-Za-z]{4}-[0-9A-Za-z]{4}-[0-9A-Za-z]{4}-[0-9A-Za-z]{12}$`,
		`^[0-9]{6}$`,
		`^https://.*$`,
		`^a+b?c*$`,
		`^(x|y)\bz?$`,
		`[^a]`,
		``,
	}
	for _, p := range patterns {
		s, ok := regexSample(p)
		if !ok {
			t.Errorf("no sample for %q", p)
			continue
		}
		if !regexp.MustCompile(p).MatchString(s) {
			t.Errorf("sample %q does not match %q", s, p)
		}
	}
	if _, ok := regexSample(`(`); ok {
		t.Error("expected failure for unparsable pattern")
	}
	if pickRune(nil) != 'a' || pickRune([]rune{0x1F600, 0x1F600}) != 0x1F600 {
		t.Error("pickRune fallbacks")
	}
}
