package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	admininmem "github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/audit"
	auditinmem "github.com/deploymenttheory/go-apple-dm/server/audit/inmem"
)

func rbacRequest(a *App, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), method, "/admin/v1"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	return w
}

func bootstrapRBAC(t *testing.T, a *App) string {
	t.Helper()
	w := rbacRequest(a, "POST", "/auth/bootstrap", "bootstrap-secret", `{"Name":"root"}`)
	var result struct {
		Token string `json:"Token"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !adminauth.Valid(adminauth.Token(result.Token)) {
		t.Fatalf("bootstrap: HTTP %d", w.Code)
	}
	return result.Token
}

func TestRBACBootstrapPersistsAndRootHasNoFleetAuthority(t *testing.T) {
	cfg := Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "server.db"), BootstrapToken: "bootstrap-secret", StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a, err := Build(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	root := bootstrapRBAC(t, a)
	for _, tc := range []struct {
		method, path, token string
		want                int
	}{
		{"GET", "/enrollments", "bootstrap-secret", 401},
		{"GET", "/enrollments", root, 403},
		{"GET", "/roles", root, 200},
		{"GET", "/schema", root, 200},
		{"GET", "/auth/me", root, 200},
		{"POST", "/auth/bootstrap", "bootstrap-secret", 409},
	} {
		if w := rbacRequest(a, tc.method, tc.path, tc.token, `{"Name":"another"}`); w.Code != tc.want {
			t.Fatalf("%s: %d, want %d", tc.path, w.Code, tc.want)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Build(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if w := rbacRequest(a, "POST", "/auth/bootstrap", "bootstrap-secret", `{"Name":"after-restart"}`); w.Code != 409 {
		t.Fatalf("bootstrap reopened: %d", w.Code)
	}
	if w := rbacRequest(a, "GET", "/auth/me", root, ""); w.Code != 200 {
		t.Fatalf("root lost on restart: %d", w.Code)
	}
}

func TestRBACRoutineGrantsAndSensitiveBoundaries(t *testing.T) {
	trail := auditinmem.New()
	a, err := Build(t.Context(), Config{Storage: "inmem", BootstrapToken: "bootstrap-secret", Sinks: SinkConfig{AuditStore: trail}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	root := bootstrapRBAC(t, a)
	if w := rbacRequest(a, "POST", "/principals", root, `{"Name":"operator","Roles":["operators"]}`); w.Code != 400 {
		t.Fatalf("unknown role accepted: %d", w.Code)
	}
	if w := rbacRequest(a, "PUT", "/roles/operators", root, `{"Description":"Diagnostic operators"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	w := rbacRequest(a, "POST", "/principals", root, `{"Name":"operator","Roles":["operators"]}`)
	var credential struct {
		Token string `json:"Token"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &credential) != nil {
		t.Fatal(w.Code)
	}
	if w := rbacRequest(a, "GET", "/enrollments", credential.Token, ""); w.Code != 403 {
		t.Fatalf("role implicitly granted access: %d", w.Code)
	}
	source := `permit(principal in MDM::Role::"operators", action in MDM::Action::"OperatorActions", resource);`
	put := func(name, source string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"Source": source})
		if err != nil {
			t.Fatal(err)
		}
		if w := rbacRequest(a, "PUT", "/policies/"+name, root, string(body)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	put("operators", source)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "RBAC-DEVICE"}
	if err := a.Core.ImportEnrollment(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	path := "/enrollments/device/" + id.ID
	if w := rbacRequest(a, "GET", "/enrollments", credential.Token, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, kind := range []string{"DeviceInformation", "FutureUnknownAfterDiagnostic", "SecurityInfo", "ProfileList", "InstalledApplicationList", "CertificateList", "EraseDevice", "DeviceLock", "RotateFileVaultKey", "FutureUnknownCommand"} {
		queries := ""
		if kind == "DeviceInformation" {
			queries = "<key>Queries</key><array><string>OSVersion</string></array>"
		}
		command := fmt.Sprintf(`<plist version="1.0"><dict><key>CommandUUID</key><string>rbac-%s</string><key>Command</key><dict><key>RequestType</key><string>%s</string>%s</dict></dict></plist>`, kind, kind, queries)
		want := 403
		switch kind {
		case "DeviceInformation", "SecurityInfo", "ProfileList", "InstalledApplicationList", "CertificateList":
			want = 200
		}
		if w := rbacRequest(a, "POST", path+"/commands", credential.Token, command); w.Code != want {
			t.Fatalf("%s: %d want %d: %s", kind, w.Code, want, w.Body.String())
		}
	}
	for _, endpoint := range []string{path + "/commands/rbac-DeviceInformation/result", "/export", "/configuration-profiles/secret/content"} {
		if w := rbacRequest(a, "GET", endpoint, credential.Token, ""); w.Code != 403 {
			t.Fatalf("sensitive route %s: %d", endpoint, w.Code)
		}
	}
	if _, err := a.Store.Next(t.Context(), id, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.StoreResult(t.Context(), id, &mdm.Response{ID: id, CommandUUID: "rbac-DeviceInformation", Status: mdm.StatusAcknowledged, Raw: []byte("fixture-sensitive-result")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	put("raw-read", `permit(principal == MDM::Principal::"operator",action == MDM::Action::"readRawCommandResult",resource);`)
	if w := rbacRequest(a, "GET", path+"/commands/rbac-DeviceInformation/result", credential.Token, ""); w.Code != 200 {
		t.Fatal("explicit raw-result grant failed", w.Code)
	}
	var concurrent sync.WaitGroup
	for i := range 12 {
		concurrent.Go(func() {
			kind, want := "FutureUnknownCommand", 403
			if i%2 == 0 {
				kind, want = "SecurityInfo", 200
			}
			body := fmt.Sprintf(`<plist version="1.0"><dict><key>CommandUUID</key><string>concurrent-%d</string><key>Command</key><dict><key>RequestType</key><string>%s</string></dict></dict></plist>`, i, kind)
			if w := rbacRequest(a, "POST", path+"/commands", credential.Token, body); w.Code != want {
				t.Errorf("concurrent %s: %d, want %d", kind, w.Code, want)
			}
		})
	}
	concurrent.Wait()
	if w := rbacRequest(a, "PUT", "/roles/escalation", credential.Token, `{}`); w.Code != 403 {
		t.Fatalf("nonroot administered roles: %d", w.Code)
	}
	if w := rbacRequest(a, "POST", "/policies/operators/activation", root, `{"Active":false}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := rbacRequest(a, "GET", "/enrollments", credential.Token, ""); w.Code != 403 {
		t.Fatalf("deactivation not applied: %d", w.Code)
	}
	if w := rbacRequest(a, "POST", "/policies/operators/activation", root, `{"Active":true}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	put("deny", `forbid(principal in MDM::Role::"operators",action == MDM::Action::"listEnrollments",resource);`)
	if w := rbacRequest(a, "GET", "/enrollments", credential.Token, ""); w.Code != 403 {
		t.Fatalf("forbid did not override permit: %d", w.Code)
	}
	if w := rbacRequest(a, "POST", "/policies/validate", root, `{"Name":"bad","Source":"permit(principal, action == MDM::Action::\"listEnrollments\",resource) when { context.requestTypo == \"x\" };"}`); w.Code != 400 {
		t.Fatalf("invalid context passed validation: %d", w.Code)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err := trail.List(t.Context(), audit.Query{Type: "admin-denied"}, audit.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows.Items {
		if row.Fields["Action"] == "enqueueCommand.EraseDevice" {
			found = true
			if row.Actor != "operator" || row.Fields["Resource"] != `MDM::Enrollment::"device/RBAC-DEVICE/"` || row.Fields["PolicyVersion"] == nil || row.Fields["Outcome"] != "denied" {
				t.Fatalf("incomplete denial audit: %+v", row)
			}
		}
	}
	reads, err := trail.List(t.Context(), audit.Query{Type: "admin-action"}, audit.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	rawAudited := false
	for _, row := range reads.Items {
		if row.Fields["Action"] == "readRawCommandResult" {
			rawAudited = row.Fields["Outcome"] == "succeeded" && row.Fields["Policies"] != nil
		}
		encoded, _ := json.Marshal(row.Fields)
		if strings.Contains(string(encoded), "fixture-sensitive-result") || strings.Contains(string(encoded), credential.Token) {
			t.Fatal("audit exposed a secret")
		}
	}
	if !rawAudited {
		t.Fatal("sensitive read was not audited")
	}
	if !found {
		t.Fatal("destructive command denial was not audited")
	}
}

func TestRBACInvalidActivePolicyFailsClosedAndRootCanRepair(t *testing.T) {
	store := admininmem.New()
	a, err := Build(t.Context(), Config{Storage: "inmem", AdminStore: store, BootstrapToken: "bootstrap-secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	root := bootstrapRBAC(t, a)
	if _, err := a.admin.PutPolicy(t.Context(), adminauth.Root, adminauth.Policy{Name: "allow", Source: `permit(principal, action, resource);`}); err != nil {
		t.Fatal(err)
	}
	// Simulate an old persisted forbid with an action removed by this upgrade.
	if _, err := store.PutPolicy(t.Context(), adminauth.Policy{Name: "old-forbid", Source: `forbid(principal,action == MDM::Action::"enqueueCommand",resource);`}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := rbacRequest(a, "GET", "/enrollments", root, ""); w.Code != 403 {
		t.Fatalf("invalid forbid was dropped while permits survived: %d", w.Code)
	}
	if w := rbacRequest(a, "GET", "/policies", root, ""); w.Code != 200 {
		t.Fatal("root cannot inspect broken policies", w.Code)
	}
	if w := rbacRequest(a, "DELETE", "/policies/old-forbid", root, ""); w.Code != 204 {
		t.Fatal("root cannot repair broken policies", w.Code)
	}
	if w := rbacRequest(a, "GET", "/enrollments", root, ""); w.Code != 200 {
		t.Fatal("repaired policy set not used", w.Code)
	}
}
