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
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// appsRuntime uses the actual TLS reference runtime and sealed SQL composition.
// Only Apple's external HTTP service is replaced; no administration API is faked.
func appsRuntime(t *testing.T) (*Session, *appsFixture, appsAcceptance) {
	t.Helper()
	return appsRuntimeWith(t, nil)
}

// appsRuntimeWith installs a fixture transport before any runtime worker starts.
func appsRuntimeWith(t *testing.T, prepare func(*appsFixture), settings ...map[string]string) (*Session, *appsFixture, appsAcceptance) {
	t.Helper()
	f := appsSDKFixture(t)
	if prepare != nil {
		prepare(f)
	}
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
	if len(settings) > 1 {
		t.Fatal("fixture accepts one explicit runtime settings map")
	}
	if len(settings) == 1 {
		w.Settings = settings[0]
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
	_, code, err := HTTP(t.Context(), s.Env.Client, s.Env.URL, s.Env.Token, "POST", "/appsbooks/locations/"+location+"/jobs/"+id+"/poll", body, http.Header{"X-Lab-Fixture": {"early-poll-barrier"}})
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
	t.Parallel()
	appsNativeInstallationRuntime(t, managedcontent.MDM, false, nil)
}

// TestAppsBooksDeclarativeInstallationThroughReferenceRuntime proves current owned
// declarative app status independently of publication and historical processing.
func TestAppsBooksDeclarativeInstallationThroughReferenceRuntime(t *testing.T) {
	t.Parallel()
	appsNativeInstallationRuntime(t, managedcontent.DDM, false, nil)
}

// appsRuntimeScenarioBudget bounds one simulated Apps and Books runtime
// scenario. The native inspection scenario proves installation, then nine
// contradictory native reports and a recovery, each through authenticated
// native queries whose results are polled once a second; under the race
// detector it takes about a minute on a developer machine. Declarative
// scenarios that take seconds there have exceeded a minute on CI runners,
// which run the whole package's parallel tests on fewer cores while waiting
// for the reference server's once-a-second worker to commit its observation.
// Four minutes covers that contention, while a stalled scenario still fails
// well inside the package's 20-minute test timeout.
const appsRuntimeScenarioBudget = 4 * time.Minute

// appsNativeInstallationRuntime exercises the real worker, authenticated native
// reports and read-only acceptance with an APNs-driven protocol simulator.
func appsNativeInstallationRuntime(t *testing.T, method managedcontent.Method, endpointFailures bool, beforeProof func(context.Context, *Session, *appsFixture, string) func(managedcontent.Operation)) {
	t.Helper()
	s, f, c := appsRuntime(t)
	capture := appsNativeStackCapture(t, s)
	c.User, c.Serial = "", benchSerial
	adeTestJSON(t, s.Env.Workspace.path("appsbooks", "acceptance.json"), c)
	f.mu.Lock()
	f.assigned = true
	f.assignment = appsbooks.Assignment{Asset: c.Asset, SerialNumber: c.Serial}
	f.mu.Unlock()
	var installed atomic.Bool
	var nativeFault atomic.Value
	nativeFault.Store("")
	responder := func(command *mdm.Command) simulator.Reply {
		switch command.RequestType {
		case "DeviceInformation":
			fault, ok := nativeFault.Load().(string)
			if !ok {
				t.Error("invalid native fixture mode")
			}
			facts := commands.DeviceInformationResponseQueryResponses{SerialNumber: new(benchSerial), OSVersion: new("27.0"), BuildVersion: new("27A100"), ProductName: new("Mac14,7"), IsSupervised: new(true)}
			switch fault {
			case "wrong serial":
				facts.SerialNumber = new("FOREIGN-SERIAL")
			case "missing build":
				facts.BuildVersion = nil
			case "invalid OS":
				facts.OSVersion = new("invalid")
			case "unsupported OS":
				facts.OSVersion = new("1.0")
			case "foreign platform":
				facts.ProductName = new("iPhone18,1")
			}
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.DeviceInformationResponse{QueryResponses: facts}}
		case "InstallApplication":
			installed.Store(true)
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstallApplicationResponse{Identifier: new("com.example.selected"), State: new("Managed")}}
		case "InstalledApplicationList":
			rows := []commands.InstalledApplicationListResponseInstalledApplicationList{}
			if installed.Load() {
				fault, ok := nativeFault.Load().(string)
				if !ok {
					t.Error("invalid native fixture mode")
				}
				row := commands.InstalledApplicationListResponseInstalledApplicationList{Identifier: new("com.example.selected"), Version: new("2.4"), Installing: new(false)}
				switch fault {
				case "wrong app version":
					row.Version = new("9.9")
				case "installing":
					row.Installing = new(true)
				case "missing app version":
					row.Version = nil
				}
				if fault != "missing app" {
					rows = append(rows, row)
				}
			}
			return simulator.Reply{Status: mdm.StatusAcknowledged, Payload: &commands.InstalledApplicationListResponse{InstalledApplicationList: rows}}
		case "ManagedApplicationList":
			state := "ManagedButUninstalled"
			if installed.Load() {
				state = "Managed"
			}
			if fault, ok := nativeFault.Load().(string); ok && fault == "not managed" {
				state = "UserInstalledApp"
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
	id := "native-" + randomID()
	base := "/managed-content/operations/" + id
	started := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), appsRuntimeScenarioBudget)
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
		t.Logf("native fixture flow: connects=%d empty=%d processed=%d wakes=%d elapsed=%s", connects.Load(), empty.Load(), processed.Load(), len(s.Env.APNS.Requests())-wakesBefore, time.Since(started).Round(time.Millisecond))
		if t.Failed() {
			appsRuntimeDiagnostics(t, s, d, base)
		}
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
	if method == managedcontent.MDM {
		// The supervised worker records native facts only while the stored
		// observation is pending; a terminal one stays as of its inspection
		// (docs/operations/apps-and-books.md). Let the worker record the
		// acknowledged installation before the installed-app report exists, so
		// this scenario always proves the documented path for a report that
		// follows the acknowledgement, whatever the runner's timing.
		if err = waitLive(ctx, func() (bool, error) {
			var current managedcontent.Operation
			if err := s.Env.api(ctx, "GET", base, nil, &current); err != nil {
				return false, err
			}
			return current.Worker == "" && current.LeaseUntil.IsZero() && current.Observation != nil && current.Observation.Terminal, nil
		}); err != nil {
			capture("terminal-acknowledgement-error")
			t.Fatal(err)
		}
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
	var proofRead func(managedcontent.Operation)
	if beforeProof != nil {
		proofRead = beforeProof(ctx, s, f, base)
	}
	// Observe the supervised worker's committed result instead of competing for
	// its inspection lease after the authenticated native report arrives. A
	// terminal observation recorded before that report stays as of its
	// inspection, so step it explicitly, as the removal check below does.
	if err = waitLive(ctx, func() (bool, error) {
		var current managedcontent.Operation
		if err := s.Env.api(ctx, "GET", base, nil, &current); err != nil {
			return false, err
		}
		if proofRead != nil {
			proofRead(current)
		}
		if current.Worker != "" || !current.LeaseUntil.IsZero() || current.Observation == nil {
			return false, nil
		}
		if current.Observation.Terminal && !current.Observation.Installed {
			if err := s.Env.api(ctx, "POST", base+"/step", worker, &current); err != nil {
				return false, err
			}
			return false, nil
		}
		if !current.Observation.Installed {
			return false, nil
		}
		op = current
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if proofRead != nil {
		proofRead(op)
	}
	if op.Observation == nil || !op.Observation.Installed || op.Observation.AppVersion != "2.4" {
		t.Fatal("actual native installation proof did not reach SDK operation")
	}
	s.Info = target.Info{Kind: target.KindDevice, UDID: d.UDID, Serial: c.Serial}
	installation := appsInstallation{Operation: id, BundleID: "com.example.selected", Version: "2.4", Platform: "macOS", Identity: "scep", MaxAgeSeconds: 60}
	adeTestJSON(t, s.Env.Workspace.path("appsbooks", "installation.json"), installation)
	if method == managedcontent.DDM {
		// The proof requires a status report no older than MaxAgeSeconds. Report
		// the unchanged app status again, so the bound measures the device's
		// report rather than how long this runner took to reach the proof.
		report(declaration, false)
	}
	if err = liveAppsBooksInstallation(ctx, s); err != nil {
		capture("native-proof-error")
		t.Fatal(err)
	}
	if endpointFailures {
		appsNativeEndpointFailures(t, ctx, s, base)
		if f.postCount() != 0 {
			t.Fatal("native inspection failure changed licensing")
		}
		installs := 0
		for _, command := range d.Commands() {
			if command.RequestType == "InstallApplication" {
				installs++
			}
		}
		if installs != 1 {
			t.Fatal("read-only failure queued another installation")
		}
		return
	}
	if method == managedcontent.MDM {
		for _, name := range []string{"wrong serial", "missing build", "invalid OS", "foreign platform", "wrong app version", "missing app version", "installing", "missing app", "not managed"} {
			t.Run(name, func(t *testing.T) {
				nativeFault.Store(name)
				if err := liveAppsBooksInstallation(ctx, s); err == nil {
					t.Fatal("contradictory authenticated native facts passed")
				}
				nativeFault.Store("")
				// Restore source facts using the same authenticated native query path.
				if _, err := appsNativeQuery(ctx, s, pathOf(d), &commands.DeviceInformation{Queries: []string{"OSVersion", "BuildVersion", "SerialNumber", "ProductName", "IsSupervised"}}); err != nil {
					t.Fatal(err)
				}
				if _, err := appsNativeQuery(ctx, s, pathOf(d), &commands.InstalledApplicationList{Identifiers: []string{"com.example.selected"}}); err != nil {
					t.Fatal(err)
				}
			})
		}
		if err := liveAppsBooksInstallation(ctx, s); err != nil {
			t.Fatal("valid native proof did not recover after contradictory reports", err)
		}
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
	simulated := 0
	for _, row := range rows {
		if bytes.Contains(row, []byte(`"macOS":"passed"`)) || !bytes.Contains(row, []byte(`"iOS":"unexercised"`)) {
			t.Fatal("simulator evidence claimed physical or other-platform acceptance")
		}
		if bytes.Contains(row, []byte(`"macOS":"simulated"`)) {
			simulated++
		}
	}
	expectedProofs := 1
	if method == managedcontent.MDM {
		expectedProofs = 2
	}
	if simulated != expectedProofs {
		t.Fatal("initial or recovered simulated proof missing")
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
	installed.Store(true)
	if _, err = appsNativeQuery(ctx, s, pathOf(d), &commands.InstalledApplicationList{Identifiers: []string{"com.example.selected"}}); err != nil {
		t.Fatal(err)
	}
	// A truly unsupported OS prevents subsequent command admission as well.
	// Exercise it last; do not manufacture a recovery through unsupported APIs.
	nativeFault.Store("unsupported OS")
	if err = liveAppsBooksInstallation(ctx, s); err == nil {
		t.Fatal("unsupported native OS passed installation acceptance")
	}
}

// appsRuntimeDiagnostics logs where a failed runtime scenario stopped: the request
// types the simulated device processed, the server's queue for its enrollment and
// the stored operation's state. It logs no payloads, identifiers or credentials.
func appsRuntimeDiagnostics(t *testing.T, s *Session, d *simulator.Device, base string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
	defer cancel()
	var processed []string
	for _, command := range d.Commands() {
		processed = append(processed, command.RequestType)
	}
	t.Logf("device processed: %s", strings.Join(processed, ", "))
	var queue struct {
		Items []struct{ RequestType, State string }
	}
	if err := s.Env.api(ctx, "GET", pathOf(d)+"/commands", nil, &queue); err != nil {
		t.Logf("server queue unavailable: %v", err)
	} else {
		rows := make([]string, 0, len(queue.Items))
		for _, q := range queue.Items {
			rows = append(rows, q.RequestType+"="+q.State)
		}
		t.Logf("server queue: %s", strings.Join(rows, ", "))
	}
	var op managedcontent.Operation
	if err := s.Env.api(ctx, "GET", base, nil, &op); err != nil {
		t.Logf("operation unavailable: %v", err)
		return
	}
	observation := "none"
	if o := op.Observation; o != nil {
		observation = fmt.Sprintf("terminal=%t acknowledged=%t published=%t installed=%t observed=%s", o.Terminal, o.CommandAcknowledged, o.DeclarationPublished, o.Installed, o.ObservedAt.Format(time.RFC3339))
	}
	t.Logf("operation: phase=%s blocked=%q worker=%q lease=%s observation: %s", op.Phase, op.Blocked, op.Worker, op.LeaseUntil.Format(time.RFC3339), observation)
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
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 G703 -- Explicit operator-selected private diagnostic file; no network input determines its path.
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
	t.Parallel()
	for _, scenario := range []struct {
		name                           string
		preexisting, unlimited, serial bool
	}{{"absent", false, false, false}, {"preexisting", true, false, false}, {"unlimited", false, true, false}, {"serial absent", false, false, true}, {"serial preexisting", true, false, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			preexisting := scenario.preexisting
			s, f, c := appsRuntime(t)
			f.mu.Lock()
			f.assigned = preexisting
			if scenario.serial {
				c.User, c.Serial = "", "DESIGNATED-SERIAL"
				f.assignment.ClientUserID, f.assignment.UserStatus, f.assignment.SerialNumber = "", "", c.Serial
				// Apple reports the canonical serial; the operator's spelling differs.
				written := c
				written.Serial = " designated-serial "
				adeTestJSON(t, s.Env.Workspace.path("appsbooks", "acceptance.json"), written)
			}
			if scenario.unlimited {
				f.rewrite = func(r *http.Request, m map[string]any) {
					if r.URL.Path == "/assets" {
						rows, asset := appsFixtureAsset(t, m)
						delete(asset, "availableCount")
						m["unlimitedAssets"] = rows
						m["assets"] = []any{}
					}
				}
			}
			f.mu.Unlock()
			appsInstallDueFixture(t, s, f, c)
			ctx := appsLongContext(t)
			f.mu.Lock()
			f.requests = nil
			f.mu.Unlock()
			if err := liveAppsBooksCollection(ctx, s); err != nil {
				t.Fatal(err)
			}
			if f.postCount() != 0 {
				t.Fatal("real read-only composition mutated Apple licensing")
			}
			appsAssertReadOnlyRequests(t, f)
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
	t.Parallel()
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
					_, asset := appsFixtureAsset(t, m)
					asset["revocable"] = false
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
			if name == "missing callback" && (errors.Is(err, ErrBlocked) || !errors.Is(err, errOperation)) {
				t.Fatal("missing callback after Apple licensing was not a failure", err)
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

// TestAppsBooksRuntimeLateReferenceCallback accepts an authenticated callback
// that arrives after the reference job's fallback status poll completed it.
func TestAppsBooksRuntimeLateReferenceCallback(t *testing.T) {
	t.Parallel()
	s, f, c := appsRuntime(t)
	appsInstallDueFixture(t, s, f, c)
	f.mu.Lock()
	f.dropCallbackEvent = 2
	f.mu.Unlock()
	var polled, delivered atomic.Bool
	transport := s.Env.Client.Transport
	s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(r.URL.Path, "/jobs/") {
			return response, err
		}
		if strings.HasSuffix(r.URL.Path, "/poll") {
			polled.Store(true)
		}
		// Deliver only after the lab has read the succeeded job without a callback.
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/evidence") && polled.Load() && delivered.CompareAndSwap(false, true) {
			f.mu.Lock()
			f.dropCallbackEvent = 0
			f.notify(t, r, "notification-2", "ASSET_MANAGEMENT", appsbooks.AssetManagementNotification{EventID: "event-2", Result: "SUCCESS", Type: "ASSOCIATE", Assignments: []appsbooks.Assignment{f.assignment}})
			f.mu.Unlock()
		}
		return response, nil
	})
	if err := liveAppsBooksLicensing(appsLongContext(t), s); err != nil {
		t.Fatal(err)
	}
	if !delivered.Load() {
		t.Fatal("late callback was not exercised")
	}
	if _, err := os.Stat(s.Env.Workspace.path("appsbooks", "pending-licensing.json")); !os.IsNotExist(err) {
		t.Fatal("settled late callback retained fence", err)
	}
	if len(adeTestEvidence(t, s, "appsbooks-licensing-*/reference-callback-*.json")) != 1 || len(adeTestEvidence(t, s, "appsbooks-licensing-*/reference-incomplete-callback.json")) != 0 {
		t.Fatal("late callback evidence missing or reported incomplete")
	}
}

// TestAppsBooksShortDeadlineBlocksBeforeAppleMutation rejects an insufficient mutation budget before Apple writes.
func TestAppsBooksShortDeadlineBlocksBeforeAppleMutation(t *testing.T) {
	s, f := appsFixtureSession(t)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), appsMinimumBudget-time.Second)
	defer cancel()
	if err := liveAppsBooksLicensing(ctx, s); !errors.Is(err, ErrBlocked) || !strings.Contains(err.Error(), "-module-timeout") {
		t.Fatal("insufficient restoration budget accepted or unexplained", err)
	}
	if f.postCount() != 0 {
		t.Fatal("insufficient budget reached Apple mutation")
	}
	if _, err := os.Stat(s.Env.Workspace.path("appsbooks", "pending-licensing.json")); !os.IsNotExist(err) {
		t.Fatal("short budget acquired mutation fence")
	}
}

// TestAppsBooksRuntimeExchangeFailuresPreserveRecovery proves rejected and lost
// exchanges never become success or authorize a guessed cleanup mutation.
func TestAppsBooksRuntimeExchangeFailuresPreserveRecovery(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"reference location unavailable", "reference intent denied", "reference evidence denied", "reference submit endpoint unavailable", "reference polling endpoint unavailable", "reference callback evidence denied", "reference completed evidence unavailable", "final location unavailable", "final job evidence unavailable", "preflight unavailable", "before SDK submit", "accepted SDK response lost", "SDK status unavailable", "SDK target readback unavailable", "reference submit unavailable", "accepted reference response lost", "reference status unavailable", "reference target readback unavailable", "cleanup rejected", "accepted cleanup response lost", "cleanup status unavailable", "cleanup target readback unavailable"} {
		t.Run(name, func(t *testing.T) {
			var armed, hit atomic.Bool
			s, f, c := appsRuntimeWith(t, func(f *appsFixture) {
				transport := f.config.HTTPClient.Transport
				f.config.HTTPClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
					f.mu.Lock()
					events := f.events
					f.mu.Unlock()
					selected := armed.Load() && ((name == "preflight unavailable" && r.URL.Path == "/config" && events == 0) ||
						((name == "before SDK submit" || name == "accepted SDK response lost") && r.URL.Path == "/associate" && events == 0) ||
						(name == "SDK status unavailable" && r.URL.Path == "/status" && events == 1) ||
						(name == "SDK target readback unavailable" && r.URL.Path == "/assignments" && events == 1) ||
						((name == "reference submit unavailable" || name == "accepted reference response lost") && r.URL.Path == "/associate" && events == 1) ||
						(name == "reference status unavailable" && r.URL.Path == "/status" && events == 2) ||
						(name == "reference target readback unavailable" && r.URL.Path == "/assignments" && events == 2) ||
						((name == "cleanup rejected" || name == "accepted cleanup response lost") && r.URL.Path == "/disassociate") ||
						(name == "cleanup status unavailable" && r.URL.Path == "/status" && events == 3) ||
						(name == "cleanup target readback unavailable" && r.URL.Path == "/assignments" && events == 3))
					selected = selected && hit.CompareAndSwap(false, true)
					if selected && !strings.Contains(name, "response lost") {
						return nil, errors.New("private-token private-user upstream rejected")
					}
					response, err := transport.RoundTrip(r)
					if selected {
						if response != nil {
							if closeErr := response.Body.Close(); closeErr != nil {
								t.Error(closeErr)
							}
						}
						return nil, errors.New("private-token private-user response lost after acceptance")
					}
					return response, err
				})
			})
			appsInstallDueFixture(t, s, f, c)
			referenceTransport := s.Env.Client.Transport
			s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				// The Apple fixture delivers callbacks while holding its state mutex.
				// Only administrative requests participate in this fault matrix.
				if !strings.HasPrefix(r.URL.Path, "/admin/v1/") || r.Header.Get("X-Lab-Fixture") == "early-poll-barrier" {
					return referenceTransport.RoundTrip(r)
				}
				f.mu.Lock()
				events := f.events
				f.mu.Unlock()
				path := r.URL.Path
				selected := armed.Load() && ((name == "reference location unavailable" && strings.HasSuffix(path, appsBase(c)) && events == 0) ||
					(name == "reference intent denied" && r.Method == "PUT" && strings.Contains(path, "/jobs/") && events == 0) ||
					(name == "reference evidence denied" && strings.Contains(path, "/jobs/") && strings.HasSuffix(path, "/evidence") && events == 0) ||
					(name == "reference submit endpoint unavailable" && strings.HasSuffix(path, "/submit")) ||
					(name == "reference polling endpoint unavailable" && strings.HasSuffix(path, "/poll")) ||
					(name == "reference callback evidence denied" && strings.Contains(path, "/notifications/") && events == 2) ||
					(name == "reference completed evidence unavailable" && strings.Contains(path, "/jobs/") && strings.HasSuffix(path, "/evidence") && events == 2) ||
					(name == "final location unavailable" && strings.HasSuffix(path, appsBase(c)) && events == 3) ||
					(name == "final job evidence unavailable" && strings.Contains(path, "/jobs/") && strings.HasSuffix(path, "/evidence") && events == 3))
				if selected && hit.CompareAndSwap(false, true) {
					return nil, errors.New("private-token private-user reference unavailable")
				}
				return referenceTransport.RoundTrip(r)
			})
			armed.Store(true)
			err := liveAppsBooksLicensing(appsLongContext(t), s)
			if !hit.Load() || err == nil {
				t.Fatal("named failure was not reached or passed", err)
			}
			if strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "private-user") {
				t.Fatal("upstream error leaked private facts")
			}
			posts := f.postCount()
			fence, readErr := os.ReadFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
			if name == "preflight unavailable" || name == "reference location unavailable" || name == "reference intent denied" || name == "reference evidence denied" {
				if posts != 0 || !os.IsNotExist(readErr) {
					t.Fatal("failed preflight wrote or claimed mutation ownership")
				}
				return
			}
			if readErr != nil || len(fence) == 0 {
				t.Fatal("uncertain outcome lost durable recovery fence", readErr)
			}
			maximum := 1
			if strings.HasPrefix(name, "reference") || name == "accepted reference response lost" {
				maximum = 2
			}
			if strings.HasPrefix(name, "cleanup") || strings.HasPrefix(name, "final ") || name == "accepted cleanup response lost" {
				maximum = 3
			}
			if posts > maximum {
				t.Fatal("failed phase triggered an unproven inverse")
			}
			if strings.HasPrefix(name, "final ") {
				for _, b := range adeTestEvidence(t, s, "appsbooks-licensing-*/assertions.json") {
					if !bytes.Contains(b, []byte(`"cleanup":"unresolved"`)) {
						t.Fatal("cleanup passed while the fence was retained", string(b))
					}
				}
			}
			f.mu.Lock()
			assigned := f.assigned
			f.mu.Unlock()
			if posts > 0 && posts < 3 && !assigned {
				t.Fatal("unknown outcome revoked the selected assignment")
			}
			s.Evidence = t.TempDir()
			if err := liveAppsBooksLicensing(appsLongContext(t), s); !errors.Is(err, ErrBlocked) {
				t.Fatal("unresolved run allowed replay", err)
			}
			after, err := os.ReadFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
			if err != nil || !bytes.Equal(fence, after) || f.postCount() != posts {
				t.Fatal("recovery ownership changed on rerun", err)
			}
		})
	}
}

// TestAppsBooksNativeInspectionFailures preserves denial and freshness boundaries
// through the actual reference runtime after genuine native installation.
func TestAppsBooksNativeInspectionFailures(t *testing.T) {
	t.Parallel()
	appsNativeInstallationRuntime(t, managedcontent.MDM, true, nil)
}

// appsBlockEvidence simulates unavailable private storage in the one unfinished
// acceptance scope, preserving every completed invocation's evidence.
func appsBlockEvidence(s *Session, name string) error {
	return appsBlockScopeEvidence(s, "installation", name)
}

// appsBlockScopeEvidence fails storage in exactly the active private phase.
func appsBlockScopeEvidence(s *Session, phase, name string) error {
	paths, err := filepath.Glob(filepath.Join(s.Evidence, "appsbooks-"+phase+"-*"))
	if err != nil {
		return err
	}
	var pending []string
	for _, path := range paths {
		if _, err := os.Stat(filepath.Join(path, "assertions.json")); os.IsNotExist(err) {
			pending = append(pending, path)
		}
	}
	if len(pending) != 1 {
		return fmt.Errorf("fixture requires exactly one unfinished private scope")
	}
	return os.Mkdir(filepath.Join(pending[0], name), 0o700)
}

// appsNativeEndpointFailures fails explicit inspection boundaries while the
// actual installed app and original licensing remain untouched.
func appsNativeEndpointFailures(t *testing.T, ctx context.Context, s *Session, base string) {
	t.Helper()
	for _, name := range []string{"selected serial mismatch", "different selected native device", "query metadata denied", "query metadata missing", "required query unsupported", "operation read denied", "dispatch evidence denied", "native observation denied", "foreign observation identity", "initial identity denied", "disabled identity", "device query denied", "installed query denied", "managed query denied", "identity readback denied", "identity continuity changed", "operation readback denied", "native readback denied", "native readback removed", "private SDK evidence unavailable", "private native observation unavailable", "private initial operation evidence unavailable", "private operation evidence unavailable", "private native source unavailable"} {
		t.Run(name, func(t *testing.T) {
			transport := s.Env.Client.Transport
			var operations, observations, identities atomic.Int32
			var hit atomic.Bool
			originalInfo := s.Info
			if name == "selected serial mismatch" {
				s.Info.Serial = "FOREIGN-PHYSICAL-SERIAL"
				hit.Store(true)
			}
			if name == "different selected native device" {
				s.Info.UDID = "FOREIGN-NATIVE-ENROLLMENT"
				hit.Store(true)
			}
			s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				path := strings.TrimPrefix(r.URL.Path, "/admin/v1")
				metadata := strings.HasPrefix(path, "/enrollments/") && strings.Count(path, "/") == 3
				operation, observation, identity := int32(0), int32(0), int32(0)
				if path == base {
					operation = operations.Add(1)
				}
				if path == base+"/native-observation" {
					observation = observations.Add(1)
				}
				if strings.HasSuffix(path, "/enrollment-evidence") {
					identity = identities.Add(1)
				}
				denied := (name == "query metadata denied" && metadata) || (name == "operation read denied" && operation == 1) || (name == "dispatch evidence denied" && path == base+"/evidence") ||
					(name == "native observation denied" && observation == 1) || (name == "initial identity denied" && identity == 1) ||
					(name == "identity readback denied" && identity == 2) || (name == "operation readback denied" && operation == 2) || (name == "native readback denied" && observation == 2)
				if r.Method == "POST" && strings.HasSuffix(path, "/commands") && r.GetBody != nil {
					body, err := r.GetBody()
					if err != nil {
						return nil, err
					}
					raw, readErr := io.ReadAll(body)
					closeErr := body.Close()
					if err := errors.Join(readErr, closeErr); err != nil {
						return nil, err
					}
					command, err := mdm.DecodeCommand(raw)
					if err != nil {
						return nil, err
					}
					denied = denied || name == "device query denied" && command.RequestType == "DeviceInformation" || name == "installed query denied" && command.RequestType == "InstalledApplicationList" || name == "managed query denied" && command.RequestType == "ManagedApplicationList"
				}
				if denied {
					hit.Store(true)
					return nil, errors.New("private-user private-token denied native inspection")
				}
				response, err := transport.RoundTrip(r)
				if err != nil {
					return response, err
				}
				evidence := ""
				if operation == 1 {
					switch name {
					case "private SDK evidence unavailable":
						evidence = "sdk-evidence.json"
					case "private initial operation evidence unavailable":
						evidence = "operation-before.json"
					case "private operation evidence unavailable":
						evidence = "operation-final.json"
					case "private native source unavailable":
						evidence = "native-evidence.json"
					}
				}
				if name == "private native observation unavailable" && observation == 1 {
					evidence = "native-observation-before.json"
				}
				if evidence != "" {
					hit.Store(true)
					if err := appsBlockEvidence(s, evidence); err != nil {
						t.Error(err)
					}
				}
				rewrite := metadata && (name == "query metadata missing" || name == "required query unsupported") || name == "foreign observation identity" && observation == 1 || name == "disabled identity" && identity == 1 || name == "identity continuity changed" && identity == 2 || name == "native readback removed" && observation == 2
				if rewrite {
					raw, readErr := io.ReadAll(response.Body)
					closeErr := response.Body.Close()
					if err := errors.Join(readErr, closeErr); err != nil {
						return nil, err
					}
					if metadata {
						var fields map[string]json.RawMessage
						if err := json.Unmarshal(raw, &fields); err != nil {
							return nil, err
						}
						if name == "query metadata missing" {
							fields["ProductName"] = json.RawMessage(`""`)
						} else {
							fields["OSVersion"] = json.RawMessage(`"1.0"`)
						}
						raw, err = json.Marshal(fields)
					} else if identity > 0 {
						var fields map[string]json.RawMessage
						if err := json.Unmarshal(raw, &fields); err != nil {
							return nil, err
						}
						key, value := "Certificate", json.RawMessage(`"foreign-certificate"`)
						if name == "disabled identity" {
							key, value = "Enabled", json.RawMessage(`false`)
						}
						if _, ok := fields[key]; !ok {
							return nil, fmt.Errorf("native identity fixture field missing")
						}
						fields[key] = value
						raw, err = json.Marshal(fields)
					} else {
						var current app.ManagedContentNativeObservation
						if err := json.Unmarshal(raw, &current); err != nil {
							return nil, err
						}
						if name == "foreign observation identity" {
							current.OperationID = "foreign"
						} else {
							current.Observation.Installed = false
						}
						raw, err = json.Marshal(current)
					}
					if err != nil {
						return nil, err
					}
					hit.Store(true)
					response.Body = io.NopCloser(bytes.NewReader(raw))
					response.ContentLength = int64(len(raw))
				}
				return response, nil
			})
			err := liveAppsBooksInstallation(ctx, s)
			s.Info = originalInfo
			s.Env.Client.Transport = transport
			if err == nil || !hit.Load() {
				t.Fatal("named native inspection failure was not rejected", err)
			}
			if strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "private-user") {
				t.Fatal("private upstream error escaped inspection")
			}
		})
	}
}
