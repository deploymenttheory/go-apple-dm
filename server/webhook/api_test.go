package webhook

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func adminRequest(t *testing.T, s *Store, method, path, body string, root bool, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Admin(w, r, root)
	if w.Code != status {
		t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable admin response")
	}
	return w
}

func TestAdminContract(t *testing.T) {
	s := testStore(t, Config{})
	body := `{"name":"workflow","url":"https://receiver.example.test/webhook","events":["protocol.*"],"payload":{"full_json":true}}`
	adminRequest(t, s, "POST", "/webhooks", body, false, 403)
	w := adminRequest(t, s, "POST", "/webhooks", body, true, 201)
	var c Change
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || c.Credentials == nil {
		t.Fatal(c, err)
	}
	base := "/webhooks/" + c.Subscription.ID
	for _, path := range []string{"/webhooks", "/webhooks/catalogue", "/webhooks/status", base} {
		w := adminRequest(t, s, "GET", path, "", false, 200)
		if strings.Contains(w.Body.String(), c.Credentials.SigningSecret) || strings.Contains(w.Body.String(), c.Credentials.PayloadToken) {
			t.Fatal("credential read leak")
		}
	}
	for _, op := range []string{"pause", "resume", "disable", "enable", "test", "credentials"} {
		adminRequest(t, s, "POST", base+"/"+op, "{}", false, 403)
	}
	for _, op := range []string{"pause", "disable", "enable", "resume"} {
		adminRequest(t, s, "POST", base+"/"+op, "", true, 200)
	}
	adminRequest(t, s, "POST", base+"/credentials", "{}", true, 200)
	adminRequest(t, s, "POST", base+"/credentials", `{"overlap_seconds":-1}`, true, 400)
	adminRequest(t, s, "POST", base+"/credentials", `{"overlap_seconds":0}`, true, 200)
	adminRequest(t, s, "POST", base+"/test", "", true, 202)
	d := deliveries(t, s, c.Subscription.ID)[0]
	adminRequest(t, s, "GET", "/webhooks/deliveries?subscription_id="+c.Subscription.ID, "", false, 200)
	adminRequest(t, s, "GET", "/webhooks/deliveries/"+d.ID, "", false, 200)
	adminRequest(t, s, "POST", "/webhooks/deliveries/"+d.ID+"/retry", "", false, 403)
	adminRequest(t, s, "POST", "/webhooks/deliveries/"+d.ID+"/retry", "", true, 204)
	adminRequest(t, s, "POST", "/webhooks/replays", `{"subscription_id":"`+c.Subscription.ID+`","dry_run":true}`, false, 403)
	adminRequest(t, s, "POST", "/webhooks/replays", `{"subscription_id":"`+c.Subscription.ID+`","dry_run":true}`, true, 202)
	adminRequest(t, s, "PUT", base, `{"revision":1,"spec":`+body+`}`, true, 200)
	adminRequest(t, s, "PUT", base, `{"revision":1,"spec":`+body+`}`, true, 409)
	adminRequest(t, s, "DELETE", base, "", false, 403)
	adminRequest(t, s, "DELETE", base, "", true, 200)
	adminRequest(t, s, "POST", base+"/resume", "", true, 409)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/webhooks", `{"extra":1}`, 400},
		{"POST", "/webhooks", `{} {}`, 400},
		{"POST", "/webhooks", strings.Repeat(" ", 64<<10) + body, 400},
		{"GET", "/webhooks?limit=invalid", "", 400},
		{"GET", "/webhooks?limit=1001", "", 400},
		{"GET", "/webhooks/missing", "", 404},
		{"POST", "/webhooks/replays", `{}`, 400},
		{"POST", base + "/unknown", "", 404},
		{"PATCH", base, "", 404},
		{"GET", "/webhooks/deliveries/x/extra", "", 404},
	} {
		adminRequest(t, s, tc.method, tc.path, tc.body, true, tc.status)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE webhook_subscriptions"); err != nil {
		t.Fatal(err)
	}
	w = adminRequest(t, s, "GET", base, "", true, 503)
	if strings.Contains(w.Body.String(), "SQL") || strings.Contains(w.Body.String(), "table") {
		t.Fatal("storage diagnostic disclosure")
	}
}

func TestFiltersAndPauseSemantics(t *testing.T) {
	s := testStore(t, Config{})
	c := subscribe(t, s, PayloadPolicy{})
	spec := c.Subscription.Spec
	spec.Events = []string{"protocol.mdm.*"}
	spec.Filters = Filters{Operations: []string{"connect"}, Outcomes: []string{"succeeded"}, Channels: []string{"device"}, Subjects: []string{"device-1"}, CommandTypes: []string{"DeviceInformation"}}
	if _, err := s.Update(t.Context(), c.Subscription.ID, 1, spec, false); err != nil {
		t.Fatal(err)
	}
	e := occurrence()
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if len(deliveries(t, s, "")) != 0 {
		t.Fatal("missing command type matched")
	}
	e.Data["command_type"] = "DeviceInformation"
	if _, err := s.SetState(t.Context(), c.Subscription.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	d := deliveries(t, s, "")
	if len(d) != 1 || d[0].State != "paused" {
		t.Fatal(d)
	}
	if _, err := s.SetState(t.Context(), c.Subscription.ID, "disable", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if len(deliveries(t, s, "")) != 1 {
		t.Fatal("disabled subscription captured")
	}
	for _, action := range []string{"enable", "resume"} {
		if _, err := s.SetState(t.Context(), c.Subscription.ID, action, false); err != nil {
			t.Fatal(err)
		}
	}
	if got := deliveries(t, s, "")[0].State; got != "pending" {
		t.Fatal(got)
	}
	e.Subject = nil
	e.Data["claimed_id"] = "device-1"
	if err := s.Capture(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if len(deliveries(t, s, "")) != 1 {
		t.Fatal("claimed subject matched verified filter")
	}
}
