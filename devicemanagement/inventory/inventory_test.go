package inventory

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm/axmtest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

// repository creates an isolated transactional inventory fixture.
func repository(t *testing.T) *Repository {
	t.Helper()
	r, e := New(NewMemory())
	if e != nil {
		t.Fatal(e)
	}
	return r
}

// observe records a fixture snapshot and fails immediately on invalid evidence.
func observe(t *testing.T, r *Repository, serial, kind, account, id, raw string, at time.Time) DeviceRecord {
	t.Helper()
	o, e := ResourceObservation(SourceReference{kind, account, id}, json.RawMessage(raw), at, 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	d, e := r.Observe(t.Context(), serial, o)
	if e != nil {
		t.Fatal(e)
	}
	return d
}

// TestLosslessReconciliationAndQueries checks lossless Apple data, query indexes, provenance and redacted views.
func TestLosslessReconciliationAndQueries(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	raw := `{"id":"opaque/apple/id","attributes":{"serialNumber":"SER123","ethernetMacAddress":["a","b"],"imei":["i1","i2"],"eid":"e","isMdmMigrationCapable":false,"future":{"nil":null,"number":9007199254740993}}}`
	d := observe(t, r, " ser123 ", "axm.device", "a", "opaque/apple/id", raw, at)
	if string(d.Fields["migration_capable"].Value) != "false" || string(d.Fields["axm.device.attributes.future.nil"].Value) != "null" {
		t.Fatalf("fields lost: %+v", d.Fields)
	}
	if string(d.Sources[(SourceReference{"axm.device", "a", "opaque/apple/id"}).Key()].Raw) != raw {
		t.Fatal("raw changed")
	}
	native := observe(t, r, "SER123", "mdm.DeviceInformation", "", "device:enrollment1", `{"QueryResponses":{"OSVersion":"27.0","DeviceName":"Mac"}}`, at.Add(time.Minute))
	if native.ID != d.ID {
		t.Fatal("native enrollment duplicated AxM device")
	}
	page, e := r.Devices(t.Context(), DeviceQuery{Conditions: []Condition{{"ethernet_mac_addresses", "contains", json.RawMessage(`"b"`)}}})
	if e != nil || len(page.Items) != 1 {
		t.Fatalf("array index: %+v %v", page, e)
	}
	page, e = r.Devices(t.Context(), DeviceQuery{Conditions: []Condition{{"migration_capable", "eq", json.RawMessage(`false`)}}})
	if e != nil || len(page.Items) != 1 {
		t.Fatalf("false index: %+v %v", page, e)
	}
	apple, e := r.Devices(t.Context(), DeviceQuery{Focus: "apple"})
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := apple.Items[0].Fields["os_version"]; ok {
		t.Fatal("Apple projection inherited native OS")
	}
	// Opaque resource identity stays authoritative when serial changes unexpectedly.
	conflict := observe(t, r, "DIFFERENT", "axm.device", "a", "opaque/apple/id", `{"attributes":{"serialNumber":"DIFFERENT"}}`, at.Add(time.Hour))
	if conflict.ID != d.ID || conflict.SerialNumber != "SER123" || len(conflict.Conflicts) == 0 {
		t.Fatalf("unsafe identity replacement %+v", conflict)
	}
	public := PublicRecord(native)
	if _, ok := public.Fields["axm.device.attributes.future.nil"]; ok {
		t.Fatal("unreviewed field leaked")
	}
	for _, o := range public.Sources {
		if len(o.Raw) > 0 || len(o.Fields) > 0 {
			t.Fatal("raw leaked")
		}
	}
	var output bytes.Buffer
	if e := r.Export(t.Context(), &output, DeviceQuery{}, "csv", []string{"imei", "serial_number"}, false); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(output.String(), "imei,serial_number\n") {
		t.Fatal(output.String())
	}
	output.Reset()
	if e := r.Diagnostics(t.Context(), &output); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"SER123", "opaque/apple/id", "enrollment1"} {
		if strings.Contains(output.String(), secret) {
			t.Fatal("diagnostic identifier leaked")
		}
	}
}

// TestCoverageAndPartialDDM checks open-ended plans and full versus partial DDM replacement.
func TestCoverageAndPartialDDM(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	d := observe(t, r, "S", "axm.device", "a", "opaque", `{"attributes":{"serialNumber":"S"}}`, at)
	if d.CoverageAt(at).State != "unknown" {
		t.Fatal("unfetched coverage became none")
	}
	d = observe(t, r, "S", "axm.coverage", "a", "opaque", `[]`, at)
	if d.CoverageAt(at).State != "none" {
		t.Fatal("empty success not represented")
	}
	d = observe(t, r, "S", "axm.coverage", "a", "opaque", `[{"id":"limited","attributes":{"status":"INACTIVE"}},{"id":"subscription","attributes":{"status":"ACTIVE","endDateTime":null,"agreementNumber":null,"isCanceled":false}}]`, at.Add(time.Second))
	if d.CoverageAt(at).State != "active" || d.CoverageAt(at).NextExpiry != nil || len(d.Coverage) != 2 {
		t.Fatal("open ended coverage lost")
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "enrolled"}
	report := func(full bool, at time.Time, values ...ddm.StatusValue) {
		t.Helper()
		e := r.ObserveStatus(t.Context(), "S", id, ddm.StatusUpdate{Raw: []byte(`{"StatusItems":{}}`), ReceivedAt: at, FullReport: full, Values: values})
		if e != nil {
			t.Fatal(e)
		}
	}
	report(true, at, ddm.StatusValue{Path: "device.operating-system.version", Value: []byte(`"26.0"`)}, ddm.StatusValue{Path: "unknown.boolean", Value: []byte(`false`)})
	report(false, at.Add(time.Hour), ddm.StatusValue{Path: "device.model.family", Value: []byte(`"Mac"`)})
	d, e := r.Device(t.Context(), d.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !d.Fields["os_version"].ObservedAt.Equal(at) {
		t.Fatal("partial report made old value fresh")
	}
	report(true, at.Add(2*time.Hour), ddm.StatusValue{Path: "device.model.family", Value: []byte(`"Mac"`)})
	d, e = r.Device(t.Context(), d.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := d.Fields["os_version"]; ok {
		t.Fatal("full report retained omitted item")
	}
}

// TestAtomicBackendAndPagination checks rollback and gap-free bounded enumeration.
func TestAtomicBackendAndPagination(t *testing.T) {
	r := repository(t)
	boom := errors.New("rollback")
	e := r.Backend.Update(t.Context(), func(tx Tx) error {
		if e := put(t.Context(), tx, "example/key", true); e != nil {
			return e
		}
		return boom
	})
	if !errors.Is(e, boom) {
		t.Fatal(e)
	}
	if _, e := r.Backend.Get(t.Context(), "example/key"); !errors.Is(e, ErrNotFound) {
		t.Fatal("failed transaction visible")
	}
	for i := range 5 {
		observe(t, r, string(rune('A'+i)), "axm.device", "a", string(rune('a'+i)), `{"attributes":{}}`, time.Now())
	}
	seen := map[string]bool{}
	q := DeviceQuery{Limit: 2}
	for {
		p, e := r.Devices(t.Context(), q)
		if e != nil {
			t.Fatal(e)
		}
		for _, d := range p.Items {
			if seen[d.ID] {
				t.Fatal("duplicate cursor row")
			}
			seen[d.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		q.Cursor = p.NextCursor
	}
	if len(seen) != 5 {
		t.Fatal(len(seen))
	}
}

// syncFixture creates an independently authenticated fake Apple account and sync worker.
func syncFixture(t *testing.T) (*Repository, *Syncer, *axmtest.Server, Account) {
	t.Helper()
	r := repository(t)
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	der, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	server := axmtest.NewServer()
	t.Cleanup(server.Close)
	server.RegisterKey("BUSINESSAPI.test", "key", &key.PublicKey)
	a, e := r.SaveAccount(t.Context(), Account{Name: "Test", ClientID: "BUSINESSAPI.test", KeyID: "key", BaseURL: server.URL, TokenURL: server.TokenURL, Enabled: true}, keyPEM, time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	s := &Syncer{Repository: r, Client: func(ctx context.Context, a Account, key []byte) (*axm.Client, error) {
		return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, BaseURL: a.BaseURL, TokenURL: a.TokenURL, HTTPClient: server.Client()})
	}}
	return r, s, server, a
}

// TestSyncNeverEnrolledAndLease checks agentless discovery, built-in Apple MDM enrichment and worker fencing.
func TestSyncNeverEnrolledAndLease(t *testing.T) {
	r, s, server, a := syncFixture(t)
	server.AddOrgDevice("opaque", map[string]any{"serialNumber": "SER1", "ethernetMacAddress": []string{"mac1", "mac2"}, "future": false})
	server.AddAppleCareCoverage("opaque", "plan", map[string]any{"endDateTime": nil})
	server.AddMDMServer("Apple", map[string]any{"serverType": "APPLE_MDM"})
	server.AddMDMDevice("builtin", map[string]any{"serialNumber": "SER1"}, map[string]any{"serialNumber": "SER1", "futureDetail": true})
	j, e := r.Enqueue(t.Context(), a.ID, false, time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	second, e := r.Enqueue(t.Context(), a.ID, false, time.Now().UTC())
	if e != nil || j.ID != second.ID {
		t.Fatal("enqueue did not coalesce")
	}
	run, e := s.RunOne(t.Context())
	if e != nil || run.State != "success" {
		t.Fatalf("sync %+v: %v", run, e)
	}
	page, e := r.Devices(t.Context(), DeviceQuery{})
	if e != nil || len(page.Items) != 1 {
		t.Fatalf("records %+v: %v", page, e)
	}
	d := page.Items[0]
	if d.SerialNumber != "SER1" || d.CoverageAt(time.Now()).State != "active" {
		t.Fatalf("bad record %+v", d)
	}
	if string(d.Fields["axm.mdm.detail.attributes.futureDetail"].Value) != "true" {
		t.Fatal("built-in details missing")
	}
	native := observe(t, r, "SER1", "mdm.DeviceInformation", "", "device:native", `{"QueryResponses":{"OSVersion":"28.0"}}`, time.Now().Add(time.Hour))
	if native.ID != d.ID {
		t.Fatal("native enrichment created duplicate")
	}
	j, e = r.Enqueue(t.Context(), a.ID, false, time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := r.claim(t.Context(), "worker", time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.claim(t.Context(), "other", time.Now().UTC()); !errors.Is(e, ErrLease) {
		t.Fatal("concurrent worker admitted", e)
	}
	if e := r.Control(t.Context(), j.ID, "pause", time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	if e := r.commitJob(t.Context(), &claimed, nil); !errors.Is(e, ErrStopped) {
		t.Fatal("paused worker committed", e)
	}
	if e := r.release(t.Context(), claimed); e != nil {
		t.Fatal(e)
	}
	if e := r.Control(t.Context(), j.ID, "resume", time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	run, e = s.RunOne(t.Context())
	if e != nil || run.State != "success" {
		t.Fatalf("resume %+v %v", run, e)
	}
	if e := r.DeleteAccount(t.Context(), a.ID, a.Revision, time.Now()); e != nil {
		t.Fatal(e)
	}
	d, e = r.Device(t.Context(), d.ID)
	if e != nil || String(d.Fields["os_version"].Value) != "28.0" {
		t.Fatal("account deletion damaged native device", e)
	}
}

// TestSchedules checks cron validation, ordinary cadence and daylight-saving transitions.
func TestSchedules(t *testing.T) {
	cases := []struct{ cron, zone, after, want string }{{"0 2 * * *", "UTC", "2026-09-21T01:00:00Z", "2026-09-21T02:00:00Z"}, {"*/15 * * * *", "Europe/London", "2026-09-21T01:01:00Z", "2026-09-21T01:15:00Z"}, {"30 1 * * *", "America/New_York", "2026-11-01T05:31:00Z", "2026-11-01T06:30:00Z"}}
	for _, c := range cases {
		at, _ := time.Parse(time.RFC3339, c.after)
		got, e := NextRun(c.cron, c.zone, at)
		if e != nil || got.Format(time.RFC3339) != c.want {
			t.Fatalf("%+v got %v %v", c, got, e)
		}
	}
	if _, e := NextRun("0 60 * * *", "UTC", time.Now()); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

// TestAmbiguousSerialsDoNotAutoMerge keeps duplicate Apple serial identities separate and inspectable.
func TestAmbiguousSerialsDoNotAutoMerge(t *testing.T) {
	r := repository(t)
	at := time.Now()
	first := observe(t, r, "S", "axm.device", "a", "one", `{"attributes":{"serialNumber":"S"}}`, at)
	second := observe(t, r, "S", "axm.device", "a", "two", `{"attributes":{"serialNumber":"S"}}`, at)
	if first.ID == second.ID || len(second.Conflicts) == 0 {
		t.Fatal("ambiguous Apple resources merged")
	}
	native := observe(t, r, "S", "mdm.DeviceInformation", "", "device:three", `{"QueryResponses":{"SerialNumber":"S"}}`, at)
	if native.ID == first.ID || native.ID == second.ID || len(native.Conflicts) == 0 {
		t.Fatal("ambiguous enrollment auto-matched")
	}
}

// TestEnrollmentLearnsSerialAfterCloudDiscovery checks provisional-record reconciliation and stable aliases.
func TestEnrollmentLearnsSerialAfterCloudDiscovery(t *testing.T) {
	r := repository(t)
	at := time.Now()
	provisional := observe(t, r, "", "enrollment", "", "device:enrolled", `{"managed":true}`, at)
	cloud := observe(t, r, "SER", "axm.device", "a", "opaque", `{"attributes":{"serialNumber":"SER"}}`, at.Add(time.Minute))
	enriched := observe(t, r, "SER", "mdm.DeviceInformation", "", "device:enrolled", `{"QueryResponses":{"SerialNumber":"SER","OSVersion":"27.0"}}`, at.Add(2*time.Minute))
	if enriched.ID != cloud.ID {
		t.Fatal("did not enrich cloud record")
	}
	if string(enriched.Fields["managed"].Value) != "true" {
		t.Fatal("lost provisional enrollment")
	}
	alias, e := r.Device(t.Context(), provisional.ID)
	if e != nil || alias.ID != cloud.ID {
		t.Fatal("old device link stopped resolving", e)
	}
	page, e := r.Devices(t.Context(), DeviceQuery{})
	if e != nil || len(page.Items) != 1 {
		t.Fatal("merge left duplicate", e)
	}
	mapped, e := r.SourceDevice(t.Context(), SourceReference{Kind: "enrollment", ResourceID: "device:enrolled"})
	if e != nil || mapped.ID != cloud.ID {
		t.Fatal("source link not updated", e)
	}
}

// TestRepeatBuilder preserves local calendar cadence and clamps short months.
func TestRepeatBuilder(t *testing.T) {
	anchor, _ := time.Parse(time.RFC3339, "2026-01-31T02:00:00Z")
	s := Schedule{RepeatEvery: 1, RepeatUnit: "months", TimeZone: "UTC", Anchor: anchor}
	next, e := s.NextOccurrence(anchor)
	if e != nil || next.Format(time.RFC3339) != "2026-02-28T02:00:00Z" {
		t.Fatal(next, e)
	}
	next, e = s.NextOccurrence(next)
	if e != nil || next.Format(time.RFC3339) != "2026-03-31T02:00:00Z" {
		t.Fatal(next, e)
	}
	s.RepeatUnit = "hours"
	s.RepeatEvery = 6
	next, e = s.NextOccurrence(anchor.Add(13 * time.Hour))
	if e != nil || !next.Equal(anchor.Add(18*time.Hour)) {
		t.Fatal(next, e)
	}
}

// TestExactNumericQueries keeps distinct large integers and equivalent JSON numeric spellings correct.
func TestExactNumericQueries(t *testing.T) {
	r := repository(t)
	observe(t, r, "A", "axm.device", "a", "one", `{"attributes":{"counter":9007199254740993,"decimal":1.0}}`, time.Now())
	for _, tc := range []struct {
		field, value string
		want         int
	}{{"axm.device.attributes.counter", "9007199254740992", 0}, {"axm.device.attributes.counter", "9007199254740993", 1}, {"axm.device.attributes.decimal", "1", 1}} {
		page, e := r.Devices(t.Context(), DeviceQuery{Conditions: []Condition{{Field: tc.field, Operator: "eq", Value: json.RawMessage(tc.value)}}})
		if e != nil || len(page.Items) != tc.want {
			t.Fatal(tc, page, e)
		}
	}
}

// TestCancelAllRetainsCompletedWork stops only outstanding jobs and preserves their checkpoints.
func TestCancelAllRetainsCompletedWork(t *testing.T) {
	repo, worker, server, account := syncFixture(t)
	server.AddOrgDevice("one", nil)
	_, err := repo.Enqueue(t.Context(), account.ID, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	finished, err := worker.RunOne(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	queued, err := repo.Enqueue(t.Context(), account.ID, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	count, err := repo.CancelAll(t.Context(), time.Now())
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	old, err := repo.Job(t.Context(), finished.ID)
	if err != nil || old.State != "success" {
		t.Fatal("completed work changed", err)
	}
	cancelled, err := repo.Job(t.Context(), queued.ID)
	if err != nil || cancelled.State != "cancelled" {
		t.Fatal("queued job not cancelled", err)
	}
}
