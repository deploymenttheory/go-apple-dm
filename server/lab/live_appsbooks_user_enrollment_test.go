package lab

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// appsPostUserAppStatus sends an actual CMS-authenticated user-channel status
// exchange. It uses the enrolled simulator's identity, never an admin observation.
func appsPostUserAppStatus(ctx context.Context, s *Session, u *simulator.User, bearer string, rows []map[string]any) error {
	data, err := json.Marshal(map[string]any{"StatusItems": map[string]any{"app": map[string]any{"managed": map[string]any{"list": rows}}}})
	if err != nil {
		return err
	}
	raw, err := plist.Marshal(map[string]any{"MessageType": "DeclarativeManagement", "EnrollmentID": u.Device.EnrollmentID, "EnrollmentUserID": u.UserID, "Endpoint": "status", "Data": data})
	if err != nil {
		return err
	}
	signature, err := cms.Sign(raw, u.Device.Identity.Cert, u.Device.Identity.Key)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, u.Device.CheckinURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", simulator.ContentTypeCheckin)
	request.Header.Set(cms.HeaderName, cms.EncodeHeader(signature))
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := s.Env.Client.Do(request) // #nosec G704 -- Actual test-owned enrolled simulator check-in URL.
	if err != nil {
		return err
	}
	if err := response.Body.Close(); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("signed native user status HTTP %d", response.StatusCode)
	}
	return nil
}

// appsUserEnrollmentPump connects only the native channel named by an actual
// APNs request; an unwoken device or user channel is never polled.
func appsUserEnrollmentPump(t *testing.T, ctx context.Context, s *Session, d *simulator.Device, u *simulator.User) {
	t.Helper()
	pumpCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	seen := len(s.Env.APNS.Requests())
	go func() {
		defer close(stopped)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-pumpCtx.Done():
				return
			case <-tick.C:
			}
			requests := s.Env.APNS.Requests()
			for _, request := range requests[seen:] {
				var err error
				switch request.Token {
				case hex.EncodeToString(d.PushToken):
					_, err = d.Connect(pumpCtx)
				case hex.EncodeToString(u.PushToken):
					_, err = u.Connect(pumpCtx)
				default:
					t.Error("APNs wake did not match either selected native channel")
					return
				}
				if err != nil && pumpCtx.Err() == nil {
					t.Error(err)
					return
				}
			}
			seen = len(requests)
		}
	}()
	t.Cleanup(func() { cancel(); <-stopped })
}

// TestAppsBooksMacUserEnrollmentUsesOwnedDeclarativeVersion proves the privacy
// path with a real signed native user, user licensing and SQL-backed SDK workflow.
func TestAppsBooksMacUserEnrollmentUsesOwnedDeclarativeVersion(t *testing.T) {
	t.Parallel()
	s, f, c := appsRuntimeWith(t, nil, map[string]string{"DM_DISCOVERY": "Mac=mdm-byod"})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	f.mu.Lock()
	f.assigned = true
	f.mu.Unlock()
	d, err := s.Env.factoryDevice()
	if err != nil {
		t.Fatal(err)
	}
	unsentUDID := d.UDID
	d.SerialNumber = ""
	simulator.WithDDM(map[string]any{})(d)
	d.OSVersion, d.BuildVersion, d.ProductName = "27.0", "27A100", "Mac14,7"
	var onQuery atomic.Pointer[func() error]
	d.Responder = func(command *mdm.Command) simulator.Reply {
		if command.RequestType == "DeviceInformation" {
			if callback := onQuery.Swap(nil); callback != nil {
				if err := (*callback)(); err != nil {
					t.Error(err)
				}
			}
			var query commands.DeviceInformation
			if err := plist.Unmarshal(command.Raw, &struct {
				Command *commands.DeviceInformation `plist:"Command"`
			}{Command: &query}); err != nil {
				t.Error(err)
			}
			for _, key := range query.Queries {
				if key == "SerialNumber" || key == "UDID" {
					t.Error("User Enrollment requested a physical identifier")
				}
			}
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.DeviceInformationResponse{QueryResponses: commands.DeviceInformationResponseQueryResponses{OSVersion: new("27.0"), BuildVersion: new("27A100"), ProductName: new("Mac14,7"), IsSupervised: new(false)}}}
		}
		if command.RequestType == "InstalledApplicationList" {
			t.Error("privacy-forbidden installed-app query queued")
		}
		return simulator.AcknowledgeAll(command)
	}
	bearer := ""
	result, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "user@example.com", ModelFamily: "Mac", DiscoveryURL: s.Env.URL, Authenticate: func(ctx context.Context, challenge simulator.AuthChallenge) (string, error) {
		var token string
		var err error
		if challenge.Method == "apple-oauth2" {
			token, err = d.OAuth2CodeFlow(ctx, challenge, "user@example.com", func(ctx context.Context, location string) (string, error) { return browser(ctx, s.Env, location) })
		} else {
			location, browseErr := browser(ctx, s.Env, challenge.URL)
			if browseErr != nil {
				return "", browseErr
			}
			redirect, parseErr := url.Parse(location)
			if parseErr != nil {
				return "", parseErr
			}
			token = redirect.Query().Get("access-token")
		}
		bearer = token
		return token, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	d.UDID = ""
	if result.Chosen.Version != "mdm-byod" || d.EnrollmentID == "" || bearer == "" {
		t.Fatal("fixture did not perform actual account-driven User Enrollment")
	}
	u := d.User("alice", "alice", "Alice")
	if err = authenticateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	appsUserEnrollmentPump(t, ctx, s, d, u)
	parent := mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: d.EnrollmentID}
	selected := mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentUser, ID: d.EnrollmentID + ":" + u.UserID, ParentID: d.EnrollmentID}
	devicePath := "/enrollments/" + parent.Channel.String() + "/" + url.PathEscape(parent.ID)
	if _, err = appsNativeQuery(ctx, s, devicePath, &commands.DeviceInformation{Queries: []string{"OSVersion", "BuildVersion", "ProductName", "IsSupervised"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Env.api(ctx, "PUT", "/managed-content/users", map[string]any{"enrollment": selected, "clientUserId": c.User}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = u.SyncDDM(ctx); err != nil {
		t.Fatal(err)
	}
	if err = u.PostDDMStatus(ctx, true); err != nil {
		t.Fatal(err)
	}
	id := "private-user-" + randomID()
	base := "/managed-content/operations/" + id
	plan := managedcontent.Plan{LocationID: c.Location, Content: managedcontent.Content{Asset: c.Asset, Kind: "App"}, Target: managedcontent.Selection{Enrollment: selected, ClientUserID: c.User}, Method: managedcontent.DDM, MaxPages: c.MaxPages}
	var op managedcontent.Operation
	if err = s.Env.api(ctx, "PUT", base, map[string]any{"mode": managedcontent.InstallOnly, "plan": plan}, &op); err != nil {
		t.Fatal(err)
	}
	if err = waitLive(ctx, func() (bool, error) {
		if err := s.Env.api(ctx, "GET", base, nil, &op); err != nil {
			return false, err
		}
		return op.Phase == managedcontent.Dispatched, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = u.SyncDDM(ctx); err != nil {
		t.Fatal(err)
	}
	if err = u.PostDDMStatus(ctx, true); err != nil {
		t.Fatal(err)
	}
	var evidence managedcontent.Evidence
	if err = s.Env.api(ctx, "GET", base+"/evidence", nil, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Dispatch == nil || len(evidence.Dispatch.Declarations) == 0 || evidence.Dispatch.Serial != "" || evidence.Dispatch.ClientUserID != c.User {
		t.Fatal("native user dispatch lost independent user licensing scope")
	}
	declaration := evidence.Dispatch.Declarations[0].Identifier
	row := map[string]any{"identifier": "com.example.private", "declaration-identifier": declaration, "state": "managed", "version": "2.4"}
	if err = appsPostUserAppStatus(ctx, s, u, bearer, []map[string]any{row}); err != nil {
		t.Fatal(err)
	}
	s.Info = target.Info{Kind: target.KindDevice, UDID: d.EnrollmentID, UserID: u.UserID}
	installation := appsInstallation{Operation: id, BundleID: "com.example.private", Version: "2.4", Platform: "macOS", Identity: "scep", MaxAgeSeconds: 60}
	adeTestJSON(t, s.Env.Workspace.path("appsbooks", "installation.json"), installation)
	if err = liveAppsBooksInstallation(ctx, s); err != nil {
		t.Fatal(err)
	}
	proofs := adeTestEvidence(t, s, "appsbooks-installation-*/assertions.json")
	if len(proofs) != 1 || !bytes.Contains(proofs[0], []byte(`"verification_method":"current_owned_app_managed_list"`)) || !bytes.Contains(proofs[0], []byte(`"installed_application_list":"unexercised_unsupported_for_native_target"`)) || !bytes.Contains(proofs[0], []byte(`"macOS":"simulated"`)) {
		t.Fatal("privacy proof did not distinguish supported status from forbidden query")
	}
	for _, name := range []string{"missing version", "version removed during query", "foreign declaration", "removed", "stale"} {
		t.Run(name, func(t *testing.T) {
			altered := map[string]any{"identifier": "com.example.private", "declaration-identifier": declaration, "state": "managed", "version": "2.4"}
			switch name {
			case "missing version":
				delete(altered, "version")
			case "version removed during query":
				delete(altered, "version")
				callback := func() error { return appsPostUserAppStatus(ctx, s, u, bearer, []map[string]any{altered}) }
				onQuery.Store(&callback)
			case "foreign declaration":
				altered["declaration-identifier"] = "foreign"
			case "removed":
				altered = map[string]any{"identifier": "com.example.private", "_removed": true}
			case "stale":
				installation.MaxAgeSeconds = 1
				adeTestJSON(t, s.Env.Workspace.path("appsbooks", "installation.json"), installation)
			}
			if name != "stale" && name != "version removed during query" {
				if err := appsPostUserAppStatus(ctx, s, u, bearer, []map[string]any{altered}); err != nil {
					t.Fatal(err)
				}
			} else if name == "stale" {
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Second + 10*time.Millisecond):
				}
			}
			if err := liveAppsBooksInstallation(ctx, s); !errors.Is(err, ErrBlocked) {
				t.Fatal("unproven private app status passed", err)
			}
			installation.MaxAgeSeconds = 60
			adeTestJSON(t, s.Env.Workspace.path("appsbooks", "installation.json"), installation)
			if err := appsPostUserAppStatus(ctx, s, u, bearer, []map[string]any{row}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if f.postCount() != 0 {
		t.Fatal("native inspection changed user licensing")
	}
	for _, command := range d.Commands() {
		if command.RequestType == "InstalledApplicationList" || command.RequestType == "InstallApplication" {
			t.Fatal("User Enrollment emitted forbidden or mutating app command")
		}
	}
	for _, source := range adeTestEvidence(t, s, "appsbooks-installation-*/*-request.plist") {
		if strings.Contains(string(source), "SerialNumber") || strings.Contains(string(source), unsentUDID) {
			t.Fatal("private query disclosed physical identity")
		}
	}
}
