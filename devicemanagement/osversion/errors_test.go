package osversion_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
)

// TestErrorsClassify pins the parse failure to its catalogued client condition, so an
// API caller receives a stable code for a version it can correct.
func TestErrorsClassify(t *testing.T) {
	_, err := osversion.Parse("not.a.version")
	if !errors.Is(err, osversion.ErrVersion) || !errors.Is(err, fault.InvalidArgument) {
		t.Fatal("parse failure did not classify", err)
	}
	if fault.CodeOf(err) != "DM-OSVERSION-MALFORMED" || fault.AudienceOf(err) != fault.Client {
		t.Fatal("code or audience", fault.CodeOf(err), fault.AudienceOf(err))
	}
}
