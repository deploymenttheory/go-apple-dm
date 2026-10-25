package lab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// appsRuntime uses the actual TLS reference runtime and sealed SQL composition.
// Only Apple's external HTTP service is replaced; no administration API is faked.
func appsRuntime(t *testing.T) (*Session, *appsFixture, appsAcceptance) {
	t.Helper()
	f := appsSDKFixture(t)
	f.location.ID = "lab-" + randomID()
	f.location.UID = "library-" + randomID()
	f.config.UID = f.location.UID
	dir := t.TempDir()
	if err := Init(dir, "simulated", "sqlite", "127.0.0.1:0", AdapterProcess, nil); err != nil {
		t.Fatal(err)
	}
	w, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	e, err := start(t.Context(), w, "", io.Discard, &f.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.Close()
		if t.Failed() {
			select {
			case runtimeErr := <-e.errs:
				t.Logf("private reference runtime termination: %v", runtimeErr)
			default:
				t.Log("private reference runtime had no terminal error receipt")
			}
		}
	})
	s := &Session{Env: e, Evidence: t.TempDir()}
	c := appsAcceptance{Location: f.location.ID, UID: f.location.UID, MDMID: "mdm", Asset: appsbooks.Asset{AdamID: "123", PricingParam: "STDQ"}, User: "private-user", MaxPages: 5, FreeApp: true, Mutate: true, Quiescent: true}
	if err = os.Mkdir(w.path("appsbooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	adeTestJSON(t, w.path("appsbooks", "acceptance.json"), c)
	writeFixture(t, w.path("appsbooks", "stoken"), []byte(f.config.SToken))
	var location appsbooks.Location
	if err = e.api(t.Context(), "PUT", appsBase(c), map[string]any{"revision": 0, "sToken": f.config.SToken}, &location); err != nil {
		t.Fatal(err)
	}
	if err = e.api(t.Context(), "PUT", appsBase(c)+"/receiver", map[string]string{"token": "private-callback"}, nil); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.callbackClient = e.Client
	f.mu.Unlock()
	request := appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: c.MDMID, Name: "lab", Metadata: "lab"}, NotificationURL: e.URL + "/appsbooks/notifications/" + c.Location, NotificationAuthToken: "private-callback", NotificationTypes: []string{"ASSET_MANAGEMENT"}}
	if err = e.api(t.Context(), "POST", appsBase(c)+"/configure", map[string]any{"revision": location.Revision, "configuration": request}, &location); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.location = location
	f.mu.Unlock()
	return s, f, c
}

// appsRuntimeSQL opens the actual sealed SQLite state store only in simulated mode.
func appsRuntimeSQL(t *testing.T, s *Session) *statestore.Store {
	t.Helper()
	if s.Env.Mode != "simulated" {
		t.Fatal("private SQL fixture setup is forbidden in live mode")
	}
	w := s.Env.Workspace
	if w.Storage != "sqlite" {
		t.Fatal("controlled due-state timing fixture requires its private SQLite backend")
	}
	db, err := sql.Open("sqlite", sqlite.DSN(w.path("mdm", "bench-simulated.sqlite"), sqlite.Options{}))
	if err != nil {
		t.Fatal("private SQL fixture pool unavailable")
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	b, err := os.ReadFile(w.path("mdm", "storage-key"))
	if err != nil {
		t.Fatal(err)
	}
	material := []byte(strings.TrimSpace(string(b)))
	ring, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "bench", Accepted: []string{"lab"}, Strict: true}, Provider: secrets.Static{"bench": material, "lab": material}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := statestore.Open(t.Context(), db, sqlite.Dialect, ring)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// appsSQLSnapshot reads private source bytes without exposing them in failures.
func appsSQLSnapshot(t *testing.T, st state.Store, prefix string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	after := ""
	for {
		rows, err := st.List(t.Context(), prefix, after, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			out[row.Key] = row.Value
			after = row.Key
		}
		if len(rows) < 100 {
			return out
		}
	}
}

// appsDueFixture constructs one due-state fixture after proving the real
// five-minute SQL barrier. It changes only NextPollAt, never a result or fence.
// This is simulated scheduler timing, not proof that five wall minutes elapsed.
func appsDueFixture(t *testing.T, s *Session, f *appsFixture, st state.Store, location, id string) {
	t.Helper()
	if s.Env.Mode != "simulated" {
		t.Fatal("virtual timing setup is forbidden in live mode")
	}
	prefix := fmt.Sprintf("appsbooks/location/%x/", sha256.Sum256([]byte(location)))
	jobKey := fmt.Sprintf("%sjobs/%x", prefix, sha256.Sum256([]byte(id)))
	before := appsSQLSnapshot(t, st, prefix)
	var original appsbooks.LicensingJob
	if json.Unmarshal(before[jobKey], &original) != nil || original.ID != id || original.State != appsbooks.JobAccepted || original.EventID == "" || original.Status != nil {
		t.Fatal("fixture job was not actually accepted")
	}
	statusBefore := f.requestCount("GET /status")
	body := strings.NewReader(`{"worker":"early-fixture","leaseSeconds":30}`)
	_, code, err := HTTP(t.Context(), s.Env.Client, s.Env.URL, s.Env.Token, "POST", "/appsbooks/locations/"+location+"/jobs/"+id+"/poll", body)
	if err == nil || code != http.StatusConflict || f.requestCount("GET /status") != statusBefore {
		t.Fatal("real SQL poll gate did not reject early query before HTTP")
	}
	if !reflect.DeepEqual(before, appsSQLSnapshot(t, st, prefix)) {
		t.Fatal("early poll modified sealed job or receipt state")
	}
	var due time.Time
	err = st.Update(t.Context(), []string{prefix + "metadata", jobKey}, func(tx state.Tx) error {
		record, err := tx.Get(t.Context(), jobKey)
		if err != nil {
			return err
		}
		if !bytes.Equal(record.Value, before[jobKey]) {
			return fmt.Errorf("fixture job changed before controlled timing setup")
		}
		if wait := original.NextPollAt.Sub(tx.Now()); wait < 4*time.Minute || wait > 5*time.Minute+time.Second {
			return fmt.Errorf("fixture did not observe real five-minute barrier")
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(record.Value, &fields); err != nil {
			return err
		}
		due = tx.Now().Add(-time.Second)
		fields["nextPollAt"], err = json.Marshal(due)
		if err != nil {
			return err
		}
		record.Value, err = json.Marshal(fields)
		if err != nil {
			return err
		}
		return tx.Put(t.Context(), record)
	})
	if err != nil {
		t.Fatal(err)
	}
	after := appsSQLSnapshot(t, st, prefix)
	var changed appsbooks.LicensingJob
	if json.Unmarshal(after[jobKey], &changed) != nil {
		t.Fatal("fixture job could not be reread")
	}
	expected := original
	expected.NextPollAt = due
	if !reflect.DeepEqual(expected, changed) {
		t.Fatal("virtual timing modified another job field")
	}
	delete(before, jobKey)
	delete(after, jobKey)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("virtual timing modified receipts, location or fencing state")
	}
	if err = appsSave(s, "virtual-due-"+id+".json", map[string]any{"mode": "simulated", "timing": "controlled_next_poll_at_fixture", "real_early_poll_rejected": true, "other_fields_and_receipts_preserved": true}); err != nil {
		t.Fatal(err)
	}
}

// requestCount counts a selected Apple request under the fixture lock.
func (f *appsFixture) requestCount(request string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, seen := range f.requests {
		if seen == request {
			n++
		}
	}
	return n
}

// appsInstallDueFixture installs test-only due progression after asserting the real SQL poll barrier.
func appsInstallDueFixture(t *testing.T, s *Session, f *appsFixture, c appsAcceptance) {
	t.Helper()
	st := appsRuntimeSQL(t, s)
	base := s.Env.Client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	f.mu.Lock()
	f.beforeLicensing = func() error {
		b, err := os.ReadFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
		if err != nil {
			return fmt.Errorf("Apple mutation preceded immutable private fence")
		}
		var intent appsLicensingIntent
		if json.Unmarshal(b, &intent) != nil || intent.RunID == "" || intent.EvidenceDirectory == "" || !filepath.IsAbs(intent.EvidenceDirectory) || intent.Location.Revision == 0 || len(intent.ConfigurationRaw) == 0 {
			return fmt.Errorf("Apple mutation preceded complete original snapshot")
		}
		return nil
	}
	f.mu.Unlock()
	s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		if err == nil && response.StatusCode == http.StatusOK && strings.HasSuffix(r.URL.Path, "/submit") {
			parts := strings.Split(r.URL.Path, "/")
			id := parts[len(parts)-2]
			appsDueFixture(t, s, f, st, c.Location, id)
		}
		return response, err
	})
}

// TestAppsBooksPreparedJobHasNoAutomaticSubmission proves the worker cannot remotely submit a locally prepared licensing intent.
func TestAppsBooksPreparedJobHasNoAutomaticSubmission(t *testing.T) {
	s, f, _ := appsRuntime(t)
	p, id, err := appsScope(s, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	a, err := appsOpen(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.referencePrepare(t.Context(), p, id); err != nil {
		t.Fatal(err)
	}
	// Exercise an actual reference worker tick with only licensing intent.
	select {
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	case <-time.After(1100 * time.Millisecond):
	}
	var job appsbooks.LicensingJob
	if err = appsRequest(t.Context(), p, "GET", appsBase(a.config)+"/jobs/"+id+"/evidence", nil, &job); err != nil {
		t.Fatal(err)
	}
	if job.State != appsbooks.JobQueued || job.EventID != "" || f.postCount() != 0 {
		t.Fatal("queued local licensing intent escaped explicit submission admission")
	}
	if _, err = os.Stat(s.Env.Workspace.path("appsbooks", "pending-licensing.json")); !os.IsNotExist(err) {
		t.Fatal("preparing intent changed mutation fence")
	}
}

// TestAppsBooksNativeInstallationThroughReferenceRuntime proves installation through signed native exchanges and real SQL composition.
func TestAppsBooksNativeInstallationThroughReferenceRuntime(t *testing.T) {
	appsNativeInstallationRuntime(t, managedcontent.MDM)
}

// TestAppsBooksDeclarativeInstallationThroughReferenceRuntime proves current owned
// declarative app status independently of publication and historical processing.
func TestAppsBooksDeclarativeInstallationThroughReferenceRuntime(t *testing.T) {
	appsNativeInstallationRuntime(t, managedcontent.DDM)
}

// appsNativeInstallationRuntime exercises the real worker, authenticated native
// reports and read-only acceptance with an APNs-driven protocol simulator.
func appsNativeInstallationRuntime(t *testing.T, method managedcontent.Method) {
	s, f, c := appsRuntime(t)
	capture := appsNativeStackCapture(t, s)
	c.User, c.Serial = "", benchSerial
	adeTestJSON(t, s.Env.Workspace.path("appsbooks", "acceptance.json"), c)
	f.mu.Lock()
	f.assigned = true
	f.assignment = appsbooks.Assignment{Asset: c.Asset, SerialNumber: c.Serial}
	f.mu.Unlock()
	var installed atomic.Bool
	responder := func(command *mdm.Command) simulator.Reply {
		switch command.RequestType {
		case "DeviceInformation":
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.DeviceInformationResponse{QueryResponses: commands.DeviceInformationResponseQueryResponses{SerialNumber: new(benchSerial), OSVersion: new("27.0"), BuildVersion: new("27A100"), ProductName: new("Mac14,7"), IsSupervised: new(true)}}}
		case "InstallApplication":
			installed.Store(true)
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstallApplicationResponse{Identifier: new("com.example.selected"), State: new("Managed")}}
		case "InstalledApplicationList":
			rows := []commands.InstalledApplicationListResponseInstalledApplicationList{}
			if installed.Load() {
				rows = append(rows, commands.InstalledApplicationListResponseInstalledApplicationList{Identifier: new("com.example.selected"), Version: new("2.4"), Installing: new(false)})
			}
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstalledApplicationListResponse{InstalledApplicationList: rows}}
		case "ManagedApplicationList":
			state := "ManagedButUninstalled"
			if installed.Load() {
				state = "Managed"
			}
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.ManagedApplicationListResponse{ManagedApplicationList: map[string]commands.ManagedApplicationListManagedApplicationItem{"com.example.selected": {Status: state}}}}
		default:
			return simulator.AcknowledgeAll(command)
		}
	}
	d, err := s.Env.device(t.Context(), simulator.WithResponder(responder), func(d *simulator.Device) { d.OSVersion, d.BuildVersion, d.ProductName = "27.0", "27A100", "Mac14,7" })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	stopped := make(chan struct{})
	var connects, empty, processed atomic.Int64
	wakesBefore := len(s.Env.APNS.Requests())
	go func() {
		defer close(stopped)
		seen := wakesBefore
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				wakes := len(s.Env.APNS.Requests())
				if wakes <= seen {
					continue
				}
				seen = wakes
				connects.Add(1)
				commands, err := d.Connect(ctx)
				processed.Add(int64(len(commands)))
				if len(commands) == 0 {
					empty.Add(1)
				}
				if err != nil && ctx.Err() == nil {
					capture("native-connect-error")
					t.Error(err)
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-stopped
		t.Logf("native fixture flow: connects=%d empty=%d processed=%d wakes=%d", connects.Load(), empty.Load(), processed.Load(), len(s.Env.APNS.Requests())-wakesBefore)
	}()
	query, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion", "BuildVersion", "SerialNumber", "ProductName", "IsSupervised"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Command(ctx, s.Env, pathOf(d), query, ""); err != nil {
		t.Fatal(err)
	}
	if method == managedcontent.DDM {
		if _, err = d.SyncDDM(ctx); err != nil {
			t.Fatal(err)
		}
		if err = d.PostDDMStatus(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	id := "native-" + randomID()
	base := "/managed-content/operations/" + id
	plan := managedcontent.Plan{LocationID: c.Location, Content: managedcontent.Content{Asset: c.Asset, Kind: "App"}, Target: managedcontent.Selection{Enrollment: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: d.UDID}, Serial: c.Serial}, Method: method, MaxPages: c.MaxPages}
	var op managedcontent.Operation
	if err = s.Env.api(ctx, "PUT", base, map[string]any{"mode": managedcontent.InstallOnly, "plan": plan}, &op); err != nil {
		t.Fatal(err)
	}
	worker := map[string]any{"worker": "lab-native", "leaseSeconds": 30}
	if err = waitLive(ctx, func() (bool, error) {
		var current managedcontent.Operation
		if err := appsRequest(ctx, s, "GET", base, nil, &current); err != nil {
			return false, err
		}
		return current.Phase == managedcontent.Dispatched, nil
	}); err != nil {
		capture("initial-worker-dispatch-error")
		t.Fatal(err)
	}
	var declaration string
	report := func(declaration string, removed bool) {
		t.Helper()
		row := map[string]any{"identifier": "com.example.selected", "declaration-identifier": declaration, "state": "managed", "version": "2.4"}
		if removed {
			row = map[string]any{"identifier": "com.example.selected", "_removed": true}
		}
		body, err := json.Marshal(map[string]any{"StatusItems": map[string]any{"app": map[string]any{"managed": map[string]any{"list": []any{row}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = d.DeclarativeManagement(ctx, "status", body); err != nil {
			t.Fatal(err)
		}
	}
	if method == managedcontent.DDM {
		if _, err = d.SyncDDM(ctx); err != nil {
			t.Fatal(err)
		}
		if err = d.PostDDMStatus(ctx, true); err != nil {
			t.Fatal(err)
		}
		var current managedcontent.Evidence
		if err = s.Env.api(ctx, "GET", base+"/evidence", nil, &current); err != nil {
			t.Fatal(err)
		}
		if current.Dispatch == nil || len(current.Dispatch.Declarations) == 0 {
			t.Fatal("worker did not publish actual declarations")
		}
		declaration = current.Dispatch.Declarations[0].Identifier
		installed.Store(true)
	}
	list, err := mdm.NewCommand(&commands.InstalledApplicationList{Identifiers: []string{"com.example.selected"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Command(ctx, s.Env, pathOf(d), list, ""); err != nil {
		t.Fatal(err)
	}
	if method == managedcontent.DDM {
		report(declaration, false)
	}
	if err = s.Env.api(ctx, "POST", base+"/step", worker, &op); err != nil {
		t.Fatal(err)
	}
	if op.Observation == nil || !op.Observation.Installed || op.Observation.AppVersion != "2.4" {
		t.Fatal("actual native installation proof did not reach SDK operation")
	}
	s.Info = target.Info{Kind: target.KindDevice, UDID: d.UDID, Serial: c.Serial}
	installation := appsInstallation{Operation: id, BundleID: "com.example.selected", Version: "2.4", Platform: "macOS", Identity: "scep", MaxAgeSeconds: 60}
	adeTestJSON(t, s.Env.Workspace.path("appsbooks", "installation.json"), installation)
	if err = liveAppsBooksInstallation(ctx, s); err != nil {
		capture("native-proof-error")
		t.Fatal(err)
	}
	if f.postCount() != 0 {
		t.Fatal("read-only inspection changed preexisting licenses")
	}
	installCommands := 0
	for _, command := range d.Commands() {
		if command.RequestType == "InstallApplication" {
			installCommands++
		}
	}
	expectedInstalls := 1
	if method == managedcontent.DDM {
		expectedInstalls = 0
	}
	if installCommands != expectedInstalls {
		t.Fatal("inspection repeated installation or DDM used an MDM install")
	}
	rows := adeTestEvidence(t, s, "appsbooks-installation-*/assertions.json")
	if len(rows) != 1 || !bytes.Contains(rows[0], []byte(`"macOS":"simulated"`)) || !bytes.Contains(rows[0], []byte(`"iOS":"unexercised"`)) {
		t.Fatal("simulator evidence claimed physical or other-platform acceptance")
	}
	if method == managedcontent.DDM {
		// Keep the physical app present: only the current management owner changes.
		report("foreign-declaration", false)
		if err = liveAppsBooksInstallation(ctx, s); !errors.Is(err, ErrBlocked) {
			t.Fatal("late foreign ownership borrowed historical acceptance", err)
		}
		report(declaration, false)
		report(declaration, true)
		if err = liveAppsBooksInstallation(ctx, s); !errors.Is(err, ErrBlocked) {
			t.Fatal("late native removal borrowed historical acceptance", err)
		}
		return
	}
	installed.Store(false)
	removed, err := mdm.NewCommand(&commands.InstalledApplicationList{Identifiers: []string{"com.example.selected"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Command(ctx, s.Env, pathOf(d), removed, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Env.api(ctx, "POST", base+"/step", worker, &op); err != nil {
		t.Fatal(err)
	}
	if err = liveAppsBooksInstallation(ctx, s); !errors.Is(err, ErrBlocked) {
		t.Fatal("historical processing passed as current installation", err)
	}
}

// appsNativeStackCapture is an opt-in private test diagnostic. It retains SQL
// blocking stacks before a supervisor failure without changing any deadline.
func appsNativeStackCapture(t *testing.T, s *Session) func(string) {
	t.Helper()
	path := os.Getenv("LAB_APPSBOOKS_STACK_FILE")
	if path == "" {
		return func(string) {}
	}
	if s.Env.Mode != "simulated" {
		t.Fatal("native stack fixture requires simulated mode")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- Operator-selected private test diagnostics path, not a network input.
	if err != nil {
		t.Fatal("private native stack file unavailable")
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	var lock sync.Mutex
	capture := func(reason string) {
		lock.Lock()
		defer lock.Unlock()
		if _, err := fmt.Fprintf(f, "\n--- native SQL blocking sample: %s ---\n", reason); err != nil {
			t.Error(err)
			return
		}
		if err := pprof.Lookup("goroutine").WriteTo(f, 2); err != nil {
			t.Error(err)
		}
	}
	go func() {
		defer close(stopped)
		for _, delay := range []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second} {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			capture("timed")
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	return capture
}

// appsLongContext provides the protocol budget while isolated timers avoid wall-clock delays.
func appsLongContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), appsModuleTimeout)
	t.Cleanup(cancel)
	return ctx
}

// TestAppsBooksLicensingThroughReferenceSQLRuntime settles both licensing paths and restores original assignments through real SQL.
func TestAppsBooksLicensingThroughReferenceSQLRuntime(t *testing.T) {
	for _, scenario := range []struct {
		name                   string
		preexisting, unlimited bool
	}{{"absent", false, false}, {"preexisting", true, false}, {"unlimited", false, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			preexisting := scenario.preexisting
			s, f, c := appsRuntime(t)
			f.mu.Lock()
			f.assigned = preexisting
			if scenario.unlimited {
				f.rewrite = func(r *http.Request, m map[string]any) {
					if r.URL.Path == "/assets" {
						rows := m["assets"].([]any)
						asset := rows[0].(map[string]any)
						delete(asset, "availableCount")
						m["unlimitedAssets"] = rows
						m["assets"] = []any{}
					}
				}
			}
			f.mu.Unlock()
			appsInstallDueFixture(t, s, f, c)
			ctx := appsLongContext(t)
			if err := liveAppsBooksCollection(ctx, s); err != nil {
				t.Fatal(err)
			}
			if f.postCount() != 0 {
				t.Fatal("real read-only composition mutated Apple licensing")
			}
			if err := liveAppsBooksLicensing(ctx, s); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			assigned, posts := f.assigned, f.posts
			f.mu.Unlock()
			want := 3
			if preexisting {
				want = 2
			}
			if assigned != preexisting || posts != want {
				t.Fatal("original assignment was not preserved/restored")
			}
			if _, err := os.Stat(s.Env.Workspace.path("appsbooks", "pending-licensing.json")); !os.IsNotExist(err) {
				t.Fatal("successful positive cleanup retained fence", err)
			}
			if len(adeTestEvidence(t, s, "appsbooks-licensing-*/reference-callback-*.json")) != 1 || len(adeTestEvidence(t, s, "appsbooks-licensing-*/cleanup.json")) != 1 {
				t.Fatal("actual callback or cleanup evidence missing")
			}
			if len(adeTestEvidence(t, s, "virtual-due-*.json")) != 1 {
				t.Fatal("simulated SQL timing setup not disclosed")
			}
		})
	}
}

// TestAppsBooksRuntimePartialCallbacksAndCleanupRetainFence retains recovery ownership for partial, contradictory and unresolved outcomes.
func TestAppsBooksRuntimePartialCallbacksAndCleanupRetainFence(t *testing.T) {
	for _, name := range []string{"SDK partial", "reference partial", "missing callback", "cleanup partial", "late callback failure", "cleanup capability changed"} {
		t.Run(name, func(t *testing.T) {
			s, f, c := appsRuntime(t)
			appsInstallDueFixture(t, s, f, c)
			f.mu.Lock()
			if name == "missing callback" {
				f.dropCallbackEvent = 2
			}
			if name == "late callback failure" {
				f.lateReferenceFailure = true
			}
			f.rewrite = func(r *http.Request, m map[string]any) {
				if name == "cleanup capability changed" && r.URL.Path == "/assets" && f.events == 2 {
					m["assets"].([]any)[0].(map[string]any)["revocable"] = false
				}
				if r.URL.Path != "/status" {
					return
				}
				if name == "SDK partial" && f.events == 1 || name == "reference partial" && f.events == 2 || name == "cleanup partial" && f.events == 3 {
					m["numCompleted"] = 0
				}
			}
			f.mu.Unlock()
			err := liveAppsBooksLicensing(appsLongContext(t), s)
			if err == nil {
				t.Fatal("partial or callback-free licensing passed")
			}
			if name == "missing callback" && !errors.Is(err, ErrBlocked) {
				t.Fatal("missing callback was not an unmet prerequisite", err)
			}
			before, readErr := os.ReadFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
			if readErr != nil {
				t.Fatal("uncertainty lost original fence", readErr)
			}
			posts := f.postCount()
			if name == "SDK partial" && posts != 1 || name == "reference partial" && posts != 2 || (name == "missing callback" || name == "cleanup capability changed") && posts != 2 || (name == "cleanup partial" || name == "late callback failure") && posts != 3 {
				t.Fatal("failure performed an unowned inverse or unexpected mutation")
			}
			s.Evidence = t.TempDir()
			if err = liveAppsBooksLicensing(appsLongContext(t), s); !errors.Is(err, ErrBlocked) {
				t.Fatal("changed output path bypassed pending fence", err)
			}
			after, readErr := os.ReadFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
			if readErr != nil || !bytes.Equal(before, after) || f.postCount() != posts {
				t.Fatal("rerun changed recovery evidence or replayed mutation")
			}
		})
	}
}

// TestAppsBooksShortDeadlineBlocksBeforeAppleMutation rejects an insufficient mutation budget before Apple writes.
func TestAppsBooksShortDeadlineBlocksBeforeAppleMutation(t *testing.T) {
	s, f := appsFixtureSession(t)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), appsMinimumBudget-time.Second)
	defer cancel()
	if err := liveAppsBooksLicensing(ctx, s); !errors.Is(err, ErrBlocked) {
		t.Fatal("insufficient restoration budget accepted", err)
	}
	if f.postCount() != 0 {
		t.Fatal("insufficient budget reached Apple mutation")
	}
	if _, err := os.Stat(s.Env.Workspace.path("appsbooks", "pending-licensing.json")); !os.IsNotExist(err) {
		t.Fatal("short budget acquired mutation fence")
	}
}
