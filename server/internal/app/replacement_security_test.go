package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

func replacementSecurityApp(t *testing.T) (*App, mdm.EnrollmentID) {
	t.Helper()
	a, err := Build(
		t.Context(),
		Config{
			Role:    RoleAll,
			Storage: "inmem",
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			Enroll: EnrollConfig{
				PublicURL: "https://mdm.example",
				Topic:     "com.apple.mgmt.test",
				Admission: func(context.Context, AdmissionRequest) (AdmissionGrant, error) {
					return AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)}, nil
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	if err = a.Store.Import(
		t.Context(),
		storage.EnrollmentExport{
			Enrollment: storage.Enrollment{
				ID:         id,
				Enabled:    true,
				CertHash:   "original-pin",
				EnrolledAt: time.Now(),
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	return a, id
}

func TestReplacementRequiresOriginalProfileRightsAndEndpoint(t *testing.T) {
	a, id := replacementSecurityApp(t)
	binding := acme.Binding{UDID: id.ID, CommonName: id.ID}
	original, err := a.enroll.profile(t.Context(), binding)
	if err != nil {
		t.Fatal(err)
	}
	original.AccessRights = enroll.RightInstallProfiles
	if err = a.enroll.recordProfile(t.Context(), binding, original); err != nil {
		t.Fatal(err)
	}
	key := profileMetadataKey(binding, original.Identifier)
	record, err := a.protocol.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"missing template", "invalid template", "no installation right", "different endpoint"} {
		t.Run(failure, func(t *testing.T) {
			var metadata profileMetadata
			if err := json.Unmarshal(record.Value, &metadata); err != nil {
				t.Fatal(err)
			}
			p := *original
			switch failure {
			case "missing template":
				metadata.Template = nil
			case "invalid template":
				metadata.Template = []byte("broken profile")
			case "no installation right":
				p.AccessRights = enroll.RightQueryDeviceInfo
				metadata.Template, err = p.Marshal()
			case "different endpoint":
				p.Topic = "com.apple.mgmt.other"
				metadata.Template, err = p.Marshal()
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.protocol.Update(
				t.Context(),
				[]string{key},
				func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: key, Value: raw}) },
			); err != nil {
				t.Fatal(err)
			}
			if _, err = a.prepareReplacement(t.Context(), id, IdentitySCEP); err == nil {
				t.Fatal("unsafe profile replacement accepted")
			}
			device, err := a.Store.Get(t.Context(), id)
			if err != nil || device.CertHash != "original-pin" {
				t.Fatal("refusal mutated enrollment", device, err)
			}
		})
	}
	if err = a.protocol.Update(
		t.Context(),
		[]string{key},
		func(tx state.Tx) error { return tx.Put(t.Context(), record) },
	); err != nil {
		t.Fatal(err)
	}
	if _, err = a.prepareReplacement(t.Context(), id, ""); err != nil {
		t.Fatal("valid replacement denied", err)
	}
	if _, err = a.prepareReplacement(t.Context(), id, "invalid"); err == nil {
		t.Fatal("invalid issuer accepted")
	}
	if _, err = a.prepareReplacement(
		t.Context(),
		mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "absent"},
		"",
	); !errors.Is(
		err,
		storage.ErrNotFound,
	) {
		t.Fatal(err)
	}
	if err = a.Store.Disable(t.Context(), id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = a.prepareReplacement(t.Context(), id, ""); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("disabled device replacement accepted", err)
	}
}

func TestReplacementAdminValidatesBeforeCreatingAttempt(t *testing.T) {
	a, id := replacementSecurityApp(t)
	for _, input := range []struct {
		method, body string
		status       int
	}{
		{"GET", "", 404}, {"POST", "{", 400}, {"POST", strings.Repeat("x", MaxAdminBody+1), 413}, {"POST", `{"identity":"unsupported"}`, 400},
	} {
		r := httptest.NewRequest(
			input.method,
			"https://mdm.example/admin/v1/replacement",
			strings.NewReader(input.body),
		)
		r.SetPathValue("id", id.ID)
		r.SetPathValue("channel", "device")
		w := httptest.NewRecorder()
		a.replaceEnrollment(w, r)
		if w.Code != input.status {
			t.Fatal(input.method, w.Code, w.Body.String())
		}
		x, err := a.replacementStore().
			TransitionReplacement(t.Context(), id, storage.ReplacementChange{Op: "read", At: time.Now()})
		if err != nil || x != nil {
			t.Fatal("invalid input created replacement", x, err)
		}
	}
}

func TestEvidenceCorruptionNeverReportsCompletedIdentity(t *testing.T) {
	a, id := replacementSecurityApp(t)
	key := "issued-identity:original-pin"
	for _, fault := range []string{"missing", "corrupt", "unavailable"} {
		backend := a.protocol
		if fault == "corrupt" {
			if err := backend.Update(
				t.Context(),
				[]string{key},
				func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: key, Value: []byte("corrupt")}) },
			); err != nil {
				t.Fatal(err)
			}
		}
		if fault == "unavailable" {
			a.protocol = issuanceStateFault{
				Store:   backend,
				readErr: errors.New("state unavailable"),
			}
		}
		r := httptest.NewRequest("GET", "https://mdm.example/evidence", nil)
		r.SetPathValue("channel", "device")
		r.SetPathValue("id", id.ID)
		w := httptest.NewRecorder()
		a.enrollmentEvidence(w, r)
		if fault == "missing" {
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"Identity":""`) {
				t.Fatal(w.Code, w.Body.String())
			}
		} else if w.Code != 500 {
			t.Fatal(fault, w.Code)
		}
		a.protocol = backend
	}
}
