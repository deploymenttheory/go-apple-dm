package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
)

// guidanceApp is an app whose setup configuration names the default identities.
func guidanceApp() *App {
	return &App{cfg: Config{Setup: &SetupConfig{
		Role:      "combined",
		VendorID:  string(lifecycle.VendorSigning),
		PushID:    string(lifecycle.MDMPush),
		HTTPSID:   string(lifecycle.ServerHTTPS),
		HTTPSCAID: string(lifecycle.ServerHTTPS) + "-ca",
		IssuerID:  string(lifecycle.EnrollmentCA),
	}}}
}

// TestExplainSetupOperationNamesWhatToRun checks that an operation on an identity that was
// never created says how to create it, and that other errors are left alone.
func TestExplainSetupOperationNamesWhatToRun(t *testing.T) {
	a := guidanceApp()
	for _, tc := range []struct {
		kind             lifecycle.Kind
		operation, wants string
	}{
		{lifecycle.MDMPush, "sign", "dmctl setup mdm-push request"},
		{lifecycle.VendorSigning, "import", "dmctl setup vendor-signing request"},
		{lifecycle.EnrollmentCA, "activate", "dmctl setup enrollment-ca create"},
		{lifecycle.ServerHTTPS, "renew", "dmctl setup server-https lab"},
	} {
		err := a.ExplainSetupOperation(tc.kind, tc.operation, "", lifecycle.ErrNotFound)
		if !errors.Is(err, lifecycle.ErrNotFound) {
			t.Fatalf("%s %s: lost the sentinel: %v", tc.kind, tc.operation, err)
		}
		if !strings.Contains(err.Error(), tc.wants) || !strings.Contains(err.Error(), string(tc.kind)) {
			t.Fatalf("%s %s: %v", tc.kind, tc.operation, err)
		}
	}

	// An operation that creates the identity expects it to be missing.
	for _, operation := range []string{"request", "create", "lab", "acme", "adopt"} {
		if err := a.ExplainSetupOperation(lifecycle.MDMPush, operation, "", lifecycle.ErrNotFound); !errors.Is(err, lifecycle.ErrNotFound) ||
			strings.Contains(err.Error(), "create it with") {
			t.Fatalf("%s was given creation guidance: %v", operation, err)
		}
	}
	// Errors about the operation itself keep their own diagnosis.
	invalid := a.ExplainSetupOperation(lifecycle.MDMPush, "sign", "", lifecycle.ErrInvalid)
	if !errors.Is(invalid, lifecycle.ErrInvalid) || invalid.Error() != lifecycle.ErrInvalid.Error() {
		t.Fatal("an operation error was rewritten:", invalid)
	}
	if a.ExplainSetupOperation(lifecycle.MDMPush, "sign", "", nil) != nil {
		t.Fatal("success was turned into an error")
	}
	// An explicit identity is named instead of the configured default.
	if err := a.ExplainSetupOperation(lifecycle.MDMPush, "sign", "adopted", lifecycle.ErrNotFound); !strings.Contains(err.Error(), `"adopted"`) {
		t.Fatal(err)
	}
}

// TestExplainIdentityListsConfiguredIdentities checks the message for an unknown workflow ID.
func TestExplainIdentityListsConfiguredIdentities(t *testing.T) {
	a := guidanceApp()
	err := a.ExplainIdentity("typo", lifecycle.ErrNotFound)
	if !errors.Is(err, lifecycle.ErrNotFound) {
		t.Fatal(err)
	}
	for _, want := range []string{`"typo"`, "vendor-signing", "mdm-push", "server-https", "server-https-ca", "enrollment-ca"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message lacks %q: %v", want, err)
		}
	}
	other := errors.New("storage unavailable")
	if passed := a.ExplainIdentity("typo", other); !errors.Is(passed, other) ||
		passed.Error() != other.Error() {
		t.Fatal("an unrelated error was rewritten:", passed)
	}
}

// TestUnsupportedOperationNamesWhatTheKindAccepts checks the wrong-verb message.
func TestUnsupportedOperationNamesWhatTheKindAccepts(t *testing.T) {
	err := unsupportedOperation(lifecycle.MDMPush, "create")
	if !errors.Is(err, lifecycle.ErrInvalid) {
		t.Fatal(err)
	}
	for _, want := range []string{"mdm-push", `"create"`, "request", "sign"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "rollover") {
		t.Fatal("offered an operation this kind does not accept")
	}
	// Every kind the switch dispatches on has a published operation set.
	for _, kind := range []lifecycle.Kind{
		lifecycle.VendorSigning, lifecycle.MDMPush, lifecycle.ServerHTTPS, lifecycle.EnrollmentCA,
	} {
		if len(operationsByKind[kind]) == 0 {
			t.Fatalf("%s has no published operations", kind)
		}
	}
	if err = unsupportedOperation("invented", "request"); !errors.Is(err, lifecycle.ErrInvalid) ||
		!strings.Contains(err.Error(), "unsupported setup operation") {
		t.Fatal(err)
	}
}
