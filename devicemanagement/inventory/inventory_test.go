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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"

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
func observe(t *testing.T, r *Repository, serial, kind, account, id, raw string, at time.Time) ResourceRecord {
	t.Helper()
	o, e := ResourceObservation(source.Reference{Kind: kind, AccountID: account, ResourceID: id}, json.RawMessage(raw), at, 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	d, e := r.Observe(t.Context(), serial, o)
	if e != nil {
		t.Fatal(e)
	}
	return d
}

// TestLosslessReconciliationAndQueries checks independent source fidelity, original-path predicates and redaction.
func TestLosslessReconciliationAndQueries(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	raw := `{"id":"opaque/apple/id","attributes":{"serialNumber":"SER123","nil":null,"false":false,"zero":0,"empty":[],"number":9007199254740993},"relationships":{"owner":{"data":null}}}`
	d := observe(t, r, "SER123", "axm.device", "a", "opaque/apple/id", raw, at)
	if string(d.Snapshot.Raw) != raw || string(d.Snapshot.Fields["attributes.number"]) != "9007199254740993" {
		t.Fatal("original lost", d)
	}
	native := observe(t, r, "SER123", "mdm.DeviceInformation", "", "device:enrollment1", `{"QueryResponses":{"OSVersion":"27.0"}}`, at)
	if native.ID == d.ID {
		t.Fatal("sources merged")
	}
	for _, tc := range []struct{ path, value string }{{"attributes.false", "false"}, {"attributes.nil", "null"}, {"attributes.zero", "0"}, {"attributes.empty", "[]"}} {
		page, err := r.Resources(t.Context(), ResourceQuery{Conditions: []Condition{{"data." + tc.path, "eq", json.RawMessage(tc.value)}}})
		if err != nil || len(page.Items) != 1 {
			t.Fatal(tc, page, err)
		}
	}
	public := PublicRecord(d)
	if len(public.Snapshot.Raw) != 0 || len(public.Snapshot.Fields) != 0 || public.Snapshot.Native != nil {
		t.Fatal("original leaked")
	}
	var output bytes.Buffer
	if err := r.Export(t.Context(), &output, ResourceQuery{}, "json", nil, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "9007199254740993") {
		t.Fatal("raw leak")
	}
	output.Reset()
	if err := r.Diagnostics(t.Context(), &output); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"SER123", "opaque/apple/id", "enrollment1"} {
		if strings.Contains(output.String(), private) {
			t.Fatal("diagnostic leak")
		}
	}
}

// TestCoverageAndPartialDDM checks open-ended plans and full versus partial DDM replacement.
func TestCoverageAndPartialDDM(t *testing.T) {
	r := repository(t)
	at := time.Now().UTC()
	coverage := observe(t, r, "S", "axm.coverage", "a", "opaque", `[{"id":"subscription","attributes":{"endDateTime":null,"isCanceled":false,"unknown":9007199254740993}}]`, at)
	if !strings.Contains(string(coverage.Snapshot.Raw), `"endDateTime":null`) {
		t.Fatal("plan changed")
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "enrolled"}
	for i, values := range [][]ddm.StatusValue{{{Path: "device.operating-system.version", Value: []byte(`"26.0"`)}, {Path: "unknown.boolean", Value: []byte(`false`)}}, {{Path: "device.model.family", Value: []byte(`"Mac"`)}}} {
		raw := []byte(fmt.Sprintf(`{"report":%d}`, i))
		if err := r.ObserveStatus(t.Context(), "S", id, ddm.StatusUpdate{Raw: raw, ReceivedAt: at.Add(time.Duration(i) * time.Hour), FullReport: i == 0, Values: values}); err != nil {
			t.Fatal(err)
		}
	}
	ref := EnrollmentReference("ddm.status", id)
	history, err := r.History(t.Context(), ref)
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	seen := false
	for _, o := range history {
		if string(o.Fields["device.operating-system.version"]) == `"26.0"` {
			seen = true
			if !o.FieldTimes["device.operating-system.version"].Equal(at) {
				t.Fatal("field age lost")
			}
		}
	}
	if !seen {
		t.Fatal("omitted device fact lost from history")
	}
	current, err := r.SourceResource(t.Context(), ref)
	if err != nil || string(current.Snapshot.Raw) != `{"report":1}` {
		t.Fatal(current, err)
	}
	if len(current.Snapshot.Fields) != 1 {
		t.Fatal("original partial report fabricated fields")
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
	q := ResourceQuery{Limit: 2}
	for {
		p, e := r.Resources(t.Context(), q)
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
	server.AddOrgDevice("opaque", map[string]any{"serialNumber": "SER1", "future": false})
	server.AddAppleCareCoverage("opaque", "plan", map[string]any{"endDateTime": nil})
	assignedServer := server.AddMDMServer("Apple", map[string]any{"serverType": "APPLE_MDM"})
	server.Assign("opaque", assignedServer)
	server.AddMDMDevice("builtin", map[string]any{"serialNumber": "SER1"}, map[string]any{"futureDetail": true})
	j, err := r.Enqueue(t.Context(), a.ID, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Enqueue(t.Context(), a.ID, false, time.Now())
	if err != nil || second.ID != j.ID {
		t.Fatal(second, err)
	}
	run, err := s.RunOne(t.Context())
	if err != nil || run.State != "success" {
		t.Fatal(run, err)
	}
	page, err := r.Resources(t.Context(), ResourceQuery{})
	if err != nil || len(page.Items) != 6 {
		t.Fatal(page, err)
	}
	detail, err := r.SourceResource(t.Context(), source.Reference{Kind: "axm.mdm.detail", AccountID: a.ID, ResourceID: "builtin"})
	if err != nil || string(detail.Snapshot.Fields["attributes.futureDetail"]) != "true" {
		t.Fatal(detail, err)
	}
	native := observe(t, r, "SER1", "mdm.DeviceInformation", "", "device:native", `{"QueryResponses":{"OSVersion":"28.0"}}`, time.Now())
	if native.ID == detail.ID {
		t.Fatal("native auto merged")
	}
	j, err = r.Enqueue(t.Context(), a.ID, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := r.claim(t.Context(), "worker", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.claim(t.Context(), "other", time.Now()); !errors.Is(err, ErrLease) {
		t.Fatal(err)
	}
	if err := r.Control(t.Context(), j.ID, "pause", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.commitJob(t.Context(), &claimed, nil); !errors.Is(err, ErrStopped) {
		t.Fatal(err)
	}
	if err := r.release(t.Context(), claimed); err != nil {
		t.Fatal(err)
	}
	if err := r.Control(t.Context(), j.ID, "resume", time.Now()); err != nil {
		t.Fatal(err)
	}
	run, err = s.RunOne(t.Context())
	if err != nil || run.State != "success" {
		t.Fatal(run, err)
	}
	if err := r.DeleteAccount(t.Context(), a.ID, a.Revision, time.Now()); err != nil {
		t.Fatal(err)
	}
	current, err := r.Resource(t.Context(), native.ID)
	if err != nil || string(current.Snapshot.Raw) != string(native.Snapshot.Raw) {
		t.Fatal("native damaged", err)
	}
	detail, err = r.Resource(t.Context(), detail.ID)
	if err != nil || !detail.Snapshot.Disconnected {
		t.Fatal(detail, err)
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
	one := observe(t, r, "S", "axm.device", "a", "one", `{"attributes":{"serialNumber":"S"}}`, at)
	two := observe(t, r, "S", "axm.device", "a", "two", `{"attributes":{"serialNumber":"S"}}`, at)
	native := observe(t, r, "S", "mdm.DeviceInformation", "", "device:three", `{"QueryResponses":{"SerialNumber":"S"}}`, at)
	if one.ID == two.ID || one.ID == native.ID || two.ID == native.ID {
		t.Fatal("automatic source merge")
	}
	for _, ref := range []source.Reference{one.Source, two.Source, native.Source} {
		links, err := r.Links(t.Context(), ref)
		if err != nil || len(links) != 0 {
			t.Fatal("implicit link", links, err)
		}
	}
}

// TestEnrollmentLearnsSerialAfterCloudDiscovery keeps serialless enrollment and later native evidence independently addressed.
func TestEnrollmentLearnsSerialAfterCloudDiscovery(t *testing.T) {
	r := repository(t)
	at := time.Now()
	enrollment := observe(t, r, "", "enrollment", "", "device:enrolled", `{"managed":true}`, at)
	apple := observe(t, r, "SER", "axm.device", "a", "opaque", `{"attributes":{"serialNumber":"SER"}}`, at)
	native := observe(t, r, "SER", "mdm.DeviceInformation", "", "device:enrolled", `{"QueryResponses":{"SerialNumber":"SER"}}`, at.Add(time.Minute))
	if enrollment.ID == apple.ID || native.ID == apple.ID || enrollment.ID == native.ID {
		t.Fatal("sources combined")
	}
	old, err := r.Resource(t.Context(), enrollment.ID)
	if err != nil || old.ID != enrollment.ID {
		t.Fatal("serialless identity changed", err)
	}
	page, err := r.Resources(t.Context(), ResourceQuery{})
	if err != nil || len(page.Items) != 3 {
		t.Fatal(page, err)
	}
	if _, err := r.Link(t.Context(), apple.Source, native.Source, at); err == nil {
		t.Fatal("missing authenticated method invented")
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
	}{{"attributes.counter", "9007199254740992", 0}, {"attributes.counter", "9007199254740993", 1}, {"attributes.decimal", "1", 1}} {
		page, e := r.Resources(t.Context(), ResourceQuery{Conditions: []Condition{{Field: "data." + tc.field, Operator: "eq", Value: json.RawMessage(tc.value)}}})
		if e != nil || len(page.Items) != tc.want {
			t.Fatal(tc, page, e)
		}
	}
}

// TestCancelAllRetainsCompletedWork stops only outstanding jobs and preserves their checkpoints.
func TestCancelAllRetainsCompletedWork(t *testing.T) {
	repo, worker, server, account := syncFixture(t)
	server.AddOrgDevice("one", nil)
	assignedServer := server.AddMDMServer("server", nil)
	server.Assign("one", assignedServer)
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
