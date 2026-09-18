package proxyserver_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

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
}

func (s *profileSource) Fetch(_ context.Context, id mdm.EnrollmentID, revision string) ([]byte, configurationprofile.Info, error) {
	s.id, s.revision = id, revision
	return s.body, configurationprofile.Info{ContentType: "application/xml"}, nil
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
