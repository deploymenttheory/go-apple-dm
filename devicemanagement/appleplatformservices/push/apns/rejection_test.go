package apns_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push/apns"
)

func TestAppInvalidAlertRejectedBeforeCredentialLookup(t *testing.T) {
	t.Parallel()
	client := apns.NewApp(nil)
	defer client.Close()
	for _, tc := range []struct {
		name, payload string
		priority      int
	}{
		{"invalid JSON", `{"aps":`, 10},
		{"empty alert", `{"aps":{}}`, 10},
		{"invalid priority", `{"aps":{"alert":"hello"}}`, 7},
		{"numeric overflow", `{"aps":{"badge":1e999}}`, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := client.Send(
				t.Context(),
				apns.AppRequest{
					Topic:    "com.example.app",
					Token:    []byte{1},
					PushType: "alert",
					Payload:  []byte(tc.payload),
					Priority: tc.priority,
				},
			)
			if result.Outcome != push.OutcomeRejected || !errors.Is(result.Err, apns.ErrRequest) {
				t.Fatalf("invalid notification reached provider: %+v", result)
			}
		})
	}
}
