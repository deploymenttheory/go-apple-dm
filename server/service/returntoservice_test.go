package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
)

func rtsResponse(t *testing.T, body []byte) checkin.ReturnToServiceResponse {
	t.Helper()
	var got checkin.ReturnToServiceResponse
	if err := plist.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode ReturnToService response: %v", err)
	}
	return got
}

// TestReturnToServiceWithoutAHandlerAnswersDisabled is the failing-closed
// case. A server that has not been configured for return to service must not
// be the reason a device erases itself, and Enabled false is a valid answer
// meaning exactly that: the device stays as it is.
func TestReturnToServiceWithoutAHandlerAnswersDisabled(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	enroll(t, h, "RTS-NOHANDLER")
	res, err := h.core.Checkin(context.Background(), req(h.cert), simple(t, "ReturnToService", "RTS-NOHANDLER", nil))
	if err != nil {
		t.Fatalf("ReturnToService with no handler: %v", err)
	}
	if got := rtsResponse(t, res.Body); got.ReturnToService.Enabled {
		t.Error("an unconfigured server answered Enabled true: a missing handler must never erase a device")
	}
}

// TestReturnToServiceAttachesTheStoredBootstrapToken is the reason this is not
// merely a passthrough handler. Apple's rule is that without the bootstrap
// token the device performs a full erasure and cannot preserve apps; the
// server is already holding the token the device sent in SetBootstrapToken, so
// a handler that forgets to attach it would silently downgrade every return to
// service.
func TestReturnToServiceAttachesTheStoredBootstrapToken(t *testing.T) {
	t.Parallel()
	const udid = "RTS-TOKEN"
	h := newHarness(t, service.Config{
		ReturnToService: func(context.Context, *mdm.Request, *checkin.ReturnToService) (*checkin.ReturnToServiceResponse, error) {
			// A policy that says "yes, erase and re-enrol" and says nothing
			// about the token, which is the realistic handler.
			return &checkin.ReturnToServiceResponse{
				ReturnToService: checkin.ReturnToServiceResponseReturnToService{Enabled: true},
			}, nil
		},
	})
	enroll(t, h, udid)
	ctx := context.Background()
	if _, err := h.core.Checkin(ctx, req(h.cert),
		simple(t, "SetBootstrapToken", udid, map[string]any{"BootstrapToken": []byte("bootstrap-secret")})); err != nil {
		t.Fatalf("SetBootstrapToken: %v", err)
	}
	res, err := h.core.Checkin(ctx, req(h.cert), simple(t, "ReturnToService", udid, nil))
	if err != nil {
		t.Fatalf("ReturnToService: %v", err)
	}
	got := rtsResponse(t, res.Body)
	if !got.ReturnToService.Enabled {
		t.Fatal("Enabled was not carried through from the handler")
	}
	if string(got.ReturnToService.BootstrapToken) != "bootstrap-secret" {
		t.Errorf("BootstrapToken = %q, want the stored token: without it the device erases fully "+
			"and cannot preserve apps", got.ReturnToService.BootstrapToken)
	}
}

// TestReturnToServiceKeepsAHandlerSuppliedToken proves the service fills the
// token in rather than overwriting one: a handler holding a token the server
// does not have, for a device enrolled elsewhere, must win.
func TestReturnToServiceKeepsAHandlerSuppliedToken(t *testing.T) {
	t.Parallel()
	const udid = "RTS-OWNTOKEN"
	h := newHarness(t, service.Config{
		ReturnToService: func(context.Context, *mdm.Request, *checkin.ReturnToService) (*checkin.ReturnToServiceResponse, error) {
			return &checkin.ReturnToServiceResponse{
				ReturnToService: checkin.ReturnToServiceResponseReturnToService{
					Enabled: true, BootstrapToken: []byte("handler-token"),
				},
			}, nil
		},
	})
	enroll(t, h, udid)
	ctx := context.Background()
	if _, err := h.core.Checkin(ctx, req(h.cert),
		simple(t, "SetBootstrapToken", udid, map[string]any{"BootstrapToken": []byte("stored-token")})); err != nil {
		t.Fatalf("SetBootstrapToken: %v", err)
	}
	res, err := h.core.Checkin(ctx, req(h.cert), simple(t, "ReturnToService", udid, nil))
	if err != nil {
		t.Fatalf("ReturnToService: %v", err)
	}
	if got := rtsResponse(t, res.Body); string(got.ReturnToService.BootstrapToken) != "handler-token" {
		t.Errorf("BootstrapToken = %q, want the handler's own token", got.ReturnToService.BootstrapToken)
	}
}

// TestReturnToServiceEnabledWithNoStoredToken covers the degraded path Apple
// describes: the device erases fully and preserves nothing. It must still be
// answered rather than refused, because refusing leaves the device waiting.
func TestReturnToServiceEnabledWithNoStoredToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{
		ReturnToService: func(context.Context, *mdm.Request, *checkin.ReturnToService) (*checkin.ReturnToServiceResponse, error) {
			return &checkin.ReturnToServiceResponse{
				ReturnToService: checkin.ReturnToServiceResponseReturnToService{Enabled: true},
			}, nil
		},
	})
	enroll(t, h, "RTS-NOTOKEN")
	res, err := h.core.Checkin(context.Background(), req(h.cert), simple(t, "ReturnToService", "RTS-NOTOKEN", nil))
	if err != nil {
		t.Fatalf("ReturnToService with no stored token: %v", err)
	}
	got := rtsResponse(t, res.Body)
	if !got.ReturnToService.Enabled {
		t.Error("Enabled should still be true; the device erases fully without app preservation")
	}
	if len(got.ReturnToService.BootstrapToken) != 0 {
		t.Errorf("BootstrapToken = %q, want none", got.ReturnToService.BootstrapToken)
	}
}

// TestReturnToServiceHandlerFailureIsReported is the failing path: a policy
// that cannot decide must not be reported to the device as "do not erase",
// because that is indistinguishable from a deliberate refusal.
func TestReturnToServiceHandlerFailureIsReported(t *testing.T) {
	t.Parallel()
	boom := errors.New("policy unavailable")
	h := newHarness(t, service.Config{
		ReturnToService: func(context.Context, *mdm.Request, *checkin.ReturnToService) (*checkin.ReturnToServiceResponse, error) {
			return nil, boom
		},
	})
	enroll(t, h, "RTS-FAIL")
	if _, err := h.core.Checkin(context.Background(), req(h.cert), simple(t, "ReturnToService", "RTS-FAIL", nil)); !errors.Is(err, boom) {
		t.Fatalf("handler failure = %v, want it surfaced", err)
	}
}

// TestReturnToServiceRequiresAKnownEnrollment keeps the message behind the
// same certificate pinning as every other check-in after Authenticate: it
// hands out a bootstrap token, so it must not answer a stranger.
func TestReturnToServiceRequiresAKnownEnrollment(t *testing.T) {
	t.Parallel()
	h := newHarness(t, service.Config{})
	_, err := h.core.Checkin(context.Background(), req(h.cert), simple(t, "ReturnToService", "RTS-UNKNOWN", nil))
	if service.CodeOf(err) != service.CodeUnknownEnrollment {
		t.Fatalf("unknown enrollment = %v (code %v), want CodeUnknownEnrollment", err, service.CodeOf(err))
	}
}
