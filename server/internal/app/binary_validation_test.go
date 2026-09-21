package app

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	ddminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
)

// TestBinaryValidationAdminTransactions checks binary validation admin transactions.
func TestBinaryValidationAdminTransactions(t *testing.T) {
	for _, tc := range []struct {
		name, list, rule string
		allowed          bool
	}{
		{"allow-empty", "AllowedBinaries", `{}`, false},
		{"allow-path", "AllowedBinaries", `{"PathPrefix":"/Applications/Fixture.app"}`, false},
		{"allow-state", "AllowedBinaries", `{"SigningState":"DeveloperID"}`, false},
		{"allow-signing-id", "AllowedBinaries", `{"SigningID":"com.example.fixture"}`, false},
		{"allow-empty-hash", "AllowedBinaries", `{"CDHash":"","PathPrefix":"/Applications/Fixture.app"}`, false},
		{"allow-empty-team", "AllowedBinaries", `{"TeamID":"","SigningState":"All"}`, false},
		{"deny-path", "DeniedBinaries", `{"PathPrefix":"/Applications/Fixture.app"}`, false},
		{"deny-empty-id", "DeniedBinaries", `{"SigningID":""}`, false},
		{"allow-hash", "AllowedBinaries", `{"CDHash":"90bc96cd95be55c12e7d9b1611cbc677610bb70c","PathPrefix":"/Applications/Fixture.app","SigningState":"All"}`, true},
		{"allow-team", "AllowedBinaries", `{"TeamID":"EXAMPLE1234","SigningID":"com.example.fixture"}`, true},
		{"deny-signing-id", "DeniedBinaries", `{"SigningID":"com.example.fixture","PathPrefix":"/Applications/Fixture.app"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store := ddminmem.New()
			at := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
			engine, err := ddm.New(ddm.Config{
				Store: store, Clock: clock.NewFake(at),
				EnrollmentTarget: func(context.Context, mdm.EnrollmentID) (support.Target, error) {
					return support.Target{OS: support.MacOS, Version: osversion.New(27, 0, 0), Channel: support.ChannelDevice, Supervised: true}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			a := &App{Engine: engine}
			mux := http.NewServeMux()
			for _, route := range a.ddmAdminRoutes() {
				mux.Handle(route.Pattern, route.Handler)
			}
			put := func(id, payload string, want int) []byte {
				t.Helper()
				raw := []byte(fmt.Sprintf(`{"Type":"com.apple.configuration.app.settings","Identifier":%q,"Payload":%s}`, id, payload))
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodPut, "/declarations", bytes.NewReader(raw)))
				if w.Code != want {
					t.Fatalf("upload %s: HTTP %d, want %d: %s", id, w.Code, want, w.Body.String())
				}
				return raw
			}
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "binary-test"}
			put("existing", `{"Allowed":{"DeniedBinaries":[{"SigningID":"com.example.original"}]}}`, http.StatusOK)
			if _, err := engine.AssignDeclaration(ctx, id, "existing"); err != nil {
				t.Fatal(err)
			}
			before, err := engine.GetDeclaration(ctx, "existing")
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := engine.Manifest(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := store.PendingChanges(ctx, at, 100)
			if err != nil || len(pending) == 0 {
				t.Fatal("missing baseline pending change", pending, err)
			}
			payload := fmt.Sprintf(`{"Allowed":{%q:[%s]}}`, tc.list, tc.rule)
			if !tc.allowed {
				put("new", payload, http.StatusBadRequest)
				put("existing", payload, http.StatusBadRequest)
				if _, err := engine.GetDeclaration(ctx, "new"); !errors.Is(err, ddm.ErrNotFound) {
					t.Fatal("rejected create persisted", err)
				}
				after, err := engine.GetDeclaration(ctx, "existing")
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("rejected replacement changed stored declaration", err)
				}
				afterSnapshot, err := store.Snapshot(ctx, id)
				if err != nil || !reflect.DeepEqual(snapshot, afterSnapshot) {
					t.Fatal("rejected upload changed snapshot/tokens", err)
				}
				afterPending, err := store.PendingChanges(ctx, at, 100)
				if err != nil || !reflect.DeepEqual(pending, afterPending) {
					t.Fatal("rejected upload changed pending notifications", err)
				}
				return
			}
			put("existing", payload, http.StatusOK)
			after, err := engine.Manifest(ctx, id)
			if err != nil || after.DeclarationsToken == snapshot.DeclarationsToken {
				t.Fatal("valid replacement did not advance manifest token", err)
			}
			wire, err := engine.Declaration(ctx, id, schema.KindConfiguration, "existing")
			if err != nil {
				t.Fatal(err)
			}
			var served struct{ Payload map[string]any }
			var want map[string]any
			if err := json.Unmarshal(wire, &served); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(payload), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, served.Payload) {
				t.Fatalf("valid rule/qualifiers changed in delivery: %s", wire)
			}
		})
	}
}
