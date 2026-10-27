package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	ddminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
)

// TestRespondAssignmentWithoutVerdict checks that an assignment stands when
// its verdict cannot be produced, and that a failed assignment reports the
// failure rather than a verdict.
func TestRespondAssignmentWithoutVerdict(t *testing.T) {
	boom := errors.New("boom")
	engine, err := ddm.New(ddm.Config{
		Store: ddminmem.New(),
		EnrollmentTarget: func(context.Context, mdm.EnrollmentID) (support.Target, error) {
			return support.Target{}, boom
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Engine: engine, cfg: Config{Logger: slog.New(slog.DiscardHandler)}}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	put := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/", nil)

	w := httptest.NewRecorder()
	a.respondAssignment(w, put, id, "set", true, nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"Changed":true}` {
		t.Fatalf("verdict failure = %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	a.respondAssignment(w, put, id, "set", false, ddm.ErrNotFound)
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "Verdict") {
		t.Fatalf("assignment failure = %d %s", w.Code, w.Body)
	}
}
