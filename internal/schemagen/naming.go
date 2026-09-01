package schemagen

import (
	"strings"
	"unicode"
)

// Naming contract (decision record 0003).
//
// A Go identifier is derived from an Apple key or title by splitting on every
// character that is not a letter or digit, then joining the segments with the
// first letter of each segment upper-cased and the rest preserved. Apple's own
// capitalisation therefore survives: UDID stays UDID, OSUpdate stays OSUpdate,
// eSIM becomes ESIM. Identifiers that would start with a digit are prefixed
// with X. Go keywords and predeclared identifiers are suffixed with an
// underscore. The rules are deliberately simple so a schema refresh cannot
// change an existing name; schema/NAMES.lock guards against that.

// goKeywords are Go keywords plus predeclared identifiers that would be
// confusing as type or field names.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// GoName converts an Apple key, title, or type identifier into an exported
// Go identifier according to the naming contract.
func GoName(s string) string {
	var b strings.Builder
	upperNext := true
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upperNext = true
			continue
		}
		if upperNext {
			b.WriteRune(unicode.ToUpper(r))
			upperNext = false
			continue
		}
		b.WriteRune(r)
	}
	name := b.String()
	if name == "" {
		return "Empty"
	}
	if unicode.IsDigit(rune(name[0])) {
		name = "X" + name
	}
	if goKeywords[name] {
		name += "_"
	}
	return name
}

// TypeNameForSchema derives the top-level Go type name for a schema.
//
// Commands and check-in messages use their wire RequestType so the Go name
// matches what appears on the wire (DeviceLock, TokenUpdate). Everything else
// uses Apple's title with family prefixes stripped ("Status Device Model
// Family" becomes DeviceModelFamily, "Error Unrecognized Device" becomes
// UnrecognizedDevice). Overrides in naming_overrides.go take precedence.
func TypeNameForSchema(s *Schema) string {
	if n, ok := typeNameOverrides[s.Path]; ok {
		return n
	}
	switch s.Family {
	case FamilyCommands, FamilyCheckin:
		if s.Payload.RequestType != "" {
			return GoName(s.Payload.RequestType)
		}
	case FamilyStatus:
		return GoName(strings.TrimPrefix(s.Title, "Status "))
	case FamilyErrors:
		return GoName(strings.TrimPrefix(s.Title, "Error "))
	case FamilyDDMProto, FamilyOther, FamilyProfiles, FamilyDDM, familyUnknown:
	}
	return GoName(s.Title)
}

// ResponseTypeName is the Go name of a command's response type.
func ResponseTypeName(requestName string) string { return requestName + "Response" }
