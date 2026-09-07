package simulator

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/secrets"
)

func TestReauthenticationRetainsInterruptedCommandResult(t *testing.T) {
	var interrupted []byte
	requests, logins := 0, 0
	command, err := plist.Marshal(map[string]any{"CommandUUID": "command-1", "Command": map[string]any{"RequestType": "DeviceInformation", "Queries": []string{"DeviceName"}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, _ := io.ReadAll(r.Body)
		switch requests {
		case 1:
			if r.Header.Get("Authorization") != "Bearer first" {
				t.Error("missing initial bearer")
			}
			_, _ = w.Write(command)
		case 2:
			interrupted = body
			var result map[string]any
			_ = plist.Unmarshal(body, &result)
			if result["Status"] != "Acknowledged" || result["CommandUUID"] != "command-1" {
				t.Error(result)
			}
			w.Header().Set("WWW-Authenticate", `Bearer method="apple-as-web", url="https://idp.example/auth"`)
			w.WriteHeader(401)
		case 3:
			if !bytes.Equal(interrupted, body) {
				t.Error("interrupted command result was changed")
			}
			if r.Header.Get("Authorization") != "Bearer replacement" {
				t.Error("old bearer reused")
			}
		default:
			t.Error("unexpected extra request")
		}
	}))
	defer srv.Close()
	d := New("udid", WithURLs(srv.URL, srv.URL))
	d.ProductName = "iPhone17,2"
	d.EnrollmentID = "enrollment-unchanged"
	d.accountDriven = true
	d.accessToken = secrets.New([]byte("first"))
	d.reauthenticate = func(context.Context, AuthChallenge) (string, error) { logins++; return "replacement", nil }
	commands, err := d.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 || logins != 1 || len(commands) != 1 || d.EnrollmentID != "enrollment-unchanged" {
		t.Fatal(requests, logins, len(commands), d.EnrollmentID)
	}
}

func TestMacDeviceChannelOmitsBearerAndUserSendsIt(t *testing.T) {
	var authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { authorization = r.Header.Get("Authorization") }))
	defer srv.Close()
	d := New("mac", WithURLs(srv.URL, srv.URL))
	d.accountDriven = true
	d.accessToken = secrets.New([]byte("access"))
	for _, tc := range []struct {
		identity map[string]any
		want     string
	}{
		{map[string]any{"UDID": "mac", "MessageType": "Authenticate"}, ""},
		{map[string]any{"EnrollmentID": "mac-byod", "MessageType": "Authenticate"}, ""},
		{map[string]any{"UDID": "mac", "UserID": "user", "MessageType": "TokenUpdate"}, "Bearer access"},
		{map[string]any{"EnrollmentID": "mac-byod", "EnrollmentUserID": "user", "MessageType": "TokenUpdate"}, "Bearer access"},
	} {
		body, _ := plist.Marshal(tc.identity)
		if _, err := d.put(t.Context(), srv.URL, ContentTypeCheckin, body); err != nil {
			t.Fatal(err)
		}
		if authorization != tc.want {
			t.Fatal(authorization, tc.want)
		}
	}
	if _, err := d.RefreshAccountToken(t.Context()); err == nil {
		t.Fatal("missing refresh accepted")
	}
}
