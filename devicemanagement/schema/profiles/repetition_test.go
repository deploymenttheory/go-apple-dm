package profiles_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// repetitionPaths returns the wire paths of the repetition failures in err.
func repetitionPaths(t *testing.T, err error) []string {
	t.Helper()
	var issues validation.Errors
	if err != nil && !errors.As(err, &issues) {
		t.Fatalf("unexpected error type: %v", err)
	}
	var paths []string
	for _, issue := range issues {
		if issue.Rule == validation.RuleRepetition {
			paths = append(paths, issue.Path)
		}
	}
	return paths
}

// TestItemRepetitionIsEnforced validates the cardinality Apple declares on
// array item keys: each ACME and SCEP subject pair holds exactly an OID and a
// value, and a VPN PPP connection names exactly one EAP plug-in and one
// authentication protocol.
func TestItemRepetitionIsEnforced(t *testing.T) {
	t.Parallel()
	acme := func(pair ...string) *profiles.ACMECertificate {
		return &profiles.ACMECertificate{Subject: [][][]string{{{"O", "Example"}}, {pair}}}
	}
	scep := func(pair ...string) *profiles.SCEP {
		return &profiles.SCEP{PayloadContent: profiles.SCEPPayloadContent{Subject: [][][]string{{pair}}}}
	}
	ppp := func(plugins, protocols []string) *profiles.VPN {
		return &profiles.VPN{VPNType: "PPP", PPP: &profiles.VPNPPP{AuthEAPPlugins: plugins, AuthProtocol: protocols}}
	}
	for _, c := range []struct {
		name    string
		payload interface{ Validate(support.Target) error }
		want    []string
	}{
		{"ACME pair", acme("CN", "a"), nil},
		{"ACME OID without a value", acme("CN"), []string{"Subject[1][0]"}},
		{"ACME pair with an extra value", acme("CN", "a", "b"), []string{"Subject[1][0]"}},
		{"SCEP pair", scep("CN", "a"), nil},
		{"SCEP OID without a value", scep("CN"), []string{"PayloadContent.Subject[0][0]"}},
		{"SCEP pair with an extra value", scep("CN", "a", "b"), []string{"PayloadContent.Subject[0][0]"}},
		{"one EAP plug-in and protocol", ppp([]string{"EAP-RSA"}, []string{"EAP"}), nil},
		{"two EAP plug-ins", ppp([]string{"EAP-RSA", "EAP-RSA"}, []string{"EAP"}), []string{"PPP.AuthEAPPlugins"}},
		{"two authentication protocols", ppp([]string{"EAP-RSA"}, []string{"EAP", "EAP"}), []string{"PPP.AuthProtocol"}},
		{"no EAP plug-ins or protocols", ppp(nil, nil), nil},
	} {
		if got := repetitionPaths(t, c.payload.Validate(support.Target{})); !slices.Equal(got, c.want) {
			t.Errorf("%s: repetition failures at %q, want %q", c.name, got, c.want)
		}
	}
}
