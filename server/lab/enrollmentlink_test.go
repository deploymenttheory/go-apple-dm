package lab

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

type rewriteResponse struct {
	base       http.RoundTripper
	match      func(*http.Request) bool
	occurrence int // 1-based occurrence of a matching request to rewrite
	seen       int
	status     int    // replacement status; zero keeps the server's
	body       string // replacement body; empty keeps the server's
}

// RoundTrip rewrites the selected response and passes every other exchange through.
func (f *rewriteResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := f.base.RoundTrip(r)
	if err != nil || !f.match(r) {
		return resp, err
	}
	f.seen++
	if f.seen != f.occurrence {
		return resp, nil
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if f.status != 0 {
		resp.StatusCode = f.status
	}
	if f.body != "" {
		b = []byte(f.body)
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	resp.ContentLength = -1
	return resp, nil
}

// request matches a method and a path predicate.
func request(method string, path func(string) bool) func(*http.Request) bool {
	return func(r *http.Request) bool { return r.Method == method && path(r.URL.Path) }
}

// Path predicates for the enrollment-link exchanges.
var (
	adminLinks = func(p string) bool { return strings.HasSuffix(p, "/admin/v1/enrollment-links") }
	landing    = func(p string) bool {
		return strings.HasPrefix(p, "/enroll/links/") && !strings.HasSuffix(p, "/profile")
	}
	download = func(p string) bool {
		return strings.HasPrefix(p, "/enroll/links/") && strings.HasSuffix(p, "/profile")
	}
)

// TestEnrollmentLinkScenarioRejectsWrongEvidence checks that E2E-032 fails when the link,
// landing page, profile, reuse refusal or listed state is not what the server must return.
func TestEnrollmentLinkScenarioRejectsWrongEvidence(t *testing.T) {
	w := testWorkspace(t, "simulated")
	e, err := Start(t.Context(), w, "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	base := e.Client.Transport
	if err = enrollmentLinkEnroll(t.Context(), e, ""); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	for _, tc := range []struct {
		name, want string
		fault      *rewriteResponse
	}{
		{"foreign URL", "not under", &rewriteResponse{match: request("POST", adminLinks), occurrence: 1, body: `{"ID":"x","URL":"https://mdm.example/other"}`}},
		{"landing status", "landing page returned HTTP 503", &rewriteResponse{match: request("GET", landing), occurrence: 1, status: http.StatusServiceUnavailable}},
		{"landing link", "without the profile link", &rewriteResponse{match: request("GET", landing), occurrence: 1, body: "<html></html>"}},
		{"profile status", "profile download returned HTTP 410", &rewriteResponse{match: request("GET", download), occurrence: 1, status: http.StatusGone}},
		{"profile content", "lab:", &rewriteResponse{match: request("GET", download), occurrence: 1, body: "not a profile"}},
		{"reuse accepted", "redeemed link returned HTTP 200", &rewriteResponse{match: request("GET", download), occurrence: 2, status: http.StatusOK}},
		{"listed state", "not listed as redeemed", &rewriteResponse{match: request("GET", adminLinks), occurrence: 1, body: `{"Items":[]}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.fault.base = base
			e.Client.Transport = tc.fault
			defer func() { e.Client.Transport = base }()
			err := enrollmentLinkEnroll(t.Context(), e, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
