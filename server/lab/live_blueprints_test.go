package lab

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/lab/target"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
)

// A live run must not pass from retained status, an invalid declaration, a
// missing token or a configuration that never activated. Inactive configurations
// can legitimately remain unknown until their activation predicate becomes true.
func TestLiveBlueprintStatusRejectsIncompleteEvidence(t *testing.T) {
	now := time.Now().UTC()
	valid := ddm.DeclarationStatus{Identifier: "configuration", ServerToken: "current", Active: true, Valid: "valid", LastSeen: now}
	for _, tc := range []struct {
		name   string
		change func(*ddm.DeclarationStatus)
	}{
		{"missing identifier", func(v *ddm.DeclarationStatus) { v.Identifier = "" }},
		{"missing token", func(v *ddm.DeclarationStatus) { v.ServerToken = "" }},
		{"stale", func(v *ddm.DeclarationStatus) { v.LastSeen = now.Add(-time.Second) }},
		{"inactive", func(v *ddm.DeclarationStatus) { v.Active = false }},
		{"invalid", func(v *ddm.DeclarationStatus) { v.Valid = "invalid" }},
		{"unknown", func(v *ddm.DeclarationStatus) { v.Valid = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := valid
			tc.change(&row)
			if liveBlueprintStatus(row, true, now, true) {
				t.Fatal("incomplete device evidence accepted")
			}
		})
	}
	if !liveBlueprintStatus(valid, true, now, true) {
		t.Fatal("fresh valid device evidence rejected")
	}
	valid.Active, valid.Valid = false, "unknown"
	if !liveBlueprintStatus(valid, false, now, true) {
		t.Fatal("inactive configuration awaiting activation rejected")
	}
	valid.Valid = "invalid"
	if liveBlueprintStatus(valid, false, now, true) {
		t.Fatal("invalid inactive declaration accepted")
	}
}

// TestLiveBlueprintRejectsMalformedDataAndEvidenceWriteFailures checks that live blueprint rejects
// malformed data and evidence write failures.
func TestLiveBlueprintRejectsMalformedDataAndEvidenceWriteFailures(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := "{"
		switch {
		case strings.HasSuffix(r.URL.Path, "/commands"):
			body = `{"Queued":1}`
		case strings.HasSuffix(r.URL.Path, "/push"):
			body = `{"Sent":true}`
		case strings.HasSuffix(r.URL.Path, "/result"):
			data, err := json.Marshal(map[string]any{"Status": "Acknowledged", "Response": []byte("invalid plist")})
			if err != nil {
				t.Fatal(err)
			}
			body = string(data)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})}
	f := &liveBlueprintTest{e: &Environment{Instance: Instance{URL: "https://bench.example"}, Client: client}, dir: t.TempDir(), id: "test", phase: "invalid"}
	if err := f.publish(t.Context(), blueprint.Spec{Identifier: "test"}); err == nil {
		t.Fatal("malformed publication response accepted")
	}
	if err := f.publish(t.Context(), blueprint.Spec{Identifier: "test", Declarations: []blueprint.Declaration{{Payload: []byte("{")}}}); err == nil {
		t.Fatal("malformed source accepted")
	}
	if err := f.checkProfile(t.Context(), ""); err == nil {
		t.Fatal("malformed native ProfileList accepted as profile removal")
	}
	if err := f.evidence("bad", func() {}); err == nil {
		t.Fatal("evidence serialization failure ignored")
	}
	f.dir = filepath.Join(t.TempDir(), "missing")
	if err := f.evidence("missing", struct{}{}); err == nil {
		t.Fatal("evidence write failure ignored")
	}
	p := &profile.Profile{Identifier: "com.example.test", UUID: profile.NewUUID(), Payloads: []profile.Payload{{Identifier: "com.example.test.settings", UUID: profile.NewUUID(), Content: &profile.Raw{Type: "com.example.test", Keys: map[string]any{"Invalid": func() {}}}}}}
	if _, err := f.upload(t.Context(), p); err == nil {
		t.Fatal("profile encoding failure ignored")
	}
}

// TestLiveBlueprintWaitRequiresBothConfigurationAndActivation checks that live blueprint wait
// requires both configuration and activation.
func TestLiveBlueprintWaitRequiresBothConfigurationAndActivation(t *testing.T) {
	for _, activation := range []bool{false, true} {
		compiled := &blueprint.Compiled{Identifiers: map[string]string{}, Activations: map[string]string{}}
		if activation {
			compiled.Activations["always"] = "missing"
		} else {
			compiled.Identifiers["subscription"] = "missing"
		}
		e := &Environment{Instance: Instance{URL: "https://bench.example"}, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("[]")), Header: http.Header{}, Request: r}, nil
		})}}
		f := &liveBlueprintTest{e: e, dir: t.TempDir(), record: blueprints.Record{Compiled: compiled}, identifiers: map[string]bool{"missing": true}}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		err := f.wait(ctx, time.Now(), true, "", nil)
		cancel()
		if err == nil {
			t.Fatal("missing native declaration status counted as success")
		}
	}
}

// TestLiveBlueprintPrerequisites checks that live blueprint scenarios reject missing
// prerequisites.
func TestLiveBlueprintPrerequisites(t *testing.T) {
	for _, mode := range []string{"user ID", "evidence directory"} {
		t.Run(mode, func(t *testing.T) {
			f := &blueprintProtocolFixture{t: t, uploads: map[string]*profile.Profile{}}
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				// Exercise the same protocol fixture without opening another listener.
				w := httptest.NewRecorder()
				f.serve(w, r)
				return w.Result(), nil
			})}
			dir := t.TempDir()
			if mode == "evidence directory" {
				dir = filepath.Join(dir, "file")
				if err := os.WriteFile(dir, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			e := &Environment{Instance: Instance{URL: "https://bench.example"}, Client: client, Workspace: &Workspace{Directory: dir}}
			if err := liveBlueprints(mode == "user ID")(t.Context(), e, "mac"); err == nil {
				t.Fatal("missing prerequisite accepted")
			}
		})
	}
}

// TestBlueprintScenariosRequireLiveDevices checks blueprint scenarios require live devices.
func TestBlueprintScenariosRequireLiveDevices(t *testing.T) {
	scenarios, err := Select("blueprints")
	if err != nil || len(scenarios) != 2 {
		t.Fatalf("Blueprint scenarios: %v %v", scenarios, err)
	}
	for _, scenario := range scenarios {
		result := Run(t.Context(), &Environment{Instance: Instance{Mode: "simulated"}}, target.Simulator{}, scenario, Options{Adapter: "test", Revision: "revision"})
		if result.Status != "unsupported" {
			t.Fatalf("%s claimed device evidence in simulated mode: %+v", scenario.ID, result)
		}
	}
}
