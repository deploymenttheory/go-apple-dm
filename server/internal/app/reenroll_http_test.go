package app_test

import (
	"bytes"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func TestSignedSQLiteReenrollmentAfterProfileRemoval(t *testing.T) {
	ca, err := testpki.NewCA("re-enrollment transport")
	if err != nil {
		t.Fatal(err)
	}
	old, err := ca.Issue("mac", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := ca.Issue("mac-new", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	a := build(t, app.Config{Role: app.RoleMDM, Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "mdm.sqlite"), CARoots: roots, AllowReenroll: true})
	request := func(identity *testpki.Identity, body map[string]any, want int, contentType string) []byte {
		t.Helper()
		raw, err := plist.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		signature, err := cms.Sign(raw, identity.Cert, identity.Key)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/mdm", bytes.NewReader(raw))
		r.Header.Set("Content-Type", contentType)
		r.Header.Set(cms.HeaderName, cms.EncodeHeader(signature))
		w := httptest.NewRecorder()
		a.Handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%v returned %d, want %d: %s", body["MessageType"], w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	auth := map[string]any{"MessageType": "Authenticate", "UDID": "mac", "Topic": "com.apple.mgmt.test", "ProductName": "Mac16,1", "OSVersion": "26.6.2"}
	token := map[string]any{"MessageType": "TokenUpdate", "UDID": "mac", "Topic": "com.apple.mgmt.test", "Token": []byte{1, 2, 3}, "PushMagic": "magic"}
	userToken := map[string]any{"MessageType": "TokenUpdate", "UDID": "mac", "UserID": "installing-user", "Topic": "com.apple.mgmt.test", "Token": []byte{4, 5, 6}, "PushMagic": "user-magic"}
	request(old, auth, 200, httpapi.ContentTypeCheckin)
	request(old, token, 200, httpapi.ContentTypeCheckin)
	request(old, userToken, 200, httpapi.ContentTypeCheckin)
	request(old, map[string]any{"MessageType": "CheckOut", "UDID": "mac"}, 200, httpapi.ContentTypeCheckin)
	request(old, auth, 403, httpapi.ContentTypeCheckin)
	request(fresh, auth, 200, httpapi.ContentTypeCheckin)
	request(fresh, token, 200, httpapi.ContentTypeCheckin)
	request(old, token, 403, httpapi.ContentTypeCheckin)
	request(old, userToken, 403, httpapi.ContentTypeCheckin)
	request(fresh, userToken, 200, httpapi.ContentTypeCheckin)
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "mac"}
	cmd, err := mdm.NewCommand(&commands.ProfileList{}, mdm.WithUUID("after-reenrollment"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	raw := request(fresh, map[string]any{"Status": "Idle", "UDID": "mac"}, 200, httpapi.ContentTypeConnect)
	got, err := mdm.DecodeCommand(raw)
	if err != nil || got.UUID != cmd.UUID {
		t.Fatalf("new enrollment cannot receive a command: %v %v", got, err)
	}
	userID := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "mac:installing-user", ParentID: "mac"}
	userCmd, err := mdm.NewCommand(&commands.ProfileList{}, mdm.WithUUID("user-after-reenrollment"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Core.Enqueue(t.Context(), []mdm.EnrollmentID{userID}, userCmd, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	userIdle := map[string]any{"Status": "Idle", "UDID": "mac", "UserID": "installing-user"}
	request(old, userIdle, 403, httpapi.ContentTypeConnect)
	raw = request(fresh, userIdle, 200, httpapi.ContentTypeConnect)
	got, err = mdm.DecodeCommand(raw)
	if err != nil || got.UUID != userCmd.UUID {
		t.Fatalf("returning user cannot receive a command: %v %v", got, err)
	}
}
