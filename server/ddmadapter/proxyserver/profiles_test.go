package proxyserver_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/internal/proxywire"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/proxyclient"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/proxyserver"
)

type profileSource struct {
	id       mdm.EnrollmentID
	revision string
	body     []byte
	err      error
	calls    int
}

func (s *profileSource) Fetch(_ context.Context, id mdm.EnrollmentID, revision string) ([]byte, configurationprofile.Info, error) {
	s.id, s.revision = id, revision
	s.calls++
	return s.body, configurationprofile.Info{ContentType: "application/xml"}, s.err
}

func TestProfilePrivateHopRejectsInvalidRequests(t *testing.T) {
	body, err := json.Marshal(proxywire.ConfigurationProfileRequest{
		Enrollment: mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}, Revision: "revision",
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := string(body)
	for _, test := range []struct {
		name, contentType, body string
		fetchErr                error
		status, calls           int
	}{
		{name: "content type", contentType: "application/xml", body: valid, status: 415},
		{name: "body limit", contentType: "application/json", body: strings.Repeat("x", 4097), status: 400},
		{name: "malformed JSON", contentType: "application/json", body: "{", status: 400},
		{name: "unknown member", contentType: "application/json", body: `{"Unexpected":true}`, status: 400},
		{name: "missing identity", contentType: "application/json", body: `{"Revision":"revision"}`, status: 400},
		{name: "missing profile", contentType: "application/json", body: valid, fetchErr: ddm.ErrNotFound, status: 404, calls: 1},
		{name: "invalid revision", contentType: "application/json", body: valid, fetchErr: ddm.ErrInvalid, status: 404, calls: 1},
		{name: "backend failure", contentType: "application/json", body: valid, fetchErr: errors.New("private storage detail"), status: 500, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &profileSource{err: test.fetchErr}
			h := mustHandler(t, proxyserver.Config{Backend: newStub(), ConfigurationProfiles: source})
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://ddm.example"+proxywire.ConfigurationProfilePath, strings.NewReader(test.body))
			r.Header.Set("Content-Type", test.contentType)
			signature, err := proxywire.SignRequest(recvKey, r, []byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set(proxywire.HeaderSignature, signature)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.status || w.Body.Len() != 0 || source.calls != test.calls {
				t.Fatalf("status=%d body=%q backend calls=%d", w.Code, w.Body.String(), source.calls)
			}
			if err := proxywire.VerifyBoundResponse(sendKey, w.Header().Get(proxywire.HeaderSignature), signature, w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
				t.Fatalf("error response not bound to authenticated request: %v", err)
			}
		})
	}
}

func TestProfilePrivateHop(t *testing.T) {
	source := &profileSource{body: bytes.Repeat([]byte("p"), 2<<20)}
	h, err := proxyserver.Handler(proxyserver.Config{Backend: newStub(), ConfigurationProfiles: source, ReplayStore: state.NewMemory(), RecvKey: recvKey, SendKey: sendKey, AllowInsecureForTests: true, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	fetch, err := proxyclient.ConfigurationProfiles(proxyclient.Config{URL: srv.URL, AllowInsecureForTests: true, SendKey: recvKey, RecvKey: sendKey})
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{ID: "parent:alice", ParentID: "parent", Channel: mdm.ChannelUserEnrollmentUser}
	response, err := fetch(t.Context(), id, "revision")
	if err != nil || response.Status != 200 || response.ContentType != "application/xml" || !bytes.Equal(response.Body, source.body) {
		t.Fatal("forwarded bytes", err)
	}
	if source.id != id || source.revision != "revision" {
		t.Fatal("identity lost", source.id)
	}
	// A resource request cannot bypass the signature or replay protections.
	body, err := json.Marshal(proxywire.ConfigurationProfileRequest{Enrollment: id, Revision: "revision"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+proxywire.ConfigurationProfilePath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	signature, err := proxywire.SignRequest(recvKey, request, body)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		signature string
		status    int
	}{{"", 401}, {signature, 200}, {signature, 401}} {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, request.URL.String(), bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(proxywire.HeaderSignature, test.signature)
		resp, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != test.status {
			t.Fatal(resp.StatusCode, test.status)
		}
	}
}
