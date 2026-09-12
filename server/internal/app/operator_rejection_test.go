package app_test

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func TestOperatorAPIsRejectInvalidInput(t *testing.T) {
	t.Parallel()
	f := newEnrollFixture(t, "", func(c *app.Config) { c.AdminToken = "operator" })
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"malformed credential", "PUT", "/apppush/credentials", "{", 400},
		{"oversized credential", "PUT", "/apppush/credentials", strings.Repeat("x", app.MaxAdminBody+1), 413},
		{"invalid identity", "PUT", "/apppush/credentials", `{"CertPEM":"invalid","KeyPEM":"private-do-not-echo"}`, 400},
		{"invalid pagination", "GET", "/apppush/credentials?limit=abc", "", 400},
		{"excessive pagination", "GET", "/apppush/credentials?limit=1001", "", 400},
		{"malformed send", "POST", "/apppush/send", "{", 400},
		{"implicit environment", "POST", "/apppush/send", `{}`, 400},
		{"invalid token", "POST", "/apppush/send", `{"Environment":"development","Token":"zz"}`, 400},
		{"invalid payload", "POST", "/apppush/send", `{"Environment":"development","Token":"aa","Topic":"com.example.app","PushType":"alert","Payload":{}}`, 400},
		{"missing credential", "POST", "/apppush/send", `{"Environment":"development","Token":"aa","Topic":"com.example.app","PushType":"alert","Payload":{"aps":{"alert":"test"}}}`, 502},
		{"missing device", "POST", "/enrollment-profiles", `{}`, 400},
		{"malformed profile request", "POST", "/enrollment-profiles", `{`, 400},
		{"oversized profile request", "POST", "/enrollment-profiles", strings.Repeat("x", app.MaxAdminBody+1), 413},
		{"unknown command", "GET", "/enrollments/device/absent/commands/absent/result", "", 404},
		{"invalid channel", "GET", "/enrollments/invalid/absent/commands/absent/result", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/admin/v1"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer operator")
			rec := httptest.NewRecorder()
			f.app.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "private-do-not-echo") {
				t.Fatal("error leaked input credential")
			}
		})
	}
	cfg := app.Config{
		Role:       app.RoleAll,
		Storage:    "inmem",
		AdminToken: "t",
		Logger:     quiet,
		AppPush:    app.AppPushConfig{RootCAFile: "missing.pem"},
	}
	if a, err := app.Build(t.Context(), cfg); err == nil {
		a.Close()
		t.Fatal("missing APNs trust roots accepted")
	}
}

func TestCommandResultPagesUntilMatchingPendingCommand(t *testing.T) {
	a, srv, _ := mdmAdminApp(t)
	id := seed(t, a, "paged-results")
	var last string
	for i := range 101 {
		cmd, err := mdm.NewCommand(
			&commands.DeviceInformation{Queries: []string{"OSVersion"}},
			mdm.WithUUID(fmt.Sprintf("CMD-%03d", i)),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Core.Enqueue(
			t.Context(),
			[]mdm.EnrollmentID{id},
			cmd,
			storage.EnqueueOptions{},
		); err != nil {
			t.Fatal(err)
		}
		last = cmd.UUID
	}
	resp := adminReq(
		t,
		srv,
		"GET",
		"/admin/v1/enrollments/device/paged-results/commands/"+last+"/result",
		"t",
		"",
	)
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("pending command on second page: HTTP %d", resp.StatusCode)
	}
}

func TestOperatorSecurityConfigurationRejectsIncompleteCredentials(t *testing.T) {
	t.Parallel()
	for _, material := range []string{"missing", "{", `{"alice":"not-a-digest"}`, `{"alice":"aabb"}`} {
		file := filepath.Join(t.TempDir(), "user-ha1.json")
		if material != "missing" {
			if err := os.WriteFile(file, []byte(material), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cfg := app.Config{
			Role:    app.RoleAll,
			Storage: "inmem",
			Logger:  quiet,
			Enroll:  app.EnrollConfig{UserAuthHA1File: file},
		}
		if a, err := app.Build(t.Context(), cfg); err == nil {
			a.Close()
			t.Fatal("invalid user authentication data accepted")
		}
	}
	if a, err := app.Build(
		t.Context(),
		app.Config{
			Role:        app.RoleAll,
			Storage:     "inmem",
			Logger:      quiet,
			TLSCertFile: "certificate.pem",
		},
	); err == nil {
		a.Close()
		t.Fatal("TLS certificate without key accepted")
	}
}
