package schemagen

import (
	"strings"
	"testing"
)

func TestAuditProseKeepsLateRequirementsAndFiltersVerifiedEditorialChanges(t *testing.T) {
	t.Parallel()
	common := strings.Repeat("Existing token description. ", 100)
	old := auditSchema + "notes:\n- title: Token\n  content: " + common + "Sign the JWT.\n- title: Other\n  content: The report is not incremental.\n"
	next := strings.Replace(
		old,
		"Sign the JWT.",
		"Sign the JWT. The signing algorithm must be RS256.",
		1,
	)
	next = strings.Replace(next, "is not", "isn't", 1)
	// A structural change must not hide an independently changed prose rule.
	next = strings.Replace(next, "type: <string>", "type: <boolean>", 1)
	before := auditFixture(t, map[string]string{"mdm/checkin/gettoken.yaml": old})
	after := auditFixture(t, map[string]string{"mdm/checkin/gettoken.yaml": next})
	report, err := Audit(before, after, "seed")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range report.Findings {
		if f.Key == "behavior-review:protocol-wording" {
			found = true
			if len(f.Evidence) != 1 || !strings.HasSuffix(f.Evidence[0].After, "RS256.") ||
				f.Evidence[0].Before == "" {
				t.Fatalf("requirement evidence: %+v", f)
			}
		}
	}
	if !found {
		t.Fatal("late RS256 requirement lost")
	}
}

func TestAuditEditorialNormalizationIsConservative(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{
		{"The system is not ready.", "The system isn't ready."},
		{"- `tokens`: token data\n- status", "* `tokens`: token data\n* status"},
		{"The topic is `com.apple.mgmt.*`.", "The topic is `com.apple.mgmt.\\*`."},
		{"Required by the `com.apple.watch.pairing` service type.", "The `com.apple.watch.pairing` service type requires this key."},
		{"An ID. Available in iOS 13 and later, macOS 10.15 and later, and visionOS 2 and later.", "An ID."},
	} {
		if normalizeProse(pair[0], true) != normalizeProse(pair[1], true) {
			t.Fatalf("editorial change retained: %q", pair)
		}
	}
	if normalizeProse(
		"Signing must use RS256.",
		true,
	) == normalizeProse(
		"Signing may use RS256.",
		true,
	) {
		t.Fatal("obligation change suppressed")
	}
	before := auditDocument{
		fields: map[string]string{"payload.supportedOS.iOS.introduced": "26.0"},
		prose:  map[string]string{"description": "Available in iOS 26 and later."},
	}
	after := auditDocument{
		fields: map[string]string{"payload.supportedOS.iOS.introduced": "27.0"},
		prose:  map[string]string{"description": ""},
	}
	if len(substantiveProse(before, after)) != 1 {
		t.Fatal("availability prose was suppressed despite changed structured support")
	}
}

func TestAuditFieldMeaningDoesNotChangeStructuralFingerprint(t *testing.T) {
	t.Parallel()
	before := auditFixture(t, map[string]string{"mdm/checkin/message.yaml": auditSchema})
	var previous string
	for _, meaning := range []string{"First description.", "Second description."} {
		after := auditFixture(
			t,
			map[string]string{
				"mdm/checkin/message.yaml": strings.Replace(
					auditSchema,
					"type: <string>",
					"type: <boolean>\n  content: "+meaning,
					1,
				),
			},
		)
		report, err := Audit(before, after, "seed")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range report.Findings {
			if f.Key != "behavior-review:protocol:mdm:requesttype:Example" {
				continue
			}
			if previous != "" && previous != f.Fingerprint {
				t.Fatal("presentation context changed structural identity")
			}
			previous = f.Fingerprint
			if len(f.Evidence) != 1 || f.Evidence[0].Context != meaning ||
				f.Evidence[0].After != "<boolean>" {
				t.Fatalf("field facts missing: %+v", f)
			}
		}
	}
	if previous == "" {
		t.Fatal("structural finding missing")
	}
	if fieldContext(nil, "unknown") != "" {
		t.Fatal("invented field context")
	}
	tree := &auditTree{findings: map[string]*Finding{}}
	tree.enrichEvidence("missing", "missing", Evidence{}, "")
}
