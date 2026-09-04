//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
)

// E2E-025: a supervised Automated Device Enrollment device escrows a
// bootstrap token, then asks for its return-to-service configuration. The
// server agrees, and the response carries the escrowed token so the device can
// preserve its apps across the erasure.
//
// The token is the point of the scenario. Apple's rule is that without it the
// device erases fully and preserves nothing, and the policy below never
// mentions it: the service attaches the token it is already holding, so a
// deployment cannot lose app preservation by forgetting a field.
func TestE2E_ReturnToService(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{
		ReturnToService: func(context.Context, *mdm.Request, *checkin.ReturnToService) (*checkin.ReturnToServiceResponse, error) {
			return &checkin.ReturnToServiceResponse{
				ReturnToService: checkin.ReturnToServiceResponseReturnToService{Enabled: true},
			}, nil
		},
	})
	ctx := context.Background()
	const udid = "E2E-025"
	d := h.device(udid)
	if err := d.Enroll(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	token := []byte("bootstrap-token-for-app-preservation")
	if err := d.SetBootstrapToken(ctx, token); err != nil {
		t.Fatalf("SetBootstrapToken: %v", err)
	}

	resp, err := d.ReturnToService(ctx)
	if err != nil {
		t.Fatalf("ReturnToService: %v", err)
	}
	if !resp.ReturnToService.Enabled {
		t.Fatal("the server refused a return to service the policy allows")
	}
	if string(resp.ReturnToService.BootstrapToken) != string(token) {
		t.Errorf("BootstrapToken = %q, want the escrowed token: without it the device erases "+
			"fully and cannot preserve apps", resp.ReturnToService.BootstrapToken)
	}
}

// E2E-026: the same device against a server with no return-to-service policy
// is told not to erase. An unconfigured server must never be the reason a
// device wipes itself, so the absence of a handler is a refusal and not an
// error the device has to interpret.
func TestE2E_ReturnToServiceDisabledByDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	ctx := context.Background()
	d := h.device("E2E-026")
	if err := d.Enroll(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	resp, err := d.ReturnToService(ctx)
	if err != nil {
		t.Fatalf("ReturnToService: %v", err)
	}
	if resp.ReturnToService.Enabled {
		t.Error("a server with no return-to-service policy told the device to erase itself")
	}
}
