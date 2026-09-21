package ddm_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// TestTypedValidationCollectsRules checks typed validation collects rules.
func TestTypedValidationCollectsRules(t *testing.T) {
	t.Parallel()
	payload := &ddm.ContentCaching{AllowPersonalCaching: new(false), AllowSharedCaching: new(false), ManagementSecurityConfig: new("specificServerCert")}
	var issues validation.Errors
	if !errors.As(payload.Validate(support.Target{}), &issues) || len(issues) != 2 {
		t.Fatalf("expected both field relationship failures: %v", issues)
	}
	var nilPayload *ddm.AppManaged
	if !errors.Is(nilPayload.Validate(support.Target{}), validation.ErrValidation) {
		t.Fatal("nil declaration accepted")
	}
}
