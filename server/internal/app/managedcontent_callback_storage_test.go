package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// TestManagedContentCallbackReceiverStorageFailure verifies that an unavailable
// receiver credential cannot acknowledge or retain a callback. A healthy retry
// of the same notification must commit both its receipt and access occurrence.
func TestManagedContentCallbackReceiverStorageFailure(t *testing.T) {
	for _, scenario := range []string{"missing-receiver", "update-unavailable", "credential-read-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			f := contentFixtureService(t)
			a, token := contentBuild(t, contentConfig(t, f))
			location := registerContent(t, a, token)
			contentFailureReply(t, a, token, "POST", "/appsbooks/locations/london/refresh", map[string]any{"revision": location.Revision, "maxPages": 2}, http.StatusOK)
			const receiver = "private-receiver-secret"
			if scenario != "missing-receiver" {
				contentFailureReply(t, a, token, "PUT", "/appsbooks/locations/london/receiver", map[string]any{"token": receiver}, http.StatusOK)
			}
			original := a.protocol
			calls := 0
			if scenario != "missing-receiver" {
				method := "update"
				if scenario == "credential-read-unavailable" {
					method = "tx-get"
				}
				a.protocol = contentNativeStateFailure{Store: original, fail: func(operation, key string) error {
					if operation == method && key == receiverKey("london") {
						calls++
						return errors.New("private-storage-provider-secret")
					}
					return nil
				}}
			}
			notify := func() *httptest.ResponseRecorder {
				t.Helper()
				body := fmt.Sprintf(`{"notificationId":"storage-retry","notificationType":"TEST_NOTIFICATION","uId":%q,"notification":{}}`, f.uid)
				r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/appsbooks/notifications/london", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+receiver)
				w := httptest.NewRecorder()
				a.Handler.ServeHTTP(w, r)
				return w
			}
			before := f.requests.Load()
			response := notify()
			want := http.StatusServiceUnavailable
			if scenario == "missing-receiver" {
				want = http.StatusUnauthorized
			} else if calls != 1 {
				t.Fatal("receiver storage fault did not fire exactly once", calls)
			}
			if response.Code != want || strings.Contains(response.Body.String(), "private-") {
				t.Fatal("receiver storage outage acknowledged or exposed credentials", response.Code, response.Body.String())
			}
			a.protocol = original
			if _, err := a.AppsBooks.LoadNotification(t.Context(), "london", "storage-retry"); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("failed receiver authentication retained a notification", err)
			}
			var captured int
			if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_records WHERE type = ?", "appsbooks-notification").Scan(&captured); err != nil || captured != 0 {
				t.Fatal("failed receiver authentication retained an access occurrence", captured, err)
			}
			if scenario == "missing-receiver" {
				contentFailureReply(t, a, token, "PUT", "/appsbooks/locations/london/receiver", map[string]any{"token": receiver}, http.StatusOK)
			}
			if response = notify(); response.Code != http.StatusNoContent {
				t.Fatal("healthy same-notification retry failed", response.Code, response.Body.String())
			}
			if _, err := a.AppsBooks.LoadNotification(t.Context(), "london", "storage-retry"); err != nil {
				t.Fatal("successful callback missing its durable receipt", err)
			}
			if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_records WHERE type = ?", "appsbooks-notification").Scan(&captured); err != nil || captured != 1 || f.requests.Load() != before {
				t.Fatal("callback did not commit exactly one occurrence locally", captured, err)
			}
		})
	}
}
