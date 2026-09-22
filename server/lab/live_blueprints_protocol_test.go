package lab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

// These fixtures test the acceptance runner's failure detection and cleanup.
// Their synthetic status and command responses are not device acceptance evidence.
func TestLiveBlueprintProtocolEvidenceAndCleanup(t *testing.T) {
	for _, failure := range []string{"", "user", "inventory", "wrong OS", "user unavailable", "upload", "upload identity", "publish", "missing compilation", "notify", "assignment", "status", "stale status", "invalid status", "changed identical publication", "wrong source", "wrong UUID", "missing profile", "duplicate profile", "unchanged token", "compatibility disabled", "compatibility missing", "incompatible delivered", "delete"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			f := &blueprintProtocolFixture{t: t, fault: failure, cancel: cancel, uploads: map[string]*profile.Profile{}}
			server := httptest.NewServer(http.HandlerFunc(f.serve))
			defer server.Close()
			e := &Environment{
				Instance: Instance{URL: server.URL, Mode: "live"}, Client: server.Client(),
				Workspace: &Workspace{Directory: t.TempDir()}, InstallingUserID: "user",
			}
			err := liveBlueprints(failure == "user" || failure == "user unavailable")(ctx, e, "mac")
			if (failure == "" || failure == "user") != (err == nil) {
				t.Fatalf("failure %q: %v", failure, err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.assigned || f.record.Revision != "" {
				t.Fatal("temporary assignment or Blueprint survived cleanup")
			}
		})
	}
}

type blueprintProtocolFixture struct {
	t                                     *testing.T
	mu                                    sync.Mutex
	fault                                 string
	used                                  bool
	cancel                                context.CancelFunc
	record                                blueprints.Record
	assigned                              bool
	uploads                               map[string]*profile.Profile
	command                               string
	faultMethod, faultPrefix, faultSuffix string
	faultOccurrence, requests             int
}

// fail consumes the named fixture fault at most once.
func (f *blueprintProtocolFixture) fail(name string) bool {
	if f.fault != name || f.used {
		return false
	}
	f.used = true
	return true
}

// encode writes a JSON fixture response, reporting encoding or write errors to the test.
func (f *blueprintProtocolFixture) encode(w http.ResponseWriter, value any) {
	f.t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		f.t.Error(err)
		w.WriteHeader(500)
		return
	}
	if _, err := w.Write(b); err != nil {
		f.t.Error(err)
	}
}

// serve serves the blueprint lab protocol fixture with configurable publication, inventory,
// status, and cleanup faults.
func (f *blueprintProtocolFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/admin/v1")
	if f.faultMethod == r.Method && strings.HasPrefix(path, f.faultPrefix) && strings.HasSuffix(path, f.faultSuffix) {
		f.requests++
		if f.requests == f.faultOccurrence {
			f.used = true
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	switch {
	case path == "/configuration-profiles":
		if f.fail("upload") {
			w.WriteHeader(503)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Error(err)
			w.WriteHeader(500)
			return
		}
		parsed, err := profile.Parse(raw, profile.ParseOptions{})
		if err != nil {
			f.t.Error(err)
			w.WriteHeader(400)
			return
		}
		h := sha256.Sum256(raw)
		revision := hex.EncodeToString(h[:])
		f.uploads[revision] = parsed.Profile
		info := configurationprofile.Info{Revision: revision, PayloadIdentifier: parsed.Profile.Identifier, PayloadUUID: parsed.Profile.UUID}
		if f.fail("upload identity") {
			info.PayloadUUID = "different"
		}
		f.encode(w, info)
	case strings.HasPrefix(path, "/blueprints/"):
		if r.Method == "DELETE" {
			if f.fail("delete") {
				w.WriteHeader(503)
				return
			}
			f.record, f.assigned = blueprints.Record{}, false
			w.WriteHeader(204)
			return
		}
		if f.fail("publish") {
			w.WriteHeader(503)
			return
		}
		var spec blueprint.Spec
		if err := json.UnmarshalRead(r.Body, &spec); err != nil {
			f.t.Error(err)
			w.WriteHeader(400)
			return
		}
		if f.fail("missing compilation") {
			f.encode(w, map[string]string{"Revision": "incomplete"})
			return
		}
		if f.record.Revision != "" && r.Header.Get("If-Match") != `"`+f.record.Revision+`"` {
			f.t.Error("update omitted its expected revision")
			w.WriteHeader(409)
			return
		}
		if !reflect.DeepEqual(spec, f.record.Spec) || f.fail("changed identical publication") {
			opts := blueprint.Options{ConfigurationProfiles: map[string]blueprint.ConfigurationProfileDescriptor{}}
			for revision := range f.uploads {
				opts.ConfigurationProfiles[revision] = blueprint.ConfigurationProfileDescriptor{URL: "https://profiles.example/" + revision, ContentType: "application/xml", Size: 1024, SHA256: revision}
			}
			compiled, err := blueprint.Compile(spec, opts)
			if err != nil {
				f.t.Error(err)
				w.WriteHeader(400)
				return
			}
			f.record = blueprints.Record{Spec: spec, Revision: randomID(), Compiled: compiled}
		}
		f.encode(w, f.record)
	case path == "/notify":
		if f.fail("notify") {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	case strings.Contains(path, "/blueprints/"):
		if f.fail("assignment") {
			w.WriteHeader(503)
			return
		}
		if strings.Contains(path, "/user/") && r.URL.Query().Get("parent") != "mac" {
			f.t.Error("user assignment omitted parent")
		}
		f.assigned = r.Method == "PUT"
		w.WriteHeader(204)
	case strings.HasSuffix(path, "/compatibility"):
		report := ddm.CompatibilityReport{Enforced: !f.fail("compatibility disabled")}
		if !f.fail("compatibility missing") {
			report.Issues = []ddm.CompatibilityIssue{{Identifier: f.record.Compiled.Identifiers["preferences"], Withheld: true, Reason: "unsupported-target"}}
		}
		f.encode(w, report)
	case strings.HasSuffix(path, "/status"):
		if f.fail("status") {
			w.WriteHeader(503)
			return
		}
		rows := []ddm.DeclarationStatus{}
		if f.assigned && f.record.Compiled != nil {
			for _, raw := range f.record.Compiled.Publication.Declarations {
				var declaration struct {
					Identifier, Type string
					Payload          map[string]any
				}
				if err := json.Unmarshal(raw, &declaration); err != nil {
					f.t.Error(err)
					return
				}
				name := ""
				for key, id := range f.record.Compiled.Identifiers {
					if id == declaration.Identifier {
						name = key
					}
				}
				for key, id := range f.record.Compiled.Activations {
					if id == declaration.Identifier {
						name = key
					}
				}
				if f.assetReference() && (name == "preferences" || name == "conditional") {
					if name != "preferences" || !f.fail("incompatible delivered") {
						continue
					}
				}
				active := name != "preferences" && name != "conditional" || f.profileActive()
				row := ddm.DeclarationStatus{Identifier: declaration.Identifier, Active: active, Valid: "valid", ServerToken: ddm.TokenFor(raw), LastSeen: time.Now().UTC()}
				if f.fault == "unchanged token" && name == "preferences" {
					row.ServerToken = "never-changes"
				}
				if f.fail("stale status") {
					row.LastSeen = time.Now().Add(-time.Hour)
					f.cancel()
				}
				if f.fail("invalid status") {
					row.Valid = "invalid"
					f.cancel()
				}
				rows = append(rows, row)
			}
		}
		f.encode(w, rows)
	case strings.HasSuffix(path, "/commands"):
		if f.fail("inventory") {
			w.WriteHeader(503)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Error(err)
			return
		}
		var command struct{ Command struct{ RequestType string } }
		if err := plist.Unmarshal(raw, &command); err != nil {
			f.t.Error(err)
			return
		}
		f.command = command.Command.RequestType
		f.encode(w, map[string]int{"Queued": 1})
	case strings.HasSuffix(path, "/push"):
		f.encode(w, map[string]bool{"Sent": true})
	case strings.HasSuffix(path, "/result"):
		var response any
		if f.command == "DeviceInformation" {
			version := "26.6.2"
			if f.fail("wrong OS") {
				version = "27.0"
			}
			response = map[string]any{"QueryResponses": map[string]string{"OSVersion": version, "BuildVersion": "25G83"}}
		} else {
			profiles := []map[string]string{{"PayloadIdentifier": "com.example.preexisting", "PayloadUUID": "preexisting", "Source": "MDM"}}
			if f.assigned && f.profileActive() && !f.assetReference() {
				p := f.uploads[f.record.Spec.Declarations[1].ConfigurationProfile.Revision]
				item := map[string]string{"PayloadIdentifier": p.Identifier, "PayloadUUID": p.UUID, "PayloadDisplayName": p.DisplayName, "Source": "Declarative Device Management"}
				if f.fail("wrong source") {
					item["Source"] = "MDM"
				}
				if f.fail("wrong UUID") {
					item["PayloadUUID"] = "different"
				}
				if !f.fail("missing profile") {
					profiles = append(profiles, item)
				}
				if f.fail("duplicate profile") {
					profiles = append(profiles, item)
				}
			}
			response = map[string]any{"ProfileList": profiles}
		}
		raw, err := plist.Marshal(response)
		if err != nil {
			f.t.Error(err)
			return
		}
		f.encode(w, map[string]any{"Status": "Acknowledged", "Response": raw})
	default:
		f.encode(w, map[string]any{"Enabled": !(strings.Contains(path, "/user/") && f.fail("user unavailable")), "ParentID": "mac", "TokenUpdatedAt": time.Now()})
	}
}

// assetReference reports whether the fixture blueprint selects a profile asset reference.
func (f *blueprintProtocolFixture) assetReference() bool {
	return len(f.record.Spec.Declarations) > 1 && f.record.Spec.Declarations[1].ConfigurationProfile.UseProfileAssetReference
}

// profileActive reports whether the fixture's conditional profile activation uses TRUEPREDICATE.
func (f *blueprintProtocolFixture) profileActive() bool {
	return len(f.record.Spec.Activations) > 1 && f.record.Spec.Activations[1].Predicate == "TRUEPREDICATE"
}

// Every mutation and removal boundary must preserve failure, including failures
// after a previously successful installation. The fixture recovers for cleanup.
func TestLiveBlueprintRequestFailures(t *testing.T) {
	cases := []struct {
		method, prefix, suffix string
		occurrence             int
	}{
		{"GET", "/enrollments/", "mac", 2},
		{"POST", "/configuration-profiles", "", 2},
		{"PUT", "/blueprints/", "", 2},
		{"PUT", "/blueprints/", "", 3},
		{"PUT", "/blueprints/", "", 4},
		{"PUT", "/blueprints/", "", 5},
		{"PUT", "/blueprints/", "", 6},
		{"PUT", "/blueprints/", "", 7},
		{"PUT", "/enrollments/", "", 2},
		{"PUT", "/enrollments/", "", 3},
		{"DELETE", "/enrollments/", "", 1},
		{"POST", "/notify", "", 2},
		{"POST", "/notify", "", 10},
		{"GET", "/enrollments/", "/status", 2},
		{"GET", "/enrollments/", "/status", 3},
		{"GET", "/enrollments/", "/status", 4},
		{"GET", "/enrollments/", "/status", 5},
		{"GET", "/enrollments/", "/status", 6},
		{"GET", "/enrollments/", "/status", 7},
		{"GET", "/enrollments/", "/status", 8},
		{"GET", "/enrollments/", "/status", 9},
		{"GET", "/enrollments/", "/compatibility", 1},
		{"GET", "/enrollments/", "/result", 3},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s_%s_%s_%d", tc.method, tc.prefix, tc.suffix, tc.occurrence), func(t *testing.T) {
			t.Parallel()
			f := &blueprintProtocolFixture{t: t, uploads: map[string]*profile.Profile{}, faultMethod: tc.method, faultPrefix: tc.prefix, faultSuffix: tc.suffix, faultOccurrence: tc.occurrence}
			server := httptest.NewServer(http.HandlerFunc(f.serve))
			defer server.Close()
			e := &Environment{Instance: Instance{URL: server.URL, Mode: "live"}, Client: server.Client(), Workspace: &Workspace{Directory: t.TempDir()}}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			err := liveBlueprints(false)(ctx, e, "mac")
			f.mu.Lock()
			defer f.mu.Unlock()
			if err == nil || !f.used {
				t.Fatalf("request failure was not detected: %v, injected=%v", err, f.used)
			}
			if f.assigned || f.record.Revision != "" {
				t.Fatal("request failure prevented cleanup")
			}
		})
	}
}
