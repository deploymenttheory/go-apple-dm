package schemagen

import (
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode"
)

// regexSample derives a short string matching an RE2 pattern, for
// generated conformance samples of format-constrained keys. It returns
// false when the pattern cannot be parsed or the derived string does not
// match (for example patterns with lookaround-like constructs).
func regexSample(pattern string) (string, bool) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", false
	}
	s := genRegex(re.Simplify())
	compiled, err := regexp.Compile(pattern)
	if err != nil || !compiled.MatchString(s) {
		return "", false
	}
	return s, true
}

func genRegex(re *syntax.Regexp) string {
	switch re.Op {
	case syntax.OpLiteral:
		return string(re.Rune)
	case syntax.OpCharClass:
		return string(pickRune(re.Rune))
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return "a"
	case syntax.OpCapture:
		return genRegex(re.Sub[0])
	case syntax.OpStar, syntax.OpQuest:
		return ""
	case syntax.OpPlus:
		return genRegex(re.Sub[0])
	case syntax.OpRepeat:
		one := genRegex(re.Sub[0])
		return strings.Repeat(one, re.Min)
	case syntax.OpConcat:
		var sb strings.Builder
		for _, s := range re.Sub {
			sb.WriteString(genRegex(s))
		}
		return sb.String()
	case syntax.OpAlternate:
		return genRegex(re.Sub[0])
	case syntax.OpEmptyMatch,
		syntax.OpBeginLine,
		syntax.OpEndLine,
		syntax.OpBeginText,
		syntax.OpEndText,
		syntax.OpWordBoundary,
		syntax.OpNoWordBoundary,
		syntax.OpNoMatch:
		return ""
	}
	return ""
}

// pickRune chooses a readable rune from a character class given as
// inclusive range pairs, preferring ASCII letters and digits.
func pickRune(ranges []rune) rune {
	for i := 0; i+1 < len(ranges); i += 2 {
		for r := ranges[i]; r <= ranges[i+1] && r < 128; r++ {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
		}
	}
	if len(ranges) > 0 {
		return ranges[0]
	}
	return 'a'
}
