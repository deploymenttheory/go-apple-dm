package schemagen

import (
	"regexp"
	"strings"
)

var (
	proseBullet       = regexp.MustCompile(`(?m)^\s*[-*]\s+`)
	proseEscape       = regexp.MustCompile(`\\([*_-])`)
	proseRequirement  = regexp.MustCompile("Required by the (`[^`]+`) service type\\.")
	proseAvailability = regexp.MustCompile(
		`(?:Available in|This value is available in) (?:iOS|macOS|tvOS|visionOS|watchOS) [^\n]*?later\.`,
	)
)

// Only verified equivalent edits are suppressed. Unknown prose changes remain
// review evidence, including requirements documented outside structured fields.
func normalizeProse(text string, unchangedAvailability bool) string {
	text = proseBullet.ReplaceAllString(text, " ")
	text = proseEscape.ReplaceAllString(text, "$1")
	text = proseRequirement.ReplaceAllString(text, "The $1 service type requires this key.")
	text = strings.NewReplacer("isn't", "is not", "doesn't", "does not", "don't", "do not", "can't", "cannot").
		Replace(text)
	text = strings.NewReplacer(
		"Required when ", "The device requires this key when ",
		"This is required when the device ", "The device requires this when it ",
		"This key is required if the device's ", "The device requires this key if its ",
	).Replace(text)
	if unchangedAvailability {
		text = proseAvailability.ReplaceAllString(text, "")
	}
	return strings.Join(strings.Fields(text), " ")
}

func substantiveProse(before, after auditDocument) []Evidence {
	changes := []Evidence{}
	for _, field := range compareFields(before.prose, after.prose) {
		availabilityUnchanged := unchangedProseAvailability(before, after, field.Path)
		if normalizeProse(
			field.Before,
			availabilityUnchanged,
		) != normalizeProse(
			field.After,
			availabilityUnchanged,
		) {
			changes = append(changes, field)
		}
	}
	return changes
}

func unchangedProseAvailability(before, after auditDocument, prosePath string) bool {
	owner := strings.TrimSuffix(strings.TrimSuffix(prosePath, ".content"), ".description")
	for _, field := range compareFields(before.fields, after.fields) {
		if !strings.Contains(field.Path, ".supportedOS") {
			continue
		}
		supportOwner := strings.Split(field.Path, ".supportedOS")[0]
		if supportOwner == "payload" || owner == supportOwner ||
			strings.HasPrefix(owner, supportOwner+".") ||
			strings.HasPrefix(prosePath, "notes") {
			return false
		}
	}
	return true
}

func fieldContext(prose map[string]string, field string) string {
	for field != "" {
		if content := prose[field+".content"]; content != "" {
			return content
		}
		index := strings.LastIndex(field, ".")
		if index < 0 {
			break
		}
		field = field[:index]
	}
	return ""
}

func (t *auditTree) enrichEvidence(key, file string, field Evidence, context string) {
	f := t.findings[key]
	if f == nil {
		return
	}
	for i := range f.Evidence {
		e := &f.Evidence[i]
		if e.Path == file &&
			(e.Detail == field.Detail || e.Detail == field.Path+": "+field.Detail) {
			e.Before, e.After, e.Context = field.Before, field.After, context
		}
	}
}
