package proxyclient_test

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/server/ddmadapter/proxyclient"
)

func TestProfileFetcherRejectsInvalidInput(t *testing.T) {
	if fetch, err := proxyclient.ConfigurationProfiles(proxyclient.Config{}); !errors.Is(err, proxyclient.ErrBadURL) || fetch != nil {
		t.Fatalf("invalid configuration: %v", err)
	}
	var calls atomic.Int32
	srv := serve(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	fetch, err := proxyclient.ConfigurationProfiles(proxyclient.Config{
		URL: srv.URL, AllowInsecureForTests: true, SendKey: sendKey, RecvKey: recvKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Invalid UTF-8 cannot be represented in the signed JSON request.
	id := mdm.EnrollmentID{ID: string([]byte{0xff}), Channel: mdm.ChannelDevice}
	if _, err := fetch(t.Context(), id, "revision"); err == nil {
		t.Fatal("malformed identity accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("malformed identity was forwarded")
	}
}

func TestProfileFetcherDoesNotFollowRedirects(t *testing.T) {
	var calls atomic.Int32
	target := serve(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	source := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	fetch, err := proxyclient.ConfigurationProfiles(proxyclient.Config{
		URL: source.URL, AllowInsecureForTests: true, SendKey: sendKey, RecvKey: recvKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fetch(t.Context(), mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}, "revision"); err == nil {
		t.Fatal("unsigned redirect accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("authenticated profile request followed redirect")
	}
}
