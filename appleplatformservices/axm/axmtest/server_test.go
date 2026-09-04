package axmtest_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/axm/axmtest"
)

const clientID = "BUSINESSAPI.11111111-2222-3333-4444-555555555555"

type harness struct {
	srv *axmtest.Server
	key *ecdsa.PrivateKey
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv := axmtest.NewServer()
	t.Cleanup(srv.Close)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterKey(clientID, "kid-1", &key.PublicKey)
	return &harness{srv: srv, key: key}
}

// client builds an axm client against the fake.
func (h *harness) client(t *testing.T) *axm.Client {
	t.Helper()
	c, err := axm.New(context.Background(), axm.Config{
		ClientID: clientID, KeyID: "kid-1", PrivateKey: h.key, BaseURL: h.srv.URL, TokenURL: h.srv.TokenURL,
		HTTPClient: h.srv.Client(), Retry: axm.Retry{Max: 0, Base: time.Millisecond, Cap: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// tokenForm posts a token request with overrides and returns the status
// and decoded body.
func (h *harness) tokenForm(t *testing.T, override map[string]string, contentType string) (int, map[string]any) {
	t.Helper()
	assertion, err := axm.Assertion(h.key, clientID, "kid-1", time.Now(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_assertion_type": {axm.ClientAssertionType},
		"client_assertion": {assertion}, "scope": {"business.api"},
	}
	for k, v := range override {
		form.Set(k, v)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.TokenURL, strings.NewReader(form.Encode()))
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// bearer obtains a valid access token.
func (h *harness) bearer(t *testing.T) string {
	t.Helper()
	status, body := h.tokenForm(t, nil, "")
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, body)
	}
	return body["access_token"].(string)
}

// call performs a raw API request.
func (h *harness) call(t *testing.T, method, path, token, accept string, body string) (*http.Response, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, h.srv.URL+path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &doc)
	return resp, doc
}

// firstError returns code and source of the first error in an error document.
func firstError(t *testing.T, doc map[string]any) (code string, source map[string]any) {
	t.Helper()
	errs, _ := doc["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("no errors in %v", doc)
	}
	e := errs[0].(map[string]any)
	source, _ = e["source"].(map[string]any)
	return e["code"].(string), source
}

// signRaw builds a JWS with an arbitrary header and claims signed by key.
func signRaw(t *testing.T, key *ecdsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	// Reuse axm's signer through a real assertion's signature layout: sign
	// the digest ourselves.
	sig, err := ecdsaSign(key, signing)
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + sig
}

func TestServer(t *testing.T) {
	t.Parallel()
	t.Run("TokenEndpoint", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		status, body := h.tokenForm(t, nil, "")
		if status != http.StatusOK || body["token_type"] != "Bearer" || body["expires_in"] != float64(3600) || body["scope"] != "business.api" || body["access_token"] == "" {
			t.Fatalf("%d %v", status, body)
		}
		cases := map[string]struct {
			override map[string]string
			ct       string
			want     string
		}{
			"content type":   {nil, "application/json", "invalid_request"},
			"grant type":     {map[string]string{"grant_type": "password"}, "", "unsupported_grant_type"},
			"assertion type": {map[string]string{"client_assertion_type": "x"}, "", "invalid_request"},
			"unknown client": {map[string]string{"client_id": "BUSINESSAPI.other"}, "", "invalid_client"},
			"wrong scope":    {map[string]string{"scope": "school.api"}, "", "invalid_scope"},
			"garbage":        {map[string]string{"client_assertion": "not.a.jwt"}, "", "invalid_client"},
			"two segments":   {map[string]string{"client_assertion": "a.b"}, "", "invalid_client"},
		}
		for name, tc := range cases {
			status, body := h.tokenForm(t, tc.override, tc.ct)
			if status != http.StatusBadRequest || body["error"] != tc.want {
				t.Errorf("%s: %d %v, want %s", name, status, body, tc.want)
			}
		}
		now := time.Now().Unix()
		base := map[string]any{"iss": clientID, "sub": clientID, "aud": axm.Audience, "iat": now, "exp": now + 600, "jti": uuid.New().String()}
		other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		claimCases := map[string]struct {
			header map[string]any
			mutate func(map[string]any)
			key    *ecdsa.PrivateKey
			detail string
		}{
			"alg":        {map[string]any{"alg": "RS256", "kid": "kid-1"}, nil, h.key, "alg must be ES256"},
			"kid":        {map[string]any{"alg": "ES256", "kid": "kid-9"}, nil, h.key, "unknown kid"},
			"signature":  {map[string]any{"alg": "ES256", "kid": "kid-1"}, nil, other, "signature does not verify"},
			"issuer":     {map[string]any{"alg": "ES256", "kid": "kid-1"}, func(c map[string]any) { c["iss"] = "someone" }, h.key, "iss/sub"},
			"audience":   {map[string]any{"alg": "ES256", "kid": "kid-1"}, func(c map[string]any) { c["aud"] = "x" }, h.key, "aud must be"},
			"no jti":     {map[string]any{"alg": "ES256", "kid": "kid-1"}, func(c map[string]any) { c["jti"] = "" }, h.key, "jti is required"},
			"future iat": {map[string]any{"alg": "ES256", "kid": "kid-1"}, func(c map[string]any) { c["iat"] = now + 3600 }, h.key, "in the future"},
			"expired":    {map[string]any{"alg": "ES256", "kid": "kid-1"}, func(c map[string]any) { c["exp"] = now - 3600 }, h.key, "has passed"},
			"too long":   {map[string]any{"alg": "ES256", "kid": "kid-1"}, func(c map[string]any) { c["exp"] = now + 200*24*3600 }, h.key, "180 days"},
		}
		for name, tc := range claimCases {
			claims := map[string]any{}
			for k, v := range base {
				claims[k] = v
			}
			claims["jti"] = uuid.New().String()
			if tc.mutate != nil {
				tc.mutate(claims)
			}
			status, body := h.tokenForm(t, map[string]string{"client_assertion": signRaw(t, tc.key, tc.header, claims)}, "")
			if status != http.StatusBadRequest || body["error"] != "invalid_client" || !strings.Contains(body["error_description"].(string), tc.detail) {
				t.Errorf("%s: %d %v", name, status, body)
			}
		}
		// jti replay.
		replay := signRaw(t, h.key, map[string]any{"alg": "ES256", "kid": "kid-1"}, base)
		if status, _ := h.tokenForm(t, map[string]string{"client_assertion": replay}, ""); status != http.StatusOK {
			t.Fatalf("first use: %d", status)
		}
		if status, body := h.tokenForm(t, map[string]string{"client_assertion": replay}, ""); status != http.StatusBadRequest || !strings.Contains(body["error_description"].(string), "jti") {
			t.Fatalf("replayed jti: %d %v", status, body)
		}
		h.srv.RejectNextTokenRequests(1)
		if status, body := h.tokenForm(t, nil, ""); status != http.StatusBadRequest || body["error"] != "invalid_client" {
			t.Fatalf("fault: %d %v", status, body)
		}
		if status, _ := h.tokenForm(t, nil, ""); status != http.StatusOK {
			t.Fatalf("after fault: %d", status)
		}
		if n := h.srv.TokenRequests(); n < 10 {
			t.Fatalf("token requests recorded %d", n)
		}
		// A school client id gets the school scope.
		h.srv.RegisterKey("SCHOOLAPI.x", "kid-s", &h.key.PublicKey)
		a, _ := axm.Assertion(h.key, "SCHOOLAPI.x", "kid-s", time.Now(), 0, 0)
		if status, body := h.tokenForm(t, map[string]string{"client_id": "SCHOOLAPI.x", "client_assertion": a, "scope": "school.api"}, ""); status != http.StatusOK || body["scope"] != "school.api" {
			t.Fatalf("school: %d %v", status, body)
		}
		// A malformed form body.
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, h.srv.TokenURL, strings.NewReader("%zz"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("malformed form: %d", resp.StatusCode)
		}
	})
	t.Run("AcceptAndBearer", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		tok := h.bearer(t)
		resp, doc := h.call(t, http.MethodGet, "/v1/orgDevices", tok, "", "")
		if resp.StatusCode != http.StatusNotAcceptable {
			t.Fatalf("no Accept: %d %v", resp.StatusCode, doc)
		}
		resp, _ = h.call(t, http.MethodGet, "/v1/orgDevices", tok, "text/plain", "")
		if resp.StatusCode != http.StatusNotAcceptable {
			t.Fatalf("text Accept: %d", resp.StatusCode)
		}
		for _, accept := range []string{"application/json", "*/*", "text/html, application/*;q=0.9"} {
			if resp, _ := h.call(t, http.MethodGet, "/v1/orgDevices", tok, accept, ""); resp.StatusCode != http.StatusOK {
				t.Errorf("Accept %q: %d", accept, resp.StatusCode)
			}
		}
		for name, token := range map[string]string{"missing": "", "bogus": "at-nope"} {
			resp, doc := h.call(t, http.MethodGet, "/v1/orgDevices", token, "application/json", "")
			if code, _ := firstError(t, doc); resp.StatusCode != http.StatusUnauthorized || code != "UNAUTHORIZED" {
				t.Errorf("%s token: %d %v", name, resp.StatusCode, doc)
			}
		}
		h.srv.ExpireTokens()
		if resp, _ := h.call(t, http.MethodGet, "/v1/orgDevices", tok, "application/json", ""); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expired: %d", resp.StatusCode)
		}
		tok = h.bearer(t)
		h.srv.SetNow(func() time.Time { return time.Now().Add(2 * time.Hour) })
		if resp, _ := h.call(t, http.MethodGet, "/v1/orgDevices", tok, "application/json", ""); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("lapsed TTL: %d", resp.StatusCode)
		}
	})
	t.Run("Faults", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		tok := h.bearer(t)
		h.srv.RateLimit(1, "7")
		resp, doc := h.call(t, http.MethodGet, "/v1/orgDevices", tok, "application/json", "")
		if code, _ := firstError(t, doc); resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "7" || code != "RATE_LIMIT_EXCEEDED" {
			t.Fatalf("429: %d %v %v", resp.StatusCode, resp.Header, doc)
		}
		h.srv.RateLimit(1, "")
		if resp, _ := h.call(t, http.MethodGet, "/v1/orgDevices", tok, "application/json", ""); resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "" {
			t.Fatalf("429 without header: %d %v", resp.StatusCode, resp.Header)
		}
		h.srv.ServerError(1)
		resp, doc = h.call(t, http.MethodGet, "/v1/orgDevices", tok, "application/json", "")
		if code, _ := firstError(t, doc); resp.StatusCode != http.StatusServiceUnavailable || code != "SERVICE_UNAVAILABLE" {
			t.Fatalf("503: %d %v", resp.StatusCode, doc)
		}
		if resp, _ := h.call(t, http.MethodGet, "/v1/orgDevices", tok, "application/json", ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("after faults: %d", resp.StatusCode)
		}
	})
	t.Run("PagingAndFields", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		for _, s := range []string{"A", "B", "C"} {
			h.srv.AddOrgDevice(s, map[string]any{"color": "RED"})
		}
		tok := h.bearer(t)
		resp, doc := h.call(t, http.MethodGet, "/v1/orgDevices?limit=2&fields%5BorgDevices%5D=serialNumber", tok, "application/json", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%d %v", resp.StatusCode, doc)
		}
		data := doc["data"].([]any)
		attrs := data[0].(map[string]any)["attributes"].(map[string]any)
		if len(data) != 2 || len(attrs) != 1 || attrs["serialNumber"] != "A" {
			t.Fatalf("fields not honoured: %v", data)
		}
		paging := doc["meta"].(map[string]any)["paging"].(map[string]any)
		links := doc["links"].(map[string]any)
		if paging["total"] != float64(3) || paging["limit"] != float64(2) || paging["nextCursor"] != "2" {
			t.Fatalf("paging %v", paging)
		}
		next, _ := links["next"].(string)
		if !strings.Contains(next, "cursor=2") || !strings.Contains(next, "fields%5BorgDevices%5D=serialNumber") || !strings.HasPrefix(next, h.srv.URL) {
			t.Fatalf("next %q", next)
		}
		resp, doc = h.call(t, http.MethodGet, strings.TrimPrefix(next, h.srv.URL), tok, "application/json", "")
		if data := doc["data"].([]any); resp.StatusCode != http.StatusOK || len(data) != 1 || doc["links"].(map[string]any)["next"] != nil {
			t.Fatalf("last page: %d %v", resp.StatusCode, doc)
		}
		// Cursor past the end is empty, not an error.
		if _, doc := h.call(t, http.MethodGet, "/v1/orgDevices?cursor=99", tok, "application/json", ""); len(doc["data"].([]any)) != 0 {
			t.Fatalf("past the end: %v", doc)
		}
		bad := map[string]string{
			"/v1/orgDevices?limit=0":                                          "limit",
			"/v1/orgDevices?limit=1001":                                       "limit",
			"/v1/orgDevices?limit=x":                                          "limit",
			"/v1/orgDevices?cursor=-1":                                        "cursor",
			"/v1/orgDevices?cursor=x":                                         "cursor",
			"/v1/orgDevices?fields%5BorgDevices%5D=bogus":                     "fields[orgDevices]",
			"/v1/orgDevices?fields%5Busers%5D=firstName":                      "fields[users]",
			"/v1/blueprints?include=bogus":                                    "include",
			"/v1/blueprints?limit%5Bapps%5D=0":                                "limit[apps]",
			"/v1/auditEvents":                                                 "filter[startTimestamp]",
			"/v1/auditEvents?filter%5BstartTimestamp%5D=2026-01-01T00:00:00Z": "filter[endTimestamp]",
		}
		for path, param := range bad {
			resp, doc := h.call(t, http.MethodGet, path, tok, "application/json", "")
			code, source := firstError(t, doc)
			if resp.StatusCode != http.StatusBadRequest || code != "PARAMETER_ERROR.INVALID" || source["parameter"] != param {
				t.Errorf("%s: %d %s %v", path, resp.StatusCode, code, source)
			}
		}
	})
	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		tok := h.bearer(t)
		paths := []string{
			"/v1/orgDevices/X", "/v1/orgDevices/X/appleCareCoverage", "/v1/orgDevices/X/relationships/assignedServer",
			"/v1/orgDevices/X/assignedServer", "/v1/mdmDevices/X/details", "/v1/mdmServers/X", "/v1/mdmServers/X/relationships/devices",
			"/v1/orgDeviceActivities/X", "/v1/orgDeviceActivities/X/download", "/v1/users/X", "/v1/userGroups/X",
			"/v1/userGroups/X/relationships/users", "/v1/organizationalUnits/X", "/v1/organizationalUnits/X/relationships/users",
			"/v1/apps/X", "/v1/packages/X", "/v1/configurations/X", "/v1/blueprints/X", "/v1/blueprints/X/relationships/apps",
			"/v1/nothing",
		}
		for _, p := range paths {
			resp, doc := h.call(t, http.MethodGet, p, tok, "application/json", "")
			code, _ := firstError(t, doc)
			if resp.StatusCode != http.StatusNotFound || (code != "RESOURCE_NOT_FOUND" && code != "PATH_ERROR.NOT_FOUND") {
				t.Errorf("%s: %d %s", p, resp.StatusCode, code)
			}
		}
		h.srv.AddBlueprint("bp", nil)
		if resp, _ := h.call(t, http.MethodGet, "/v1/blueprints/bp/relationships/bogus", tok, "application/json", ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("bogus relationship: %d", resp.StatusCode)
		}
		if resp, _ := h.call(t, http.MethodPost, "/v1/blueprints/bp/relationships/bogus", tok, "application/json", `{"data":[]}`); resp.StatusCode != http.StatusNotFound {
			t.Errorf("bogus relationship post: %d", resp.StatusCode)
		}
		for _, m := range []string{http.MethodPatch, http.MethodDelete} {
			for _, p := range []string{"/v1/mdmServers/X", "/v1/configurations/X", "/v1/blueprints/X"} {
				body := `{"data":{"type":"` + strings.Split(p, "/")[2] + `","id":"X","attributes":{"name":"n","serverName":"n"}}}`
				if resp, _ := h.call(t, m, p, tok, "application/json", body); resp.StatusCode != http.StatusNotFound {
					t.Errorf("%s %s: %d", m, p, resp.StatusCode)
				}
			}
		}
		h.srv.AddOrgDevice("D", nil)
		h.srv.UnassignedLinkage404(true)
		if resp, _ := h.call(t, http.MethodGet, "/v1/orgDevices/D/relationships/assignedServer", tok, "application/json", ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("unassigned 404 mode: %d", resp.StatusCode)
		}
		h.srv.UnassignedLinkage404(false)
		if _, doc := h.call(t, http.MethodGet, "/v1/orgDevices/D/relationships/assignedServer", tok, "application/json", ""); doc["data"].(map[string]any)["id"] != "" {
			t.Errorf("unassigned empty mode: %v", doc)
		}
		if resp, _ := h.call(t, http.MethodGet, "/v1/orgDevices/D/assignedServer", tok, "application/json", ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("unassigned full server: %d", resp.StatusCode)
		}
	})
	t.Run("Activities", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		serverID := h.srv.AddMDMServer("Prod", nil)
		h.srv.AddOrgDevice("S1", nil)
		h.srv.AddOrgDevice("S2", nil)
		tok := h.bearer(t)
		post := func(body string) (*http.Response, map[string]any) {
			return h.call(t, http.MethodPost, "/v1/orgDeviceActivities", tok, "application/json", body)
		}
		devices := `"devices":{"data":[{"type":"orgDevices","id":"S1"}]}`
		server := `"mdmServer":{"data":{"type":"mdmServers","id":"` + serverID + `"}}`
		bad := map[string]struct {
			body   string
			status int
		}{
			"malformed":       {`{`, 400},
			"wrong type":      {`{"data":{"type":"x"}}`, 400},
			"unknown type":    {`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"X"},"relationships":{` + devices + `}}}`, 400},
			"no devices":      {`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"ASSIGN_DEVICES"},"relationships":{}}}`, 400},
			"no server":       {`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"ASSIGN_DEVICES"},"relationships":{` + devices + `}}}`, 400},
			"unknown server":  {`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"ASSIGN_DEVICES"},"relationships":{` + devices + `,"mdmServer":{"data":{"type":"mdmServers","id":"NOPE"}}}}}`, 409},
			"no deadline":     {`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"UPDATE_MDM_MIGRATION_DEADLINE"},"relationships":{` + devices + `}}}`, 400},
			"late deadline":   {`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"UPDATE_MDM_MIGRATION_DEADLINE","activityTypeMetadata":{"mdmMigrationDeadlineDateTime":"2099-01-01T00:00:00Z"}},"relationships":{` + devices + `}}}`, 409},
			"bad device type": {`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"UNASSIGN_DEVICES"},"relationships":{"devices":{"data":[{"type":"users","id":"S1"}]}}}}`, 400},
			"unknown device":  {`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"UNASSIGN_DEVICES"},"relationships":{"devices":{"data":[{"type":"orgDevices","id":"NOPE"}]}}}}`, 409},
		}
		for name, tc := range bad {
			resp, doc := post(tc.body)
			if resp.StatusCode != tc.status {
				t.Errorf("%s: %d %v, want %d", name, resp.StatusCode, doc, tc.status)
			}
		}
		resp, doc := post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"ASSIGN_DEVICES"},"relationships":{` + devices + `,` + server + `}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d %v", resp.StatusCode, doc)
		}
		id := doc["data"].(map[string]any)["id"].(string)
		if _, err := uuid.Parse(id); err != nil {
			t.Fatalf("activity id %q", id)
		}
		status, sub, ok := h.srv.Activity(id)
		if !ok || status != "IN_PROGRESS" || sub != "SUBMITTED" {
			t.Fatalf("%s %s %v", status, sub, ok)
		}
		if _, _, ok := h.srv.Activity("nope"); ok {
			t.Fatal("unknown activity")
		}
		h.srv.SetConsistencyLag(20 * time.Millisecond)
		if n := h.srv.Advance(); n != 1 {
			t.Fatalf("advanced %d", n)
		}
		if _, sub, _ := h.srv.Activity(id); sub != "PROCESSING" {
			t.Fatal(sub)
		}
		h.srv.Advance()
		if status, sub, _ := h.srv.Activity(id); status != "COMPLETED" || sub != "COMPLETED_WITH_SUCCESS" {
			t.Fatalf("%s %s", status, sub)
		}
		if h.srv.Advance() != 0 {
			t.Fatal("terminal activities must not move")
		}
		if got := h.srv.AssignedServer("S1"); got != "" {
			t.Fatalf("visible before the lag: %q", got)
		}
		time.Sleep(30 * time.Millisecond)
		if got := h.srv.AssignedServer("S1"); got != serverID {
			t.Fatalf("after the lag: %q", got)
		}
		_, doc = h.call(t, http.MethodGet, "/v1/orgDeviceActivities/"+id, tok, "application/json", "")
		attrs := doc["data"].(map[string]any)["attributes"].(map[string]any)
		dl, _ := attrs["downloadUrl"].(string)
		if attrs["status"] != "COMPLETED" || attrs["completedDateTime"] == nil || !strings.HasPrefix(dl, h.srv.URL) {
			t.Fatalf("%v", attrs)
		}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, dl, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "text/csv")
		csvResp, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		csv, _ := io.ReadAll(csvResp.Body)
		csvResp.Body.Close()
		if csvResp.Header.Get("Content-Type") != "text/csv" || !strings.Contains(string(csv), "S1,ASSIGN_DEVICES,SUCCESS,") {
			t.Fatalf("csv %q", csv)
		}
		// Server device count, linkage, and audit trail follow.
		_, doc = h.call(t, http.MethodGet, "/v1/mdmServers/"+serverID, tok, "application/json", "")
		if doc["data"].(map[string]any)["attributes"].(map[string]any)["deviceCount"] != float64(1) {
			t.Fatalf("%v", doc)
		}
		_, doc = h.call(t, http.MethodGet, "/v1/mdmServers/"+serverID+"/relationships/devices", tok, "application/json", "")
		if len(doc["data"].([]any)) != 1 {
			t.Fatalf("%v", doc)
		}
		_, doc = h.call(t, http.MethodGet, "/v1/orgDevices/S1", tok, "application/json", "")
		if doc["data"].(map[string]any)["attributes"].(map[string]any)["status"] != "ASSIGNED" {
			t.Fatalf("%v", doc)
		}
		start := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		_, doc = h.call(t, http.MethodGet, "/v1/auditEvents?filter%5BstartTimestamp%5D="+start+"&filter%5BendTimestamp%5D="+end+"&filter%5Btype%5D=DEVICE_ASSIGNED_TO_SERVER&filter%5BsubjectId%5D=S1&filter%5BactorId%5D=api", tok, "application/json", "")
		events := doc["data"].([]any)
		if len(events) != 1 || events[0].(map[string]any)["attributes"].(map[string]any)["eventDataPropertyKey"] != "eventDataDeviceAssignedToServer" {
			t.Fatalf("audit %v", doc)
		}
		// Deleting a server with devices is a conflict.
		if resp, _ := h.call(t, http.MethodDelete, "/v1/mdmServers/"+serverID, tok, "application/json", ""); resp.StatusCode != http.StatusConflict {
			t.Fatalf("delete busy server: %d", resp.StatusCode)
		}
		// Per-serial outcomes, unassign, release, and AutoAdvance.
		h.srv.SetConsistencyLag(0)
		h.srv.SetOutcome("S2", "locked")
		h.srv.SetOutcome("S3", "x")
		h.srv.SetOutcome("S3", "")
		resp, doc = post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"ASSIGN_DEVICES"},"relationships":{"devices":{"data":[{"type":"orgDevices","id":"S1"},{"type":"orgDevices","id":"S2"}]},` + server + `}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d %v", resp.StatusCode, doc)
		}
		id2 := doc["data"].(map[string]any)["id"].(string)
		h.srv.AutoAdvance(time.Millisecond)
		deadline := time.Now().Add(2 * time.Second)
		for {
			if status, _, _ := h.srv.Activity(id2); status == "COMPLETED" || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		h.srv.AutoAdvance(0)
		if _, sub, _ := h.srv.Activity(id2); sub != "COMPLETED_WITH_ERROR" {
			t.Fatalf("outcome: %s", sub)
		}
		if h.srv.AssignedServer("S2") != "" {
			t.Fatal("failed serial assigned")
		}
		resp, doc = post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"UNASSIGN_DEVICES"},"relationships":{` + devices + `}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d %v", resp.StatusCode, doc)
		}
		h.srv.Complete()
		if h.srv.AssignedServer("S1") != "" {
			t.Fatal("still assigned")
		}
		resp, doc = post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"RELEASE_DEVICES"},"relationships":{` + devices + `}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d %v", resp.StatusCode, doc)
		}
		h.srv.Complete()
		if h.srv.HasOrgDevice("S1") {
			t.Fatal("released device remains")
		}
		// An activity whose device vanished before completion.
		h.srv.AddOrgDevice("S9", nil)
		rel, _ := post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"RELEASE_DEVICES"},"relationships":{"devices":{"data":[{"type":"orgDevices","id":"S9"}]}}}}`)
		if rel.StatusCode != http.StatusCreated {
			t.Fatal(rel.StatusCode)
		}
		resp, doc = post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"UNASSIGN_DEVICES"},"relationships":{"devices":{"data":[{"type":"orgDevices","id":"S9"}]}}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d %v", resp.StatusCode, doc)
		}
		gone := doc["data"].(map[string]any)["id"].(string)
		h.srv.Complete()
		if _, sub, _ := h.srv.Activity(gone); sub != "COMPLETED_WITH_ERROR" {
			t.Fatalf("vanished device: %s", sub)
		}
		// Migration deadline activities on a device that is migrating and
		// one that is not.
		h.srv.AddOrgDevice("M1", nil)
		m := `"devices":{"data":[{"type":"orgDevices","id":"M1"}]}`
		soon := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
		resp, _ = post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"ASSIGN_DEVICES_WITH_MDM_MIGRATION_DEADLINE","activityTypeMetadata":{"mdmMigrationDeadlineDateTime":"` + soon + `"}},"relationships":{` + m + `,` + server + `}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatal(resp.StatusCode)
		}
		h.srv.Complete()
		if h.srv.DeviceAttribute("M1", "mdmMigrationStatus") != "REQUESTED" || h.srv.DeviceAttribute("M1", "mdmMigrationDeadlineDateTime") == nil {
			t.Fatal("migration not recorded")
		}
		resp, _ = post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"UPDATE_MDM_MIGRATION_DEADLINE","activityTypeMetadata":{"mdmMigrationDeadlineDateTime":"` + soon + `"}},"relationships":{` + m + `}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatal(resp.StatusCode)
		}
		resp, doc = post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"UPDATE_MDM_MIGRATION_DEADLINE","activityTypeMetadata":{"mdmMigrationDeadlineDateTime":"` + soon + `"}},"relationships":{` + devices + `}}}`)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("released device: %d %v", resp.StatusCode, doc)
		}
		h.srv.AddOrgDevice("N1", nil)
		resp, doc = post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"UPDATE_MDM_MIGRATION_DEADLINE","activityTypeMetadata":{"mdmMigrationDeadlineDateTime":"` + soon + `"}},"relationships":{"devices":{"data":[{"type":"orgDevices","id":"N1"}]}}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatal(resp.StatusCode)
		}
		notMigrating := doc["data"].(map[string]any)["id"].(string)
		h.srv.Complete()
		if _, sub, _ := h.srv.Activity(notMigrating); sub != "COMPLETED_WITH_ERROR" {
			t.Fatalf("not migrating: %s", sub)
		}
		resp, _ = post(`{"data":{"type":"orgDeviceActivities","attributes":{"activityType":"CANCEL_MDM_MIGRATION"},"relationships":{` + m + `}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatal(resp.StatusCode)
		}
		h.srv.Complete()
		if h.srv.DeviceAttribute("M1", "mdmMigrationStatus") != nil || h.srv.DeviceAttribute("NOPE", "x") != nil {
			t.Fatal("migration not cancelled")
		}
		// Fields on the activity endpoint.
		if resp, _ := h.call(t, http.MethodGet, "/v1/orgDeviceActivities/"+id+"?fields%5BorgDeviceActivities%5D=bogus", tok, "application/json", ""); resp.StatusCode != http.StatusBadRequest {
			t.Fatal(resp.StatusCode)
		}
		// Closing stops a running ticker.
		h.srv.AutoAdvance(time.Hour)
		h.srv.AutoAdvance(time.Hour)
	})
	t.Run("ServersConfigurationsBlueprints", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		tok := h.bearer(t)
		call := func(method, path, body string) (*http.Response, map[string]any) {
			return h.call(t, method, path, tok, "application/json", body)
		}
		// Servers.
		resp, doc := call(http.MethodPost, "/v1/mdmServers", `{"data":{"type":"mdmServers","attributes":{"serverName":"A","serverCertificate":{"name":"c","data":"QQ=="}}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d %v", resp.StatusCode, doc)
		}
		serverID := doc["data"].(map[string]any)["id"].(string)
		if resp, _ := call(http.MethodPost, "/v1/mdmServers", `{"data":{"type":"mdmServers","attributes":{"serverName":"A","serverCertificate":{"name":"c","data":"QQ=="}}}}`); resp.StatusCode != http.StatusConflict {
			t.Fatalf("duplicate: %d", resp.StatusCode)
		}
		if resp, _ := call(http.MethodPost, "/v1/mdmServers", `{"data":{"type":"mdmServers","attributes":{"serverName":"B"}}}`); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("no certificate: %d", resp.StatusCode)
		}
		if resp, _ := call(http.MethodPost, "/v1/mdmServers", `{"data":{"type":"x"}}`); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("wrong type: %d", resp.StatusCode)
		}
		if resp, _ := call(http.MethodPatch, "/v1/mdmServers/"+serverID, `{"data":{"type":"mdmServers","id":"other"}}`); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("mismatched id: %d", resp.StatusCode)
		}
		resp, doc = call(http.MethodPatch, "/v1/mdmServers/"+serverID, `{"data":{"type":"mdmServers","id":"`+serverID+`","attributes":{"enableMdmDisownFlag":true,"defaultProductFamilies":["MAC"]}}}`)
		attrs := doc["data"].(map[string]any)["attributes"].(map[string]any)
		if resp.StatusCode != http.StatusOK || attrs["enableMdmDisownFlag"] != true || attrs["serverName"] != "A" {
			t.Fatalf("%d %v", resp.StatusCode, attrs)
		}
		if resp, _ := call(http.MethodDelete, "/v1/mdmServers/"+serverID, ""); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
		// Configurations.
		cfgBody := `{"data":{"type":"configurations","attributes":{"type":"CUSTOM_SETTING","name":"W","customSettingsValues":{"configurationProfile":"PD94bWw=","filename":"w.mobileconfig"}}}}`
		resp, doc = call(http.MethodPost, "/v1/configurations", cfgBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d %v", resp.StatusCode, doc)
		}
		cfgID := doc["data"].(map[string]any)["id"].(string)
		for name, body := range map[string]string{
			"wrong type": `{"data":{"type":"configurations","attributes":{"type":"WIFI","name":"W","customSettingsValues":{"configurationProfile":"x"}}}}`,
			"no profile": `{"data":{"type":"configurations","attributes":{"type":"CUSTOM_SETTING","name":"W"}}}`,
			"filename":   `{"data":{"type":"configurations","attributes":{"type":"CUSTOM_SETTING","name":"W","customSettingsValues":{"configurationProfile":"x","filename":"w.txt"}}}}`,
		} {
			if resp, _ := call(http.MethodPost, "/v1/configurations", body); resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("%s: %d", name, resp.StatusCode)
			}
		}
		if resp, _ := call(http.MethodPost, "/v1/configurations", `{"data":{"type":"x"}}`); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("wrong type: %d", resp.StatusCode)
		}
		resp, doc = call(http.MethodPost, "/v1/configurations", `{"data":{"type":"configurations","attributes":{"type":"CUSTOM_SETTING","name":"D","customSettingsValues":{"configurationProfile":"PD94bWw="}}}}`)
		values := doc["data"].(map[string]any)["attributes"].(map[string]any)["customSettingsValues"].(map[string]any)
		if resp.StatusCode != http.StatusCreated || !strings.HasSuffix(values["filename"].(string), ".mobileconfig") {
			t.Fatalf("default filename: %v", values)
		}
		_, doc = call(http.MethodGet, "/v1/configurations", "")
		for _, item := range doc["data"].([]any) {
			if item.(map[string]any)["attributes"].(map[string]any)["customSettingsValues"] != nil {
				t.Fatal("list must null customSettingsValues")
			}
		}
		_, doc = call(http.MethodGet, "/v1/configurations/"+cfgID, "")
		if doc["data"].(map[string]any)["attributes"].(map[string]any)["customSettingsValues"] == nil {
			t.Fatal("get must return customSettingsValues")
		}
		if resp, _ := call(http.MethodPatch, "/v1/configurations/"+cfgID, `{"data":{"type":"configurations","id":"`+cfgID+`","attributes":{}}}`); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("empty update: %d", resp.StatusCode)
		}
		if resp, _ := call(http.MethodPatch, "/v1/configurations/"+cfgID, `{"data":{"type":"configurations","id":"`+cfgID+`","attributes":{"customSettingsValues":{"filename":"x.txt"}}}}`); resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("bad filename: %d", resp.StatusCode)
		}
		if resp, _ := call(http.MethodPatch, "/v1/configurations/"+cfgID, `{"data":{"type":"configurations","id":"x"}}`); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("mismatched id: %d", resp.StatusCode)
		}
		resp, doc = call(http.MethodPatch, "/v1/configurations/"+cfgID, `{"data":{"type":"configurations","id":"`+cfgID+`","attributes":{"name":"W2","configuredForPlatforms":["PLATFORM_IOS"],"customSettingsValues":{"configurationProfile":"QUJD","filename":"n.mobileconfig"}}}}`)
		attrs = doc["data"].(map[string]any)["attributes"].(map[string]any)
		if resp.StatusCode != http.StatusOK || attrs["name"] != "W2" || attrs["customSettingsValues"].(map[string]any)["filename"] != "n.mobileconfig" {
			t.Fatalf("%d %v", resp.StatusCode, attrs)
		}
		h.srv.AddConfiguration("ro", map[string]any{"type": "WIFI", "customSettingsValues": nil})
		if resp, _ := call(http.MethodPatch, "/v1/configurations/ro", `{"data":{"type":"configurations","id":"ro","attributes":{"name":"x"}}}`); resp.StatusCode != http.StatusConflict {
			t.Fatalf("read-only type: %d", resp.StatusCode)
		}
		h.srv.AddConfiguration("nv", map[string]any{"customSettingsValues": nil})
		if resp, _ := call(http.MethodPatch, "/v1/configurations/nv", `{"data":{"type":"configurations","id":"nv","attributes":{"customSettingsValues":{"filename":"a.mobileconfig"}}}}`); resp.StatusCode != http.StatusOK {
			t.Fatalf("values from nil: %d", resp.StatusCode)
		}
		if resp, _ := call(http.MethodDelete, "/v1/configurations/"+cfgID, ""); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
		// Blueprints.
		h.srv.AddApp("app1", nil)
		h.srv.AddApp("app2", nil)
		h.srv.AddUser("u1", nil)
		resp, doc = call(http.MethodPost, "/v1/blueprints", `{"data":{"type":"blueprints","attributes":{"name":"B","description":"d"},"relationships":{"apps":{"data":[{"type":"apps","id":"app1"},{"type":"apps","id":"nope"}]},"bogus":{"data":[]}}}}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d %v", resp.StatusCode, doc)
		}
		bpID := doc["data"].(map[string]any)["id"].(string)
		if got := h.srv.BlueprintLinks(bpID, "apps"); len(got) != 1 || got[0] != "app1" {
			t.Fatalf("invalid ids must be dropped: %v", got)
		}
		if h.srv.BlueprintLinks("nope", "apps") != nil {
			t.Fatal("unknown blueprint links")
		}
		h.srv.LinkBlueprint("nope", "apps", "x")
		for name, body := range map[string]string{"no name": `{"data":{"type":"blueprints","attributes":{}}}`, "no attributes": `{"data":{"type":"blueprints"}}`} {
			if resp, _ := call(http.MethodPost, "/v1/blueprints", body); resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("%s: %d", name, resp.StatusCode)
			}
		}
		if resp, _ := call(http.MethodPost, "/v1/blueprints", `{"data":{"type":"x"}}`); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("wrong type: %d", resp.StatusCode)
		}
		_, doc = call(http.MethodGet, "/v1/blueprints/"+bpID+"?include=apps,users&limit%5Bapps%5D=1", "")
		if inc := doc["included"].([]any); len(inc) != 1 || inc[0].(map[string]any)["id"] != "app1" {
			t.Fatalf("included %v", doc["included"])
		}
		_, doc = call(http.MethodGet, "/v1/blueprints?include=apps", "")
		if inc := doc["included"].([]any); len(inc) != 1 {
			t.Fatalf("list included %v", doc["included"])
		}
		_, doc = call(http.MethodGet, "/v1/blueprints?include=users", "")
		if inc, ok := doc["included"].([]any); !ok || len(inc) != 0 {
			t.Fatalf("empty included %v", doc)
		}
		if resp, _ := call(http.MethodPost, "/v1/blueprints/"+bpID+"/relationships/apps", `{"data":[{"type":"apps","id":"app2"},{"type":"apps","id":"app1"}]}`); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("add: %d", resp.StatusCode)
		}
		if got := h.srv.BlueprintLinks(bpID, "apps"); len(got) != 2 {
			t.Fatalf("after add %v", got)
		}
		for name, tc := range map[string]struct {
			body   string
			status int
		}{
			"empty":        {`{"data":[]}`, 422},
			"malformed":    {`{`, 422},
			"wrong type":   {`{"data":[{"type":"users","id":"u1"}]}`, 422},
			"unknown id":   {`{"data":[{"type":"apps","id":"nope"}]}`, 409},
			"unknown path": {`{"data":[{"type":"apps","id":"app1"}]}`, 404},
		} {
			path := "/v1/blueprints/" + bpID + "/relationships/apps"
			if name == "unknown path" {
				path = "/v1/blueprints/nope/relationships/apps"
			}
			if resp, _ := call(http.MethodPost, path, tc.body); resp.StatusCode != tc.status {
				t.Errorf("%s: %d, want %d", name, resp.StatusCode, tc.status)
			}
		}
		if resp, _ := call(http.MethodDelete, "/v1/blueprints/"+bpID+"/relationships/apps", `{"data":[{"type":"apps","id":"app1"}]}`); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("remove: %d", resp.StatusCode)
		}
		_, doc = call(http.MethodGet, "/v1/blueprints/"+bpID+"/relationships/apps", "")
		if data := doc["data"].([]any); len(data) != 1 || data[0].(map[string]any)["id"] != "app2" {
			t.Fatalf("after remove %v", data)
		}
		resp, doc = call(http.MethodPatch, "/v1/blueprints/"+bpID, `{"data":{"type":"blueprints","id":"`+bpID+`","attributes":{"name":"B2","description":"d2"},"relationships":{"users":{"data":[{"type":"users","id":"u1"}]}}}}`)
		if resp.StatusCode != http.StatusOK || doc["data"].(map[string]any)["attributes"].(map[string]any)["name"] != "B2" || len(h.srv.BlueprintLinks(bpID, "users")) != 1 {
			t.Fatalf("update %d %v", resp.StatusCode, doc)
		}
		if resp, _ := call(http.MethodPatch, "/v1/blueprints/"+bpID, `{"data":{"type":"blueprints","id":"x"}}`); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("mismatched id: %d", resp.StatusCode)
		}
		if resp, _ := call(http.MethodDelete, "/v1/blueprints/"+bpID, ""); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
	})
	t.Run("SeedingAndRecorder", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.srv.AddOrgDevice("S1", nil)
		h.srv.AddAppleCareCoverage("S1", "c1", nil)
		h.srv.AddAppleCareCoverage("NOPE", "c2", nil)
		h.srv.AddMDMDevice("M1", nil, nil)
		h.srv.AddUserGroup("g", nil, "u1")
		h.srv.AddOrganizationalUnit("ou", nil, "u1")
		h.srv.AddPackage("p", nil)
		h.srv.AddAuditEvent("e", map[string]any{"eventDateTime": time.Now().UTC()})
		c := h.client(t)
		ctx := context.Background()
		cov, err := c.ListAppleCareCoverage(ctx, "S1", axm.ListOptions{})
		if err != nil || len(cov.Items) != 1 {
			t.Fatalf("%v %v", cov, err)
		}
		det, err := c.GetMDMDeviceDetails(ctx, "M1", axm.GetOptions{})
		if err != nil || det.Attributes.SerialNumber != "M1" {
			t.Fatalf("%v %v", det, err)
		}
		mdm, err := c.ListMDMDevices(ctx, axm.ListOptions{})
		if err != nil || len(mdm.Items) != 1 || mdm.Items[0].Relationships["details"].Links.Related == "" {
			t.Fatalf("%v %v", mdm, err)
		}
		if _, err := c.ListUserGroupUsers(ctx, "g", axm.ListOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ListOrganizationalUnitUsers(ctx, "ou", axm.ListOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.GetPackage(ctx, "p", axm.GetOptions{}); err != nil {
			t.Fatal(err)
		}
		events, err := c.ListAuditEvents(ctx, axm.AuditEventQuery{Start: time.Now().Add(-time.Hour), End: time.Now().Add(time.Hour)})
		if err != nil || len(events.Items) != 1 {
			t.Fatalf("%v %v", events, err)
		}
		if reqs := h.srv.Requests(); len(reqs) < 7 || h.srv.LastRequest().Path != "/v1/auditEvents" || h.srv.LastRequest().Status != http.StatusOK {
			t.Fatalf("recorder %d %+v", len(reqs), h.srv.LastRequest())
		}
		h.srv.Reset()
		if len(h.srv.Requests()) != 0 || h.srv.LastRequest().Method != "" {
			t.Fatal("Reset")
		}
		h.srv.Assign("S1", "")
		if h.srv.AssignedServer("S1") != "" {
			t.Fatal("unassign via Assign")
		}
	})
	t.Run("AssertionHelpers", func(t *testing.T) {
		t.Parallel()
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tok, err := axm.Assertion(key, clientID, "kid", time.Now(), 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := axmtest.VerifyAssertion(tok, &key.PublicKey); err != nil {
			t.Fatal(err)
		}
		bad := map[string]string{
			"segments":   "a.b",
			"header b64": "!!!.e30.AAAA",
			"header":     base64.RawURLEncoding.EncodeToString([]byte("[")) + ".e30.AAAA",
			"claims b64": "e30.!!!.AAAA",
			"claims":     "e30." + base64.RawURLEncoding.EncodeToString([]byte("[")) + ".AAAA",
		}
		for name, token := range bad {
			if _, _, err := axmtest.DecodeAssertion(token); !errors.Is(err, axmtest.ErrAssertion) {
				t.Errorf("Decode %s: %v", name, err)
			}
		}
		parts := strings.Split(tok, ".")
		for name, token := range map[string]string{
			"segments":  "a.b",
			"sig b64":   parts[0] + "." + parts[1] + ".!!!",
			"sig short": parts[0] + "." + parts[1] + ".AAAA",
			"sig wrong": parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 64)),
		} {
			if err := axmtest.VerifyAssertion(token, &key.PublicKey); !errors.Is(err, axmtest.ErrAssertion) {
				t.Errorf("Verify %s: %v", name, err)
			}
		}
	})
}
