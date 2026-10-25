package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

func TestAppsBooksIncompletePagesAndPrerequisitesNeverMutate(t *testing.T) {
	for _, name := range []string{"missing version", "version drift", "skipped page", "page bound", "unowned location", "no callback", "Book", "not revocable", "missing asset", "stock unknown", "inactive user", "foreign assignment", "revised location"} {
		t.Run(name, func(t *testing.T) {
			s, f := appsFixtureSession(t)
			f.rewrite = func(r *http.Request, m map[string]any) {
				switch name {
				case "missing version":
					if r.URL.Path == "/assets" {
						delete(m, "versionId")
					}
				case "version drift":
					if r.URL.Path == "/assets" {
						m["totalPages"] = 2
						if r.URL.Query().Get("pageIndex") == "0" {
							m["nextPageIndex"] = 1
						} else {
							m["versionId"] = "changed"
						}
					}
				case "skipped page":
					if r.URL.Path == "/assets" {
						m["nextPageIndex"] = 2
					}
				case "page bound":
					if r.URL.Path == "/assets" {
						index := m["currentPageIndex"].(int)
						m["nextPageIndex"] = index + 1
						m["totalPages"] = 100
					}
				case "unowned location":
					if r.URL.Path == "/config" {
						delete(m, "mdmInfo")
					}
				case "no callback":
					if r.URL.Path == "/config" {
						delete(m, "notificationAuthToken")
					}
				case "Book", "not revocable", "stock unknown":
					if r.URL.Path == "/assets" {
						asset := m["assets"].([]any)[0].(map[string]any)
						if name == "Book" {
							asset["productType"] = "Book"
						}
						if name == "not revocable" {
							asset["revocable"] = false
						}
						if name == "stock unknown" {
							delete(asset, "availableCount")
						}
					}
				case "missing asset":
					if r.URL.Path == "/assets" {
						m["assets"] = []any{}
					}
				case "inactive user":
					if r.URL.Path == "/users" {
						m["users"] = []any{map[string]string{"clientUserId": "private-user", "status": "Registered"}}
					}
				case "foreign assignment":
					if r.URL.Path == "/assignments" {
						m["assignments"] = []appsbooks.Assignment{{Asset: appsbooks.Asset{AdamID: "wrong", PricingParam: "STDQ"}, ClientUserID: "private-user"}}
					}
				}
			}
			if name == "revised location" {
				f.location.ConfigurationPending = true
			}
			if err := liveAppsBooksLicensing(t.Context(), s); err == nil {
				t.Fatal("unmet prerequisite or incoherent collection passed")
			}
			if f.postCount() != 0 {
				t.Fatal("unmet prerequisite changed licensing")
			}
		})
	}
}

func TestAppsBooksImmutableFenceAndEvidenceOwnership(t *testing.T) {
	s, _ := appsFixtureSession(t)
	p, id, err := appsScope(s, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	intent := appsLicensingIntent{RunID: id, EvidenceDirectory: p.Evidence}
	if err = appsFence(p, intent); err != nil {
		t.Fatal(err)
	}
	if err = appsFence(p, intent); !errors.Is(err, ErrBlocked) {
		t.Fatal("duplicate fence was overwritten", err)
	}
	if err = appsRelease(p, "foreign"); err == nil {
		t.Fatal("foreign run released fence")
	}
	copy := *p
	copy.Evidence = t.TempDir()
	if err = appsRelease(&copy, id); err == nil {
		t.Fatal("changed evidence directory released fence")
	}
	if err = appsSave(p, "immutable.json", map[string]string{"state": "original"}); err != nil {
		t.Fatal(err)
	}
	if err = appsSave(p, "immutable.json", map[string]string{"state": "changed"}); err == nil {
		t.Fatal("rerun overwrote source evidence")
	}
	if err = appsRelease(p, id); err != nil {
		t.Fatal(err)
	}
	if err = appsCheckFence(p); err != nil {
		t.Fatal(err)
	}
	if err = appsRelease(p, id); err == nil {
		t.Fatal("missing fence released")
	}
	if err = appsSave(p, "bad.json", make(chan int)); err == nil {
		t.Fatal("invalid evidence encoded")
	}
	if _, err = appsPhase(p, "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err = appsPhase(p, "fixture"); err == nil {
		t.Fatal("phase truncated original evidence")
	}
	writeFixture(t, p.Env.Workspace.path("appsbooks", "pending-licensing.json"), []byte("{"))
	if err = appsRelease(p, id); err == nil {
		t.Fatal("corrupt fence released")
	}
}

func appsSourceFixture(t *testing.T, method managedcontent.Method) (managedcontent.Operation, managedcontent.Evidence, appsInstallation) {
	t.Helper()
	c := appsInstallation{Operation: "prepared", BundleID: "com.example.app", Version: "1", Platform: "macOS", Identity: "scep", MaxAgeSeconds: 60}
	selection := managedcontent.Selection{Enrollment: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}, Serial: "serial"}
	content := managedcontent.Content{Asset: appsbooks.Asset{AdamID: "123", PricingParam: "STDQ"}, Kind: "App"}
	d := managedcontent.Dispatch{ID: "dispatch", Digest: "digest", Enrollment: selection.Enrollment, Serial: selection.Serial, Method: method, Content: content, MDM: []byte("wire"), Declarations: []managedcontent.Document{{Identifier: "declaration"}}}
	o := managedcontent.Observation{ID: d.ID, Digest: d.Digest, Enrollment: d.Enrollment, Method: method, ManagementMethod: method, Found: true, Authoritative: true, OwnsIdentity: true, Installed: true, Terminal: true, CommandAcknowledged: true, DeclarationPublished: true, ObservedAt: time.Now()}
	var err error
	if method == managedcontent.MDM {
		var install, report []byte
		install, err = plist.Marshal(&commands.InstallApplicationResponse{Identifier: new(c.BundleID), State: new("Managed")})
		if err != nil {
			t.Fatal(err)
		}
		report, err = plist.Marshal(&commands.InstalledApplicationListResponse{InstalledApplicationList: []commands.InstalledApplicationListResponseInstalledApplicationList{{Identifier: new(c.BundleID), Version: new(c.Version), Installing: new(false)}}})
		if err != nil {
			t.Fatal(err)
		}
		o.Raw, err = json.Marshal(map[string]any{"bundleId": c.BundleID, "installationResponse": install, "currentReport": report})
	} else {
		o.Raw = []byte(`[{"identifier":"com.example.app","declaration-identifier":"declaration","state":"managed","version":"1"}]`)
	}
	if err != nil {
		t.Fatal(err)
	}
	op := managedcontent.Operation{ID: c.Operation, Phase: managedcontent.Dispatched, Plan: managedcontent.Plan{Content: content, Target: selection, Method: method}, Dispatch: &managedcontent.DispatchStatus{ID: d.ID, Digest: d.Digest, Method: method}}
	return op, managedcontent.Evidence{Dispatch: &d, Observation: &o}, c
}

func TestAppsBooksInstallationRequiresNativeProvenance(t *testing.T) {
	for _, method := range []managedcontent.Method{managedcontent.MDM, managedcontent.DDM} {
		t.Run(string(method), func(t *testing.T) {
			op, e, c := appsSourceFixture(t, method)
			if err := appsInstallationSource(op, e, c, time.Now()); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"no dispatch", "no observation", "wrong operation", "not dispatched", "digest mismatch", "wrong enrollment", "not authoritative", "not owned", "not found", "not terminal", "not installed", "no raw", "future", "stale", "ack or publication only", "foreign bundle", "negative state"} {
				t.Run(name, func(t *testing.T) {
					op, e, c := appsSourceFixture(t, method)
					switch name {
					case "no dispatch":
						e.Dispatch = nil
					case "no observation":
						e.Observation = nil
					case "wrong operation":
						op.ID = "other"
					case "not dispatched":
						op.Phase = managedcontent.Prepared
					case "digest mismatch":
						e.Observation.Digest = "foreign"
					case "wrong enrollment":
						e.Observation.Enrollment.ID = "foreign"
					case "not authoritative":
						e.Observation.Authoritative = false
					case "not owned":
						e.Observation.OwnsIdentity = false
					case "not found":
						e.Observation.Found = false
					case "not terminal":
						e.Observation.Terminal = false
					case "not installed":
						e.Observation.Installed = false
					case "no raw":
						e.Observation.Raw = nil
					case "future":
						e.Observation.ObservedAt = time.Now().Add(time.Hour)
					case "stale":
						e.Observation.ObservedAt = time.Now().Add(-time.Hour)
					case "ack or publication only":
						e.Observation.Terminal = false
						e.Observation.Installed = false
					case "foreign bundle":
						c.BundleID = "other"
					case "negative state":
						e.Observation.Raw = []byte(`{"identifier":"com.example.app","state":"managed-but-uninstalled"}`)
					}
					if err := appsInstallationSource(op, e, c, time.Now()); err == nil {
						t.Fatal("unproven installation passed")
					}
				})
			}
		})
	}
}

func TestAppsBooksMissingPhysicalPrerequisitesRemainUnexercised(t *testing.T) {
	s, _ := appsFixtureSession(t)
	if err := liveAppsBooksInstallation(t.Context(), s); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	adeTestJSON(t, s.Env.Workspace.path("appsbooks", "installation.json"), appsInstallation{Operation: "prepared", BundleID: "bundle", Version: "1", Platform: "macOS", Identity: "scep", MaxAgeSeconds: 60})
	if err := liveAppsBooksInstallation(t.Context(), s); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	s.Info = target.Info{Kind: target.KindDevice, UDID: "device"}
	if err := liveAppsBooksInstallation(t.Context(), s); err == nil {
		t.Fatal("no native operation passed")
	}
	for _, b := range adeTestEvidence(t, s, "appsbooks-installation-*/assertions.json") {
		if strings.Contains(string(b), `"macOS":"passed"`) || !strings.Contains(string(b), `"cleanup":"not_applicable_no_app_or_license_changes"`) {
			t.Fatal("missing hardware invented restoration or installation", string(b))
		}
	}
	writeFixture(t, s.Env.Workspace.path("appsbooks", "installation.json"), []byte(`{"operation":"x","extra":true}`))
	if _, err := appsInstallationConfig(s); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
}

func TestAppsBooksBoundsContextAndSafeRequestEncoding(t *testing.T) {
	s, _ := appsFixtureSession(t)
	if err := appsRequest(t.Context(), s, "GET", "/unused", make(chan int), nil); err == nil {
		t.Fatal("invalid request encoded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(appsError(ctx, "fixture"), context.Canceled) {
		t.Fatal("context not observable")
	}
	if err := appsSave(s, "bad.json", json.RawMessage("{")); err == nil {
		t.Fatal("bad JSON persisted")
	}
	if err := os.WriteFile(s.Env.Workspace.path("appsbooks", "stoken"), make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := appsRead(s, "stoken"); err == nil {
		t.Fatal("oversized input accepted")
	}
	if err := os.Remove(s.Env.Workspace.path("appsbooks", "stoken")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.Env.Workspace.path("appsbooks", "stoken"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := appsRead(s, "stoken"); err == nil {
		t.Fatal("directory treated as token")
	}
	if err := os.WriteFile(filepath.Join(s.Evidence, "blocked"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	copy := *s
	copy.Evidence = filepath.Join(s.Evidence, "blocked", "child")
	if _, _, err := appsScope(&copy, "fixture"); err == nil {
		t.Fatal("unwritable evidence accepted")
	}
}

func TestAppsBooksProtocolTimeoutAndExplicitOverride(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		module, override, want time.Duration
	}{
		{"default", 0, 0, DefaultModuleTimeout}, {"protocol", 8 * time.Minute, 0, 8 * time.Minute}, {"override", 8 * time.Minute, time.Minute, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Module{ID: "timeout", Modes: []string{"simulated"}, Timeout: tc.module, Steps: []Step{{Name: "observe deadline", Run: func(ctx context.Context, _ *Session) error {
				deadline, ok := ctx.Deadline()
				left := time.Until(deadline)
				if !ok || left > tc.want || left < tc.want-time.Second {
					t.Errorf("deadline remaining %s, want %s", left, tc.want)
				}
				return nil
			}}}}
			r := Run(t.Context(), &Environment{Instance: Instance{Mode: "simulated"}}, target.Simulator{}, m, Options{Timeout: tc.override})
			if r.Status != StatusPassed {
				t.Fatal(r.Detail)
			}
		})
	}
}

func TestAppsBooksDirectNotificationFallbackAndPendingSpacing(t *testing.T) {
	s, f := appsFixtureSession(t)
	p, _, err := appsScope(s, "SDK-policy")
	if err != nil {
		t.Fatal(err)
	}
	a, err := appsOpen(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	clock := f.config.Clock.(*appsTestClock)
	started := clock.Now()
	var first time.Time
	calls := 0
	f.mu.Lock()
	f.rewrite = func(r *http.Request, m map[string]any) {
		if r.URL.Path != "/status" {
			return
		}
		calls++
		now := clock.Now()
		if calls == 1 {
			first = now
			if now.Sub(started) < appsNotificationFallback {
				t.Error("direct status query preceded notification fallback")
			}
			m["eventStatus"] = "PENDING"
		} else if now.Sub(first) < appsPendingInterval {
			t.Error("PENDING status query preceded minimum interval")
		}
	}
	f.mu.Unlock()
	if err = a.sdkJob(appsLongContext(t), p, "policy", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	observedCalls := calls
	f.mu.Unlock()
	if observedCalls != 2 || len(adeTestEvidence(t, p, "policy-observation-*.json")) != 2 {
		t.Fatal("pending and terminal source observations were not independently retained")
	}
}

func TestAppsBooksSDKPermissionPrerequisiteAndWaitCancellation(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		if err := appsSDKError(t.Context(), "probe", &appsbooks.APIError{HTTPStatus: status}); !errors.Is(err, ErrBlocked) {
			t.Fatal("missing permission was not an unmet prerequisite", err)
		}
	}
	if err := appsSDKError(t.Context(), "probe", appsbooks.ErrExpired); !errors.Is(err, ErrBlocked) {
		t.Fatal("expired token was not an unmet prerequisite", err)
	}
	if err := appsSDKError(t.Context(), "probe", &appsbooks.APIError{HTTPStatus: 500}); errors.Is(err, ErrBlocked) {
		t.Fatal("service failure misreported as prerequisite")
	}
	s, _ := appsFixtureSession(t)
	s.Env.appsBooksFixture = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(appsWait(ctx, s, time.Hour), context.Canceled) {
		t.Fatal("real notification wait ignored cancellation")
	}
	if err := appsWait(t.Context(), s, 0); err != nil {
		t.Fatal(err)
	}
}

func TestAppsBooksRawEvidencePreservesExactSourceBytes(t *testing.T) {
	s, _ := appsFixtureSession(t)
	raw := []byte("{\n  \"future\":null, \"escaped\":\"\\u0061\", \"large\":9007199254740993, \"false\":false\n}\n")
	if err := appsSaveRaw(s, "native.json", raw); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.Evidence, "native.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, b) {
		t.Fatal("private source evidence was reserialized")
	}
	if err = appsSaveRaw(s, "native.json", []byte(`{"replacement":true}`)); err == nil {
		t.Fatal("source evidence was overwritten")
	}
	if err = appsSaveRaw(s, "invalid.json", []byte("{")); err == nil {
		t.Fatal("invalid source accepted")
	}
}
