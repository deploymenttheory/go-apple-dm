package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
	"github.com/deploymenttheory/go-apple-dm/server/replycerts"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

type escrowStateFailure struct{ state.Store }

func (escrowStateFailure) Update(context.Context, []string, func(state.Tx) error) error {
	return errors.New("identity storage unavailable")
}

type escrowReadFailure struct{}

func (escrowReadFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestFileVaultEscrowFailureOrdering(t *testing.T) {
	const body = `{"CommandUUID":"escrow","ProfileIdentifier":"example.escrow","Location":"Help desk"}`
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"invalid channel", http.StatusBadRequest},
		{"user channel", http.StatusBadRequest},
		{"oversized body", http.StatusRequestEntityTooLarge},
		{"body read failure", http.StatusRequestEntityTooLarge},
		{"malformed JSON", http.StatusBadRequest},
		{"volatile storage", http.StatusServiceUnavailable},
		{"missing enrollment", http.StatusNotFound},
		{"disabled enrollment", http.StatusGone},
		{"enrollment lookup failure", http.StatusInternalServerError},
		{"identity persistence failure", http.StatusInternalServerError},
		{"conflicting retry", http.StatusBadRequest},
		{"enqueue failure", http.StatusInternalServerError},
		{"unsupported enrollment", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, id := replacementSecurityApp(t)
			backing := a.Store
			identities := state.NewMemory()
			// Production enables the manager only for encrypted persistent SQL.
			// An injected store isolates handler failure ordering here.
			a.ReplyCertificates = &replycerts.Manager{Store: identities}
			r := httptest.NewRequestWithContext(t.Context(), "POST", "https://mdm.example/admin", strings.NewReader(body))
			r.SetPathValue("id", id.ID)
			r.SetPathValue("channel", "device")
			wantRecords := 0
			switch tc.name {
			case "invalid channel":
				r.SetPathValue("channel", "invalid")
			case "user channel":
				r.SetPathValue("channel", "user")
				r.URL.RawQuery = "parent=device"
			case "oversized body":
				r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", MaxAdminBody+1)))
			case "body read failure":
				r.Body = io.NopCloser(escrowReadFailure{})
			case "malformed JSON":
				r.Body = io.NopCloser(strings.NewReader("{"))
			case "volatile storage":
				a.ReplyCertificates = nil
			case "missing enrollment":
				r.SetPathValue("id", "unknown")
			case "disabled enrollment":
				if err := backing.Disable(t.Context(), id, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "enrollment lookup failure":
				a.Store = &storagetest.Failing{Store: backing, Fail: map[string]error{"Get": errors.New("unavailable")}}
			case "identity persistence failure":
				a.ReplyCertificates.Store = escrowStateFailure{identities}
			case "unsupported enrollment":
				wantRecords = 1
			case "conflicting retry":
				if _, err := a.ReplyCertificates.EscrowProfile(t.Context(), id, "escrow", "different.profile", "Help desk"); err != nil {
					t.Fatal(err)
				}
				wantRecords = 1
			case "enqueue failure":
				if err := backing.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{
					ID: id, Enabled: true,
					Device: storage.DeviceInfo{ProductName: "Mac15,3", OSVersion: "26.0"},
				}}); err != nil {
					t.Fatal(err)
				}
				var err error
				a.Core, err = service.New(service.Config{Store: &storagetest.Failing{Store: backing, Fail: map[string]error{"Enqueue": errors.New("unavailable")}}})
				if err != nil {
					t.Fatal(err)
				}
				// The identity must survive a queue failure so an explicit retry
				// can enqueue the same certificate, never a replacement key.
				wantRecords = 1
			}
			w := httptest.NewRecorder()
			a.enqueueFileVaultEscrow(w, r)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if tc.name == "unsupported enrollment" && !strings.Contains(w.Body.String(), "Skipped") {
				t.Fatal("unsupported target not reported", w.Body.String())
			}
			rows, err := backing.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{})
			if err != nil || len(rows.Items) != 0 {
				t.Fatal("failed request queued a command", err)
			}
			records, err := identities.List(t.Context(), "", "", 100)
			if err != nil || len(records) != wantRecords {
				t.Fatal("unexpected retained identity count", len(records), err)
			}
			if tc.name == "enqueue failure" {
				before, _, err := a.ReplyCertificates.Recipient(t.Context(), id, "escrow")
				if err != nil {
					t.Fatal(err)
				}
				a.Core, err = service.New(service.Config{Store: backing})
				if err != nil {
					t.Fatal(err)
				}
				r.Body = io.NopCloser(strings.NewReader(body))
				w = httptest.NewRecorder()
				a.enqueueFileVaultEscrow(w, r)
				if w.Code != http.StatusOK {
					t.Fatal("retry failed", w.Code, w.Body.String())
				}
				after, _, err := a.ReplyCertificates.Recipient(t.Context(), id, "escrow")
				if err != nil || !before.Equal(after) {
					t.Fatal("retry replaced persisted identity", err)
				}
				rows, err = backing.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{})
				if err != nil || len(rows.Items) != 1 {
					t.Fatal("retry did not enqueue exactly once", len(rows.Items), err)
				}
			}
		})
	}
}
