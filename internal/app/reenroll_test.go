package app_test

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/app"
	"github.com/deploymenttheory/go-apple-dm/internal/testpki"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/plist"
	"github.com/deploymenttheory/go-apple-dm/service"
)

// authenticateAs sends an Authenticate for udid signed by cert, the way a
// device presenting an identity certificate does.
func authenticateAs(t *testing.T, a *app.App, udid string, cert *x509.Certificate) error {
	t.Helper()
	raw, err := plist.Marshal(map[string]any{
		"MessageType": "Authenticate", "Topic": "com.apple.mgmt.t", "UDID": udid,
		"Model": "Mac", "ModelName": "MacBook", "DeviceName": "d", "SerialNumber": "S1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ck, err := mdm.DecodeCheckin(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Core.Checkin(
		context.Background(),
		&mdm.Request{Certificate: cert, ReceivedAt: time.Now()},
		ck,
	)
	return err
}

// TestReenrollDeniedByDefault holds the reference server to DenyReenroll. A
// certificate carries no binding to an enrollment id, so under AllowReenroll
// any certificate the CA issues replaces any enrollment's pin and inherits its
// bootstrap token and command queue.
func TestReenrollDeniedByDefault(t *testing.T) {
	t.Parallel()
	pki, err := testpki.NewCA("reenroll")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	device, err := pki.Issue("UDID-1", past)
	if err != nil {
		t.Fatal(err)
	}
	// A certificate from the same CA for an unrelated subject. No enrollment
	// pins it, so the certificate reuse policy has no history to match.
	intruder, err := pki.Issue("intruder", past)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Default", func(t *testing.T) {
		a := build(t, app.Config{Role: app.RoleMDM, Storage: "inmem"})
		if err := authenticateAs(t, a, "UDID-1", device.Cert); err != nil {
			t.Fatalf("initial enrollment: %v", err)
		}
		err := authenticateAs(t, a, "UDID-1", intruder.Cert)
		if !errors.Is(err, service.ErrReenrollDenied) {
			t.Fatalf("takeover was not refused: %v", err)
		}
		if service.CodeOf(err) != service.CodeForbidden {
			t.Fatalf("takeover code = %v, want forbidden", service.CodeOf(err))
		}
	})

	t.Run("OptIn", func(t *testing.T) {
		a := build(t, app.Config{Role: app.RoleMDM, Storage: "inmem", AllowReenroll: true})
		if err := authenticateAs(t, a, "UDID-1", device.Cert); err != nil {
			t.Fatalf("initial enrollment: %v", err)
		}
		if err := authenticateAs(t, a, "UDID-1", intruder.Cert); err != nil {
			t.Fatalf("MDM_ALLOW_REENROLL did not restore rotation: %v", err)
		}
	})
}
