package app

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

type recordingPublisher struct {
	events []event.Event
	err    error
}

// Publish records the event and returns the configured failure.
func (p *recordingPublisher) Publish(_ context.Context, e event.Event) error {
	p.events = append(p.events, e)
	return p.err
}

type listFaultStore struct {
	state.Store
	err error
}

// List injects a listing failure.
func (s listFaultStore) List(context.Context, string, string, int) ([]state.Record, error) {
	return nil, s.err
}

// createLink creates a link through the admin handler and returns its response.
func createLink(t *testing.T, a *App, body string) (EnrollmentLinkCreated, *httptest.ResponseRecorder) {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), "POST", "https://mdm.example/enrollment-links", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.createEnrollmentLink(w, r)
	var created EnrollmentLinkCreated
	if w.Code == http.StatusCreated {
		if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
	}
	return created, w
}

// public performs a request against the public handler.
func public(t *testing.T, a *App, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "https://mdm.example"+path, nil))
	return w
}

// listLinks lists links through the admin handler.
func listLinks(t *testing.T, a *App, query string) (paging.Result[EnrollmentLink], *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	a.listEnrollmentLinks(w, httptest.NewRequestWithContext(t.Context(), "GET", "https://mdm.example/enrollment-links"+query, nil))
	var res paging.Result[EnrollmentLink]
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
	}
	return res, w
}

// revokeLink revokes a link through the admin handler.
func revokeLink(t *testing.T, a *App, id string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), "DELETE", "https://mdm.example/enrollment-links/"+id, nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	a.revokeEnrollmentLink(w, r)
	return w
}

// pathOf returns the public path of a created link.
func pathOf(t *testing.T, created EnrollmentLinkCreated) string {
	t.Helper()
	path, ok := strings.CutPrefix(created.URL, "https://mdm.example")
	if !ok || !strings.HasPrefix(path, PathEnrollmentLinks) {
		t.Fatalf("link URL %q is not under %s", created.URL, PathEnrollmentLinks)
	}
	return path
}

// TestEnrollmentLinkLifecycle checks creation, the landing page, single-use redemption,
// the issued profile, the redemption event and the listed state.
func TestEnrollmentLinkLifecycle(t *testing.T) {
	a, id := replacementSecurityApp(t)
	pub := &recordingPublisher{}
	a.cfg.persistentEvents = pub
	created, w := createLink(t, a, `{"DeviceID":"`+id.ID+`","Identity":"scep","AccessRights":19,"Product":"Mac","TTL":"30m"}`)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || len(created.ID) != 64 {
		t.Fatalf("created = %+v headers = %v", created, w.Header())
	}
	if d := time.Until(created.ExpiresAt); d <= 29*time.Minute || d > 30*time.Minute {
		t.Fatalf("expiry %s is not the requested TTL", created.ExpiresAt)
	}
	path := pathOf(t, created)
	token := strings.TrimPrefix(path, PathEnrollmentLinks)
	if enrollmentLinkID(token) != created.ID || strings.Contains(w.Body.String(), `"Token"`) {
		t.Fatal("link identifier is not the token digest")
	}

	page := public(t, a, path)
	if page.Code != http.StatusOK || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
		t.Fatal(page.Code, page.Body.String())
	}
	for _, want := range []string{path + "/profile", id.ID, "Device enrollment"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("landing page lacks %q", want)
		}
	}
	for header, want := range map[string]string{
		"Referrer-Policy": "no-referrer", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
	} {
		if page.Header().Get(header) != want {
			t.Fatalf("%s = %q", header, page.Header().Get(header))
		}
	}
	if list, _ := listLinks(t, a, ""); len(list.Items) != 1 || list.Items[0].State != EnrollmentLinkActive {
		t.Fatalf("viewing the landing page changed the link: %+v", list.Items)
	}

	download := public(t, a, path+"/profile")
	if download.Code != http.StatusOK || download.Header().Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatal(download.Code, download.Body.String())
	}
	p, err := enroll.Parse(download.Body.Bytes(), profile.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if p.AccessRights != 19 || p.Scope != profile.ScopeUser || !p.CheckOutWhenRemoved {
		t.Fatalf("profile does not carry the link binding: rights %d scope %q", p.AccessRights, p.Scope)
	}
	if len(pub.events) != 1 || pub.events[0].Type != event.EnrollmentLinkRedeemed {
		t.Fatalf("events = %+v", pub.events)
	}
	if data, _ := pub.events[0].Data.(map[string]any); data["link"] != created.ID || data["device"] != id.ID {
		t.Fatalf("event data = %+v", pub.events[0].Data)
	}

	list, _ := listLinks(t, a, "")
	if len(list.Items) != 1 || list.Items[0].State != EnrollmentLinkRedeemed || list.Items[0].RedeemedAt.IsZero() {
		t.Fatalf("listed = %+v", list.Items)
	}
	unknown := public(t, a, PathEnrollmentLinks+"unknown/profile")
	for _, again := range []*httptest.ResponseRecorder{public(t, a, path+"/profile"), public(t, a, path)} {
		if again.Code != http.StatusNotFound || again.Body.String() != unknown.Body.String() {
			t.Fatalf("redeemed link response %d %q differs from unknown %q", again.Code, again.Body.String(), unknown.Body.String())
		}
	}
}

// TestEnrollmentLinkPublisherFailureStillIssues checks that a failed redemption event is
// logged rather than withholding the already-consumed link's profile.
func TestEnrollmentLinkPublisherFailureStillIssues(t *testing.T) {
	a, id := replacementSecurityApp(t)
	a.cfg.persistentEvents = &recordingPublisher{err: errors.New("capture unavailable")}
	created, _ := createLink(t, a, `{"DeviceID":"`+id.ID+`"}`)
	if w := public(t, a, pathOf(t, created)+"/profile"); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestEnrollmentLinkRejectsInvalidRequests checks request validation.
func TestEnrollmentLinkRejectsInvalidRequests(t *testing.T) {
	a, _ := replacementSecurityApp(t)
	for name, body := range map[string]string{
		"missing device": `{}`,
		"identity":       `{"DeviceID":"d","Identity":"password"}`,
		"scope":          `{"DeviceID":"d","Scope":"Global"}`,
		"negative ttl":   `{"DeviceID":"d","TTL":"-1s"}`,
		"long ttl":       `{"DeviceID":"d","TTL":"25h"}`,
		"malformed ttl":  `{"DeviceID":"d","TTL":"soon"}`,
		"malformed body": `{`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, w := createLink(t, a, body); w.Code != http.StatusBadRequest {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	if _, w := createLink(t, a, strings.Repeat(" ", MaxAdminBody+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatal(w.Code)
	}
}

// TestEnrollmentLinkExpiryAndRevocation checks that expired and revoked links are refused
// identically and that revocation is idempotent but cannot follow redemption.
func TestEnrollmentLinkExpiryAndRevocation(t *testing.T) {
	a, id := replacementSecurityApp(t)
	unknown := public(t, a, PathEnrollmentLinks+"unknown")

	expired, _ := createLink(t, a, `{"DeviceID":"`+id.ID+`","TTL":"1ns"}`)
	time.Sleep(time.Millisecond)
	for _, suffix := range []string{"", "/profile"} {
		if w := public(t, a, pathOf(t, expired)+suffix); w.Code != http.StatusNotFound || w.Body.String() != unknown.Body.String() {
			t.Fatalf("expired link%s = %d %q", suffix, w.Code, w.Body.String())
		}
	}

	revoked, _ := createLink(t, a, `{"DeviceID":"`+id.ID+`"}`)
	for range 2 {
		w := revokeLink(t, a, revoked.ID)
		var link EnrollmentLink
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &link) != nil || link.State != EnrollmentLinkRevoked {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := public(t, a, pathOf(t, revoked)+"/profile"); w.Code != http.StatusNotFound || w.Body.String() != unknown.Body.String() {
		t.Fatalf("revoked link = %d %q", w.Code, w.Body.String())
	}

	redeemed, _ := createLink(t, a, `{"DeviceID":"`+id.ID+`"}`)
	if w := public(t, a, pathOf(t, redeemed)+"/profile"); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := revokeLink(t, a, redeemed.ID); w.Code != http.StatusConflict {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, missing := range []string{strings.Repeat("0", 64), strings.Repeat("x", 300)} {
		if w := revokeLink(t, a, missing); w.Code != http.StatusNotFound {
			t.Fatal(w.Code, w.Body.String())
		}
	}

	states := map[string]string{}
	list, _ := listLinks(t, a, "")
	for _, link := range list.Items {
		states[link.ID] = link.State
	}
	if states[expired.ID] != EnrollmentLinkExpired || states[revoked.ID] != EnrollmentLinkRevoked || states[redeemed.ID] != EnrollmentLinkRedeemed {
		t.Fatalf("states = %v", states)
	}
}

// TestEnrollmentLinkListPaging checks cursor paging and limit validation.
func TestEnrollmentLinkListPaging(t *testing.T) {
	a, id := replacementSecurityApp(t)
	for range 3 {
		if _, w := createLink(t, a, `{"DeviceID":"`+id.ID+`"}`); w.Code != http.StatusCreated {
			t.Fatal(w.Code)
		}
	}
	first, _ := listLinks(t, a, "?limit=2")
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	second, _ := listLinks(t, a, "?limit=2&cursor="+first.NextCursor)
	if len(second.Items) != 1 || second.NextCursor != "" || second.Items[0].ID <= first.Items[1].ID {
		t.Fatalf("second page = %+v", second)
	}
	for _, q := range []string{"?limit=0", "?limit=x", "?limit=100000"} {
		if _, w := listLinks(t, a, q); w.Code != http.StatusBadRequest {
			t.Fatal(q, w.Code)
		}
	}
}

// TestEnrollmentLinkStorageFailures checks that storage and decoding failures are
// reported as server errors to operators and as an unavailable link to devices.
func TestEnrollmentLinkStorageFailures(t *testing.T) {
	a, id := replacementSecurityApp(t)
	backing := a.enroll.state
	created, _ := createLink(t, a, `{"DeviceID":"`+id.ID+`"}`)
	path := pathOf(t, created)
	fault := errors.New("state unavailable")
	defer func() { a.enroll.state = backing }()

	a.enroll.state = issuanceStateFault{Store: backing, writeErr: fault}
	if _, w := createLink(t, a, `{"DeviceID":"`+id.ID+`"}`); w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code)
	}
	if w := revokeLink(t, a, created.ID); w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code)
	}
	if w := public(t, a, path+"/profile"); w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}

	a.enroll.state = issuanceStateFault{Store: backing, readErr: fault}
	if w := public(t, a, path); w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}

	a.enroll.state = listFaultStore{Store: backing, err: fault}
	if _, w := listLinks(t, a, ""); w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code)
	}

	a.enroll.state = issuanceStateFault{Store: backing, txValue: []byte("corrupt")}
	if w := revokeLink(t, a, created.ID); w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code)
	}
	if w := public(t, a, path+"/profile"); w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}

	a.enroll.state = backing
	key := enrollmentLinkPrefix + created.ID
	if err := backing.Update(t.Context(), []string{key}, func(tx state.Tx) error {
		record, err := tx.Get(t.Context(), key)
		if err != nil {
			return err
		}
		record.Value = []byte("corrupt")
		return tx.Put(t.Context(), record)
	}); err != nil {
		t.Fatal(err)
	}
	if _, w := listLinks(t, a, ""); w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code)
	}
	if w := public(t, a, path); w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}
}

// TestEnrollmentLinkIssuanceFailureConsumesLink checks that a profile failure after
// redemption withholds the profile and leaves the link redeemed.
func TestEnrollmentLinkIssuanceFailureConsumesLink(t *testing.T) {
	a, id := replacementSecurityApp(t)
	created, _ := createLink(t, a, `{"DeviceID":"`+id.ID+`"}`)
	topic := a.enroll.cfg.Topic
	a.enroll.cfg.Topic = ""
	w := public(t, a, pathOf(t, created)+"/profile")
	a.enroll.cfg.Topic = topic
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "<plist") {
		t.Fatal(w.Code, w.Body.String())
	}
	if list, _ := listLinks(t, a, ""); list.Items[0].State != EnrollmentLinkRedeemed {
		t.Fatalf("state = %s", list.Items[0].State)
	}
}

// TestEnrollmentLinkRoutesRegistered checks the admin route table and the unavailable-link
// guard for an empty token.
func TestEnrollmentLinkRoutesRegistered(t *testing.T) {
	a, _ := replacementSecurityApp(t)
	want := map[string]bool{
		"POST /enrollment-links": true, "GET /enrollment-links": true, "DELETE /enrollment-links/{id}": true,
	}
	for _, route := range a.AdminRoutes() {
		if want[route.RoutePattern()] {
			if route.RouteAction() != ActionIssueEnrollmentProfile {
				t.Fatalf("%s requires %s", route.RoutePattern(), route.RouteAction())
			}
			delete(want, route.RoutePattern())
		}
	}
	if len(want) != 0 {
		t.Fatalf("unregistered routes: %v", want)
	}
	if _, err := a.activeEnrollmentLink(t.Context(), ""); !errors.Is(err, errEnrollmentLinkUnavailable) {
		t.Fatal(err)
	}
}
