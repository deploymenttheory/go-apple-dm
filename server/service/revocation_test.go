package service_test

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

func TestCertificateStatusPrecedesAllSideEffectsAndPinModes(t *testing.T) {
	for _, pin := range []service.PinMode{service.PinEnforce, service.PinWarn, service.PinOff} {
		t.Run(string(rune('0'+pin)), func(t *testing.T) {
			h := newHarness(
				t,
				service.Config{
					Pinning:           pin,
					CertificateStatus: func(context.Context, *x509.Certificate) error { return revocation.ErrRevoked },
				},
			)
			for _, ck := range []*mdm.Checkin{authenticate(t, "D"), tokenUpdate(t, "D", nil), checkinPlist(t, map[string]any{"MessageType": "DeclarativeManagement", "UDID": "D", "Endpoint": "tokens"})} {
				if _, err := h.core.Checkin(
					t.Context(),
					req(h.cert),
					ck,
				); !errors.Is(err, revocation.ErrRevoked) ||
					service.CodeOf(err) != service.CodeForbidden {
					t.Fatal(err)
				}
			}
			if _, err := h.core.Connect(
				t.Context(),
				req(h.cert),
				&mdm.Response{ID: deviceID("D"), Status: mdm.StatusIdle},
			); !errors.Is(
				err,
				revocation.ErrRevoked,
			) {
				t.Fatal(err)
			}
			if len(h.events) != 4 {
				t.Fatalf("rejections missing: %v", h.events)
			}
			for _, e := range h.events {
				if e.Type != event.CertificateStatusRejected || e.Data != nil {
					t.Fatal("revoked request emitted mutation data", e)
				}
			}
			if _, err := h.store.Get(
				t.Context(),
				deviceID("D"),
			); !errors.Is(
				err,
				storage.ErrNotFound,
			) {
				t.Fatal("revoked Authenticate changed storage", err)
			}
		})
	}
}

type failedCompletion struct{}

func (failedCompletion) Before(ctx context.Context, _ *service.Call) (context.Context, error) {
	return ctx, nil
}
func (failedCompletion) After(context.Context, *service.Call, error) {}
func (failedCompletion) Complete(context.Context, *service.Call) error {
	return errors.New("association confirmation unavailable")
}

func TestCompletionFailureCannotReportSuccessfulAuthenticate(t *testing.T) {
	h := newHarness(t, service.Config{Hooks: []service.Hook{failedCompletion{}}})
	if _, err := h.core.Checkin(t.Context(), req(h.cert), authenticate(t, "D")); err == nil || service.CodeOf(err) != service.CodeInternal {
		t.Fatal(err)
	}
}
