package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
)

// TestManualEnrollmentInstallationScope checks manual enrollment installation scope.
func TestManualEnrollmentInstallationScope(t *testing.T) {
	for _, tc := range []struct{ product, scope, want string }{
		{"Mac", "", profile.ScopeUser},
		{"Mac", profile.ScopeSystem, profile.ScopeSystem},
		{"iPhone", "", profile.ScopeSystem},
		{"Mac", "invalid", ""},
	} {
		t.Run(tc.product+tc.scope, func(t *testing.T) {
			a, id := replacementSecurityApp(t)
			data, err := a.ExportEnrollmentProfile(t.Context(), EnrollmentProfileRequest{
				DeviceID: id.ID, Product: tc.product, Identity: "scep", Scope: tc.scope,
			})
			if tc.want == "" {
				if err == nil || data != nil {
					t.Fatal("invalid scope delivered a profile")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p, err := enroll.Parse(data, profile.ParseOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if p.Scope != tc.want {
				t.Fatalf("scope = %q, want %q", p.Scope, tc.want)
			}
		})
	}
}

type rejectProfileTemplateStore struct {
	state.Store
	fault error
}
type rejectProfileTemplateTx struct {
	state.Tx
	fault error
}

// Update wraps state transactions to reject writes containing profile templates.
func (s rejectProfileTemplateStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(rejectProfileTemplateTx{Tx: tx, fault: s.fault}) })
}

// Put rejects stored profile metadata containing a template and delegates other writes.
func (tx rejectProfileTemplateTx) Put(ctx context.Context, r state.Record) error {
	var metadata profileMetadata
	if json.Unmarshal(r.Value, &metadata) == nil && len(metadata.Template) > 0 {
		return tx.fault
	}
	return tx.Tx.Put(ctx, r)
}

// TestProfilePersistenceFailureNeverDeliversCredentialProfile checks that profile persistence
// failure never delivers credential profile.
func TestProfilePersistenceFailureNeverDeliversCredentialProfile(t *testing.T) {
	a, id := replacementSecurityApp(t)
	e := a.enroll
	ctx := t.Context()
	backing := e.state
	binding := acme.Binding{UDID: id.ID, CommonName: id.ID}
	p, err := e.profile(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("profile metadata unavailable")
	for _, st := range []state.Store{
		issuanceStateFault{Store: backing, txReadErr: fault},
		issuanceStateFault{Store: backing, txValue: []byte("corrupt metadata")},
		rejectProfileTemplateStore{Store: backing, fault: fault},
	} {
		e.state = st
		if err = e.recordProfile(ctx, binding, p); err == nil {
			t.Fatal("failed template persistence reported success")
		}
	}
	e.state = backing
	broken := *p
	broken.Topic = ""
	if err = e.recordProfile(ctx, binding, &broken); err == nil {
		t.Fatal("malformed template persisted")
	}
	for _, mode := range []string{"grant persistence", "metadata read", "invalid profile", "template persistence"} {
		t.Run(mode, func(t *testing.T) {
			e.state = backing
			originalTopic := e.cfg.Topic
			defer func() { e.cfg.Topic = originalTopic; e.state = backing }()
			want := 400
			switch mode {
			case "grant persistence":
				e.state = issuanceStateFault{Store: backing, writeErr: fault}
			case "metadata read":
				e.state = issuanceStateFault{Store: backing, txReadErr: fault}
			case "invalid profile":
				e.cfg.Topic = ""
				want = 500
			case "template persistence":
				e.state = rejectProfileTemplateStore{Store: backing, fault: fault}
				want = 500
			}
			r := httptest.NewRequestWithContext(t.Context(), "POST", "https://mdm.example/enrollment-profiles", strings.NewReader(`{"DeviceID":"device"}`))
			w := httptest.NewRecorder()
			a.issueEnrollmentProfile(w, r)
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "<plist") || w.Header().Get("Content-Type") == "application/x-apple-aspen-config" {
				t.Fatal("failed persistence delivered credential profile")
			}
		})
	}
}

// TestCommandEvidenceRejectsInvalidTargetAndStorageFailure checks that command evidence rejects
// invalid target and storage failure.
func TestCommandEvidenceRejectsInvalidTargetAndStorageFailure(t *testing.T) {
	a, id := replacementSecurityApp(t)
	a.Store = &storagetest.Failing{Store: a.Store, Fail: map[string]error{"Commands": errors.New("evidence unavailable")}}
	for _, valid := range []bool{false, true} {
		r := httptest.NewRequestWithContext(t.Context(), "GET", "https://mdm.example/result", nil)
		want := 400
		if valid {
			r.SetPathValue("channel", "device")
			r.SetPathValue("id", id.ID)
			r.SetPathValue("uuid", "command")
			want = 500
		}
		w := httptest.NewRecorder()
		a.getCommandResult(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
