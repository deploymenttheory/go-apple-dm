package ddm_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// TestSubjectPairRepetitionIsEnforced validates the cardinality Apple declares
// on the ACME and SCEP credential subject pair items: each pair holds exactly
// an OID and a value.
func TestSubjectPairRepetitionIsEnforced(t *testing.T) {
	t.Parallel()
	acme := func(pair ...string) *ddm.ACMECredential {
		return &ddm.ACMECredential{Subject: [][][]string{{pair}}}
	}
	scep := func(pair ...string) *ddm.SCEPCredential {
		return &ddm.SCEPCredential{Subject: [][][]string{{{"O", "Example"}, pair}}}
	}
	for _, c := range []struct {
		name    string
		payload interface{ Validate(support.Target) error }
		want    []string
	}{
		{"ACME pair", acme("CN", "a"), nil},
		{"ACME OID without a value", acme("CN"), []string{"Subject[0][0]"}},
		{"ACME pair with an extra value", acme("CN", "a", "b"), []string{"Subject[0][0]"}},
		{"SCEP pair", scep("CN", "a"), nil},
		{"SCEP OID without a value", scep("CN"), []string{"Subject[0][1]"}},
		{"SCEP pair with an extra value", scep("CN", "a", "b"), []string{"Subject[0][1]"}},
	} {
		var issues validation.Errors
		err := c.payload.Validate(support.Target{})
		if err != nil && !errors.As(err, &issues) {
			t.Fatalf("%s: unexpected error type: %v", c.name, err)
		}
		var got []string
		for _, issue := range issues {
			if issue.Rule == validation.RuleRepetition {
				got = append(got, issue.Path)
			}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: repetition failures at %q, want %q", c.name, got, c.want)
		}
	}
}
