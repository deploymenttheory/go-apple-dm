package axm

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// TestResponseStatusAndRepresentation checks response status and representation.
func TestResponseStatusAndRepresentation(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := stub(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "https://example.invalid/redirect")
				w.WriteHeader(status)
			})
			c := stubClient(t, srv, nil)
			_, readErr := c.ListOrgDevices(t.Context(), ListOptions{})
			writeErr := c.DeleteMDMServer(t.Context(), "server")
			for _, err := range []error{readErr, writeErr} {
				var apiErr *Error
				if !errors.As(err, &apiErr) || apiErr.Status != status {
					t.Fatalf("redirect status lost: %v", err)
				}
			}
		})
	}
	for _, status := range []int{200, 204} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := stub(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
			c := stubClient(t, srv, nil)
			if _, err := c.ListOrgDevices(t.Context(), ListOptions{}); !errors.Is(err, ErrDecode) {
				t.Fatalf("empty representation accepted: %v", err)
			}
			if err := c.DeleteMDMServer(t.Context(), "server"); err != nil {
				t.Fatalf("bodyless mutation rejected: %v", err)
			}
		})
	}
}

// TestRegressionRedirectReportedAsSuccess checks that a rejected redirect is returned as an error
// rather than successful empty data.
func TestRegressionRedirectReportedAsSuccess(t *testing.T) {
	srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://example.invalid/redirect")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	c := stubClient(t, srv, nil)
	result, err := c.ListOrgDevices(t.Context(), ListOptions{})
	t.Logf("307 response produced result=%v error=%v", result, err)
	if err == nil {
		t.Fatal("rejected redirect is reported as successful empty data")
	}
}
