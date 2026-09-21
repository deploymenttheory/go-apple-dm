package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	schema "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
)

// reviewApp builds an application, bootstraps a managed principal, and grants it the fixture's
// admin actions.
func reviewApp(t *testing.T) (*App, string) {
	t.Helper()
	a, err := Build(t.Context(), Config{Storage: "inmem", BootstrapToken: "bootstrap-secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	root := bootstrapRBAC(t, a)
	source := `permit(principal == MDM::Principal::"root",action,resource);`
	b, _ := json.Marshal(map[string]string{"Source": source})
	if w := rbacRequest(a, "PUT", "/policies/review", root, string(b)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return a, root
}

// TestRegressionStatusReasonsEscapeProjection checks that viewer status responses redact
// diagnostic secrets while preserving stored evidence.
func TestRegressionStatusReasonsEscapeProjection(t *testing.T) {
	a, root := reviewApp(t)
	ctx := t.Context()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "review-device"}
	if err := a.Core.ImportEnrollment(ctx, storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	w := rbacRequest(a, "POST", "/principals", root, `{"Name":"viewer"}`)
	var token struct {
		Token string `json:"Token"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &token) != nil {
		t.Fatal(w.Code)
	}
	b, _ := json.Marshal(map[string]string{"Source": `permit(principal == MDM::Principal::"viewer",action in MDM::Action::"ViewerActions",resource);`})
	if w := rbacRequest(a, "PUT", "/policies/viewer", root, string(b)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	reasons := []byte(`[{"code":"invalid","description":"review-password-secret","details":{"password":"review-password-secret"}}]`)
	if err := a.Engine.Store().Update(ctx, func(tx ddm.Tx) error {
		_, err := tx.PutStatus(ctx, id, ddm.StatusUpdate{Raw: []byte(`{"details":{"password":"review-password-secret"}}`), KeepReports: 10, Values: []ddm.StatusValue{{Path: "account.password", Value: []byte(`"review-password-secret"`)}}, Errors: []ddm.StatusError{{StatusItem: "account", Reasons: reasons}}, HasDeclarations: true, ReceivedAt: time.Now(), Declarations: []ddm.DeclarationStatus{{Kind: schema.KindConfiguration, Identifier: "review.config", Valid: "invalid", Reasons: reasons}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w = rbacRequest(a, "GET", "/enrollments/device/review-device/status", token.Token, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var rows []ddm.DeclarationStatus
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatal(len(rows))
	}
	for _, suffix := range []string{"", "/values", "/errors", "/reports"} {
		response := rbacRequest(a, "GET", "/enrollments/device/review-device/status"+suffix, token.Token, "")
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		assertNoStatusSecret(t, response.Body.Bytes())
	}
	retained, err := a.Engine.DeclarationStatus(ctx, id)
	if err != nil || len(retained) != 1 || !bytes.Equal(retained[0].Reasons, reasons) {
		t.Fatalf("projection changed stored evidence: %+v %v", retained, err)
	}

	if strings.Contains(string(rows[0].Reasons), "review-password-secret") {
		t.Fatal("routine viewer received unredacted diagnostic credentials")
	}
}

// TestRegressionLargeProfileDownload checks large profile downloads retain exact bytes and
// withhold content when required audit capture fails.
func TestRegressionLargeProfileDownload(t *testing.T) {
	a, root := reviewApp(t)
	p := &profile.Profile{Identifier: "com.example.review", UUID: "6C9B0C20-0000-7000-8000-000000000001", Scope: profile.ScopeSystem, Payloads: []profile.Payload{{Identifier: "com.example.review.settings", UUID: "6C9B0C20-0000-7000-8000-000000000002", Content: &profile.Raw{Type: "com.example.settings", Keys: map[string]any{"Value": "test"}}}}}
	body, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// Legal XML whitespace exercises byte limits without requiring a huge payload tree.
	for _, size := range []int{MaxAdminBody - 1, MaxAdminBody, MaxAdminBody + 1, configurationprofile.MaxBytes} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data := append(bytes.Clone(body), bytes.Repeat([]byte(" "), size-len(body))...)
			w := rbacRequest(a, "POST", "/configuration-profiles", root, string(data))
			if w.Code != 200 {
				t.Fatal("upload", w.Code, w.Body.String())
			}
			var info struct {
				Revision string `json:"Revision"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			path := "/configuration-profiles/" + info.Revision + "/content"
			w = rbacRequest(a, "GET", path, root, "")
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
				t.Fatalf("download %d: status=%d bytes=%d", size, w.Code, w.Body.Len())
			}
			a.cfg.persistentEvents = failedReviewPublisher{}
			w = rbacRequest(a, "GET", path, root, "")
			a.cfg.persistentEvents = nil
			if w.Code != 503 || bytes.Contains(w.Body.Bytes(), []byte("<?xml")) {
				t.Fatal("profile escaped failed audit", w.Code, w.Body.String())
			}
		})
	}
}

type failedReviewPublisher struct{}

// Publish returns event.ErrCapture to simulate event publication failure.
func (failedReviewPublisher) Publish(context.Context, event.Event) error { return event.ErrCapture }

// assertNoStatusSecret checks that JSON diagnostic strings contain neither the plaintext fixture
// secret nor its base64 encoding.
func assertNoStatusSecret(t *testing.T, raw []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case string:
			if strings.Contains(v, "review-password-secret") {
				t.Fatal("unredacted diagnostic secret")
			}
			decoded, err := base64.StdEncoding.DecodeString(v)
			if err == nil && bytes.Contains(decoded, []byte("review-password-secret")) {
				t.Fatal("encoded diagnostic secret")
			}
		case map[string]any:
			for _, child := range v {
				visit(child)
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(value)
}

// TestRegressionDEPAccountBackoffLostAcrossPasses checks that subsequent DEP passes respect a
// persisted account Retry-After deadline.
func TestRegressionDEPAccountBackoffLostAcrossPasses(t *testing.T) {
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC))
	fake := deptest.NewServer(deptest.Options{Clock: clk})
	t.Cleanup(fake.Close)
	a, err := Build(ctx, Config{Storage: "inmem", Clock: clk, DEP: DEPConfig{BaseURL: fake.URL(), HTTPClient: fake.Client()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	acct := &dep.Account{Name: "review"}
	acct.SetTokens(fake.Tokens())
	if err := a.dep.store.PutAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	profile, err := a.DEP.DefineProfile(ctx, acct.Name, &dep.Profile{ProfileName: "Review", URL: "https://mdm.example.com", OrgMagic: "review"})
	if err != nil {
		t.Fatal(err)
	}
	acct.ProfileUUID = profile.ProfileUUID
	if err := a.dep.store.PutAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}
	fake.AddDevices(dep.Device{SerialNumber: "REVIEW-RATE-LIMIT"})
	fake.Script(dep.PathProfileDevs, deptest.Scripted{Status: http.StatusTooManyRequests, RetryAfter: "3600"})
	_, first, err := a.dep.runOnce(ctx, acct.Name)
	if err == nil || !first.NotBefore.Equal(clk.Now().Add(time.Hour)) {
		t.Fatalf("first pass backoff=%v err=%v", first.NotBefore, err)
	}
	before := fake.Count(http.MethodPost, dep.PathProfileDevs)
	_, second, err := a.dep.runOnce(ctx, acct.Name)
	after := fake.Count(http.MethodPost, dep.PathProfileDevs)
	t.Logf("first not-before=%v; clock=%v; immediate next pass assigned=%d; HTTP assignment calls=%d -> %d; error=%v", first.NotBefore, clk.Now(), second.Assigned, before, after, err)
	if !errors.Is(err, dep.ErrBackoff) || after != before {
		t.Fatal("next server pass ignored the account Retry-After deadline")
	}
}

// TestDEPReconciliationCaptureRollbackAndAdmission checks DEP reconciliation capture rollback and
// admission.
func TestDEPReconciliationCaptureRollbackAndAdmission(t *testing.T) {
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC))
	fake := deptest.NewServer(deptest.Options{Clock: clk})
	t.Cleanup(fake.Close)
	a, err := Build(ctx, Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "dep.sqlite"), StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}, Clock: clk, DEP: DEPConfig{BaseURL: fake.URL(), HTTPClient: fake.Client()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	account := &dep.Account{Name: "fleet", ProfileUUID: "profile", CreatedAt: clk.Now(), UpdatedAt: clk.Now()}
	account.SetTokens(fake.Tokens())
	if err := a.dep.store.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	fake.AddDevices(dep.Device{SerialNumber: "REMOVED", ProfileUUID: account.ProfileUUID, ProfileStatus: dep.ProfileStatusAssigned})
	worker, err := a.dep.syncer(account.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	policyFile := filepath.Join(t.TempDir(), "admission.json")
	if err := os.WriteFile(policyFile, []byte(`{"depAccounts":["fleet"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	admit, err := a.enrollmentAdmission(&enrollment{cfg: EnrollConfig{AdmissionFile: policyFile}})
	if err != nil {
		t.Fatal(err)
	}
	request := AdmissionRequest{Serial: "REMOVED"}
	if _, err := admit(ctx, request); err != nil {
		t.Fatal(err)
	}
	fake.DeleteDevice(request.Serial)
	clk.Advance(8 * 24 * time.Hour)
	if _, err := a.db.ExecContext(ctx, `CREATE TRIGGER refuse_dep_capture BEFORE INSERT ON event_records WHEN NEW.type = 'dep-device-deleted' BEGIN SELECT RAISE(ABORT, 'injected capture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(ctx); !errors.Is(err, eventstore.ErrCapture) {
		t.Fatalf("capture failure: %v", err)
	}
	device, err := a.dep.store.GetDevice(ctx, account.Name, request.Serial)
	if err != nil || device.Deleted {
		t.Fatalf("reconciliation escaped rollback: %+v %v", device, err)
	}
	cursor, err := a.dep.store.Cursor(ctx, account.Name)
	if err != nil || cursor.Phase != dep.PhaseFetch || cursor.Generation == "" || cursor.Value != "" {
		t.Fatalf("final page cursor escaped rollback: %+v %v", cursor, err)
	}
	if _, err := a.db.ExecContext(ctx, "DROP TRIGGER refuse_dep_capture"); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := admit(ctx, request); !errors.Is(err, webauth.ErrDenied) {
		t.Fatalf("removed device retained admission: %v", err)
	}
}
