package axm

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDecodeWebhookNotification checks signature authentication, byte fidelity and malformed input.
func TestDecodeWebhookNotification(t *testing.T) {
	body := `{"data":{"id":"event-1","type":"orgDeviceAttributeChanged","version":1,"attributes":{"orgDeviceSerialNumber":"SERIAL","future":false},"relationships":{"instance":{"data":{"type":"orgDevices","id":"opaque-1"}}}},"future":12345678901234567890}`
	for _, tc := range []struct {
		name, body, secret string
		mutate             func(*testing.T, *httptest.ResponseRecorder)
		want               error
	}{
		{name: "valid", body: body, secret: "secret"},
		{name: "tampered", body: body + " ", secret: "secret", want: ErrWebhookSignature},
		{name: "wrong secret", body: body, secret: "wrong", want: ErrWebhookSignature},
		{name: "invalid JSON", body: "invalid", secret: "secret", want: ErrWebhookNotification},
		{name: "missing identity", body: `{"data":{"type":"webhookPingCreated","version":1}}`, secret: "secret", want: ErrWebhookNotification},
		{name: "missing version", body: `{"data":{"id":"ping","type":"webhookPingCreated"}}`, secret: "secret", want: ErrWebhookNotification},
		{name: "unknown type", body: `{"data":{"id":"future","type":"newType","version":2}}`, secret: "secret"},
		{name: "oversized", body: strings.Repeat("x", MaxWebhookNotificationBytes+1), secret: "secret", want: ErrWebhookNotification},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), "POST", "https://receiver.example/", strings.NewReader(tc.body))
			mac := hmac.New(sha256.New, []byte("secret"))
			signed := tc.body
			if tc.name == "tampered" {
				signed = body
			}
			_, _ = mac.Write([]byte(signed))
			req.Header.Set("x-apple-signature", "hmacsha256="+hex.EncodeToString(mac.Sum(nil)))
			n, err := DecodeWebhookNotification(req, []byte(tc.secret))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
			if err == nil && string(n.Raw) != tc.body {
				t.Fatal("lost original bytes")
			}
			if tc.name == "valid" && n.DeviceResourceID() != "opaque-1" {
				t.Fatal("used serial instead of opaque resource")
			}
		})
	}
	for _, header := range []string{"", "hmacsha256=invalid", "other=0000"} {
		req := httptest.NewRequestWithContext(t.Context(), "POST", "https://receiver.example/", strings.NewReader(body))
		req.Header.Set("x-apple-signature", header)
		if _, err := DecodeWebhookNotification(req, []byte("secret")); !errors.Is(err, ErrWebhookSignature) {
			t.Fatalf("header %q: %v", header, err)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), "POST", "https://receiver.example/", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	if _, err := DecodeWebhookNotification(req.WithContext(ctx), []byte("secret")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := DecodeWebhookNotification(nil, nil); !errors.Is(err, ErrWebhookSignature) {
		t.Fatal(err)
	}
	if (WebhookNotification{}).DeviceResourceID() != "" {
		t.Fatal("invented linkage")
	}
}
