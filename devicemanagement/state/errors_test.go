package state_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// TestErrorsClassify pins each sentinel to its kind, code and audience.
func TestErrorsClassify(t *testing.T) {
	if !errors.Is(state.ErrNotFound, fault.NotFound) || fault.CodeOf(state.ErrNotFound) != "DM-RECORD-NOT-FOUND" {
		t.Fatal("not-found lost its classification")
	}
	if fault.AudienceOf(state.ErrNotFound) != fault.Client {
		t.Fatal("a shared record condition is a client condition")
	}
	if !errors.Is(state.ErrInvalid, fault.Internal) || fault.CodeOf(state.ErrInvalid) != "" || fault.AudienceOf(state.ErrInvalid) != fault.Operator {
		t.Fatal("invalid lost its classification")
	}
}
