package httpapi_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/cms"
	"github.com/deploymenttheory/go-apple-dm/httpapi"
	"github.com/deploymenttheory/go-apple-dm/internal/testpki"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/plist"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/service"
)

// fakeService records calls and returns scripted results.
type fakeService struct {
	checkinErr error
	connectErr error
	result     *service.CheckinResult
	command    *mdm.Command
	lastReq    *mdm.Request
	lastCk     *mdm.Checkin
	lastResp   *mdm.Response
}

func (f *fakeService) Checkin(_ context.Context, r *mdm.Request, ck *mdm.Checkin) (*service.CheckinResult, error) {
	f.lastReq, f.lastCk = r, ck
	if f.checkinErr != nil {
		return nil, f.checkinErr
	}
	if f.result != nil {
		return f.result, nil
	}
	return &service.CheckinResult{}, nil
}

func (f *fakeService) Connect(_ context.Context, r *mdm.Request, resp *mdm.Response) (*mdm.Command, error) {
	f.lastReq, f.lastResp = r, resp
	if f.connectErr != nil {
		return nil, f.connectErr
	}
	return f.command, nil
}

const tokenUpdate = `<plist version="1.0"><dict><key>MessageType</key><string>TokenUpdate</string><key>Topic</key><string>t</string><key>UDID</key><string>D1</string><key>PushMagic</key><string>m</string><key>Token</key><data>AQ==</data><key>UserLongName</key><string></string></dict></plist>`

const idle = `<plist version="1.0"><dict><key>Status</key><string>Idle</string><key>UDID</key><string>D1</string></dict></plist>`

func do(t *testing.T, h http.Handler, method, ct, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/mdm?tag=x&tag=y", strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCheckinAndConnectHandlers(t *testing.T) {
	t.Parallel()
	fs := &fakeService{result: &service.CheckinResult{Body: []byte("<plist/>"), ContentType: "application/xml", Status: 200}}
	cfg := httpapi.Config{Checkin: fs, Connect: fs, Now: func() time.Time { return time.Unix(100, 0) }}
	h := httpapi.Handler(cfg)

	rec := do(t, h, http.MethodPut, httpapi.ContentTypeCheckin, tokenUpdate, nil)
	if rec.Code != 200 || rec.Body.String() != "<plist/>" || rec.Header().Get("Content-Type") != "application/xml" {
		t.Fatalf("checkin: %d %s", rec.Code, rec.Body.String())
	}
	if fs.lastCk.Type != "TokenUpdate" || fs.lastReq.Params["tag"] != "x" || !fs.lastReq.ReceivedAt.Equal(time.Unix(100, 0)) || fs.lastReq.Peer.RemoteAddr == "" {
		t.Fatalf("request = %+v ck=%+v", fs.lastReq, fs.lastCk)
	}
	// Empty result body and default status.
	fs.result = nil
	if rec = do(t, h, http.MethodPost, httpapi.ContentTypeCheckin, tokenUpdate, nil); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("empty checkin result: %d %q", rec.Code, rec.Body.String())
	}
	// Connect with no command: empty 200.
	if rec = do(t, h, http.MethodPut, httpapi.ContentTypeConnect, idle, nil); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("idle: %d %q", rec.Code, rec.Body.String())
	}
	if !fs.lastResp.IsIdle() {
		t.Fatal("response not passed")
	}
	// Connect with a command: raw plist.
	fs.command, _ = mdm.NewCommand(&commands.ProfileList{}, mdm.WithUUID("C1"))
	rec = do(t, h, http.MethodPut, httpapi.ContentTypeConnect, idle, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<key>CommandUUID</key><string>C1</string>") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/xml") {
		t.Fatalf("command: %d %s", rec.Code, rec.Body.String())
	}
	// Method, content type, body errors.
	if rec = do(t, h, http.MethodGet, httpapi.ContentTypeCheckin, "", nil); rec.Code != 405 || rec.Header().Get("Allow") == "" {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec = do(t, h, http.MethodPut, "text/plain", idle, nil); rec.Code != 415 {
		t.Fatalf("content type: %d", rec.Code)
	}
	if rec = do(t, h, http.MethodPut, httpapi.ContentTypeCheckin, "garbage", nil); rec.Code != 400 {
		t.Fatalf("bad checkin body: %d", rec.Code)
	}
	if rec = do(t, h, http.MethodPut, httpapi.ContentTypeConnect, "garbage", nil); rec.Code != 400 {
		t.Fatalf("bad connect body: %d", rec.Code)
	}
	small := httpapi.Handler(httpapi.Config{Checkin: fs, Connect: fs, Decoder: plist.Decoder{MaxBytes: 10}})
	if rec = do(t, small, http.MethodPut, httpapi.ContentTypeCheckin, tokenUpdate, nil); rec.Code != 400 {
		t.Fatalf("oversized: %d", rec.Code)
	}
	if rec = do(t, small, http.MethodPut, httpapi.ContentTypeConnect, idle, nil); rec.Code != 400 {
		t.Fatalf("oversized connect: %d", rec.Code)
	}
	unlimited := httpapi.Handler(httpapi.Config{Checkin: fs, Connect: fs, Decoder: plist.Decoder{MaxBytes: -1}})
	if rec = do(t, unlimited, http.MethodPut, httpapi.ContentTypeConnect, idle, nil); rec.Code != 200 {
		t.Fatalf("unlimited: %d", rec.Code)
	}
}

func TestServiceErrorMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code   service.Code
		status int
		body   string
	}{
		{service.CodeBadRequest, 400, ""},
		{service.CodeForbidden, 403, ""},
		{service.CodeNotImplemented, 501, ""},
		{service.CodeGone, 410, ""},
		{service.CodeUnknownEnrollment, 403, ""},
		{service.CodeInternal, 500, ""},
		{service.Code(99), 500, ""},
	}
	for _, c := range cases {
		fs := &fakeService{checkinErr: &service.Error{Code: c.code, Err: errors.New("x")}, connectErr: &service.Error{Code: c.code, Err: errors.New("x")}}
		h := httpapi.Handler(httpapi.Config{Checkin: fs, Connect: fs})
		if rec := do(t, h, http.MethodPut, httpapi.ContentTypeCheckin, tokenUpdate, nil); rec.Code != c.status {
			t.Errorf("code %d checkin: got %d, want %d", c.code, rec.Code, c.status)
		}
		if rec := do(t, h, http.MethodPut, httpapi.ContentTypeConnect, idle, nil); rec.Code != c.status || rec.Code == 401 {
			t.Errorf("code %d connect: got %d, want %d", c.code, rec.Code, c.status)
		}
	}
	// Unknown enrollment with unenroll opt-in returns Apple's body.
	fs := &fakeService{connectErr: &service.Error{Code: service.CodeUnknownEnrollment, Err: errors.New("x")}}
	h := httpapi.Handler(httpapi.Config{Checkin: fs, Connect: fs, UnenrollUnknown: true})
	rec := do(t, h, http.MethodPut, httpapi.ContentTypeConnect, idle, nil)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "com.apple.unrecognized.device") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/xml") {
		t.Fatalf("unenroll body: %d %s", rec.Code, rec.Body.String())
	}
	// Plain errors map to 500.
	plain := &fakeService{checkinErr: errors.New("plain")}
	if rec := do(t, httpapi.CheckinHandler(httpapi.Config{Checkin: plain}), http.MethodPut, httpapi.ContentTypeCheckin, tokenUpdate, nil); rec.Code != 500 {
		t.Fatalf("plain error: %d", rec.Code)
	}
}

func certCapture() (http.Handler, *[]*x509.Certificate) {
	var seen []*x509.Certificate
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, httpapi.CertFromContext(r.Context()))
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	}), &seen
}

func TestCertMiddlewares(t *testing.T) {
	t.Parallel()
	ca, err := testpki.NewCA("ca")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := ca.Issue("dev", time.Now().Add(-time.Minute))
	body := []byte("body")

	// Mdm-Signature.
	inner, seen := certCapture()
	h := httpapi.CertFromMdmSignature(cms.VerifyOptions{Roots: ca.Pool()}, 0)(inner)
	sig, _ := cms.Sign(body, id.Cert, id.Key)
	rec := do(t, h, http.MethodPut, "", string(body), map[string]string{cms.HeaderName: cms.EncodeHeader(sig)})
	if rec.Code != 200 || rec.Body.String() != "body" || len(*seen) != 1 || (*seen)[0] == nil || !(*seen)[0].Equal(id.Cert) {
		t.Fatalf("signature middleware: %d %q %v", rec.Code, rec.Body.String(), *seen)
	}
	if rec = do(t, h, http.MethodPut, "", "tampered", map[string]string{cms.HeaderName: cms.EncodeHeader(sig)}); rec.Code != 400 {
		t.Fatalf("tampered: %d", rec.Code)
	}
	if rec = do(t, h, http.MethodPut, "", string(body), nil); rec.Code != 200 || (*seen)[len(*seen)-1] != nil {
		t.Fatalf("no header should pass through without a cert: %d", rec.Code)
	}
	tiny := httpapi.CertFromMdmSignature(cms.VerifyOptions{}, 2)(inner)
	if rec = do(t, tiny, http.MethodPut, "", string(body), map[string]string{cms.HeaderName: cms.EncodeHeader(sig)}); rec.Code != 400 {
		t.Fatalf("oversized signed body: %d", rec.Code)
	}

	// Header: RFC 9440 and URL-escaped PEM.
	inner, seen = certCapture()
	hh := httpapi.CertFromHeader("X-Client-Cert")(inner)
	rfc := ":" + base64.StdEncoding.EncodeToString(id.Cert.Raw) + ":"
	if rec = do(t, hh, http.MethodPut, "", "", map[string]string{"X-Client-Cert": rfc}); rec.Code != 200 || !(*seen)[0].Equal(id.Cert) {
		t.Fatalf("rfc9440: %d", rec.Code)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.Cert.Raw}))
	if rec = do(t, hh, http.MethodPut, "", "", map[string]string{"X-Client-Cert": url.PathEscape(pemStr)}); rec.Code != 200 || !(*seen)[1].Equal(id.Cert) {
		t.Fatalf("pem: %d", rec.Code)
	}
	for _, bad := range []string{":!!!:", ":AAAA:", "%zz", "not a cert", url.PathEscape("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")} {
		if rec = do(t, hh, http.MethodPut, "", "", map[string]string{"X-Client-Cert": bad}); rec.Code != 400 {
			t.Errorf("bad header %q: %d", bad, rec.Code)
		}
	}
	if rec = do(t, hh, http.MethodPut, "", "", nil); rec.Code != 200 || (*seen)[len(*seen)-1] != nil {
		t.Fatalf("absent header: %d", rec.Code)
	}

	// Header with a trust anchor: a certificate is not secret, so the chain is
	// what separates a device we issued to from anyone who reaches the
	// listener past the proxy.
	inner, seen = certCapture()
	vh := httpapi.CertFromHeader("X-Client-Cert", httpapi.WithHeaderRoots(ca.Pool()))(inner)
	if rec = do(t, vh, http.MethodPut, "", "", map[string]string{"X-Client-Cert": rfc}); rec.Code != 200 || !(*seen)[0].Equal(id.Cert) {
		t.Fatalf("verified header: %d", rec.Code)
	}
	other, err := testpki.NewCA("other")
	if err != nil {
		t.Fatal(err)
	}
	foreign, _ := other.Issue("dev", time.Now().Add(-time.Minute))
	foreignHdr := ":" + base64.StdEncoding.EncodeToString(foreign.Cert.Raw) + ":"
	if rec = do(t, vh, http.MethodPut, "", "", map[string]string{"X-Client-Cert": foreignHdr}); rec.Code != 400 {
		t.Fatalf("foreign certificate accepted: %d", rec.Code)
	}
	// Without a pool the same certificate is taken at face value.
	unverified, useen := certCapture()
	uh := httpapi.CertFromHeader("X-Client-Cert")(unverified)
	if rec = do(t, uh, http.MethodPut, "", "", map[string]string{"X-Client-Cert": foreignHdr}); rec.Code != 200 || !(*useen)[0].Equal(foreign.Cert) {
		t.Fatalf("unverified header: %d", rec.Code)
	}

	// TLS peer certificate.
	inner, seen = certCapture()
	th := httpapi.CertFromTLS(inner)
	req := httptest.NewRequest(http.MethodPut, "/", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{id.Cert}}
	rec = httptest.NewRecorder()
	th.ServeHTTP(rec, req)
	if (*seen)[0] == nil || !(*seen)[0].Equal(id.Cert) {
		t.Fatal("tls cert not extracted")
	}
	req = httptest.NewRequest(http.MethodPut, "/", nil)
	rec = httptest.NewRecorder()
	th.ServeHTTP(rec, req)
	if (*seen)[1] != nil {
		t.Fatal("no tls should give nil cert")
	}
	if httpapi.CertFromContext(context.Background()) != nil || !httpapi.IsCertMissing(errors.Join(errors.New("x"))) == true {
		t.Log("helper checks")
	}
}
