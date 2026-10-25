package app

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
)

// inventoryBoundaryFault preserves real reads/transactions except the selected
// persistence namespace, including reads performed inside a resolver snapshot.
type inventoryBoundaryFault struct {
	inventory.Backend
	prefix string
}
type inventoryBoundaryReader struct {
	inventory.Reader
	prefix string
}
type inventoryBoundaryTx struct {
	inventory.Tx
	prefix string
}

// Get fails the selected persisted document without fabricating a replacement.
func (r inventoryBoundaryReader) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if strings.HasPrefix(key, r.prefix) {
		return nil, errInventoryStorage
	}
	return r.Reader.Get(ctx, key)
}

// Scan fails the selected ordered source/history namespace.
func (r inventoryBoundaryReader) Scan(ctx context.Context, prefix, after string, limit int) ([]inventory.Entry, error) {
	if strings.HasPrefix(prefix, r.prefix) {
		return nil, errInventoryStorage
	}
	return r.Reader.Scan(ctx, prefix, after, limit)
}

// Get preserves the same injected failure inside the transaction.
func (r inventoryBoundaryTx) Get(ctx context.Context, key string) (json.RawMessage, error) {
	return (inventoryBoundaryReader{r.Tx, r.prefix}).Get(ctx, key)
}

// Scan preserves the same injected failure inside the transaction.
func (r inventoryBoundaryTx) Scan(ctx context.Context, prefix, after string, limit int) ([]inventory.Entry, error) {
	return (inventoryBoundaryReader{r.Tx, r.prefix}).Scan(ctx, prefix, after, limit)
}

// Get reads through the selected failure boundary.
func (r inventoryBoundaryFault) Get(ctx context.Context, key string) (json.RawMessage, error) {
	return (inventoryBoundaryReader{r.Backend, r.prefix}).Get(ctx, key)
}

// Scan reads through the selected failure boundary.
func (r inventoryBoundaryFault) Scan(ctx context.Context, prefix, after string, limit int) ([]inventory.Entry, error) {
	return (inventoryBoundaryReader{r.Backend, r.prefix}).Scan(ctx, prefix, after, limit)
}

// Update retains the backend's atomicity and exposes the failed read in its snapshot.
func (r inventoryBoundaryFault) Update(ctx context.Context, fn func(inventory.Tx) error) error {
	return r.Backend.Update(ctx, func(tx inventory.Tx) error { return fn(inventoryBoundaryTx{tx, r.prefix}) })
}

// TestInventoryInspectionFailuresStayClosed verifies real authenticated handlers
// never return partial source/history payloads when any evidence component fails.
func TestInventoryInspectionFailuresStayClosed(t *testing.T) {
	a, err := Build(t.Context(), Config{Storage: "inmem", BootstrapToken: "bootstrap-secret"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	token := bootstrapRBAC(t, a)
	policy := `{"Source":"permit(principal, action in [MDM::Action::\"readInventory\", MDM::Action::\"readRawInventory\", MDM::Action::\"manageInventory\"], resource);"}`
	if w := rbacRequest(a, "PUT", "/policies/boundaries", token, policy); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	ctx := t.Context()
	at := time.Now().UTC()
	observe := func(ref source.Reference, raw string, native *inventory.NativeIdentity) inventory.ResourceRecord {
		t.Helper()
		o, err := inventory.ResourceObservation(ref, json.RawMessage(raw), at, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		o.Native = native
		d, err := a.Inventory.Observe(ctx, "", o)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cloud := observe(source.Reference{Kind: "axm.device", AccountID: "apple", ResourceID: "opaque"}, `{"id":"opaque","type":"orgDevices","attributes":{"serialNumber":"SER","deviceModel":"private-model"}}`, nil)
	native := observe(source.Reference{Kind: "mdm.identity", ResourceID: "device:native"}, `{"SerialNumber":"SER","Model":"native-model"}`, &inventory.NativeIdentity{Serial: "SER", Method: correlation.AutomatedDeviceEnrollment, Authenticated: true})
	if _, err := a.Inventory.Link(ctx, cloud.Source, native.Source, at); err != nil {
		t.Fatal(err)
	}
	mapping := fmt.Sprintf(`{"mappings":[{"name":"model","inputs":[{"resource_id":%q,"path":"deviceModel"},{"resource_id":%q,"path":"Model"}]}]}`, cloud.ID, native.ID)
	base := a.Inventory.Backend
	for _, tc := range []struct{ path, prefix, body string }{
		{"/inventory/raw/devices/" + cloud.ID + "/evidence", "link/", ""},
		{"/inventory/raw/devices/" + cloud.ID + "/evidence", "history/", ""},
		{"/inventory/raw/devices/" + cloud.ID + "/evidence", "attempt/", ""},
		{"/inventory/raw/devices/" + cloud.ID + "/evidence", "envelope/", ""},
		{"/inventory/raw/serials/SER", "history/", ""},
		{"/inventory/raw/serials/SER", "resource/", ""},
		{"/inventory/raw/devices/" + cloud.ID + "/resolve", "resource/", mapping},
		{"/inventory/raw/devices/" + cloud.ID + "/resolve", "link/", mapping},
		{"/inventory/raw/devices/" + cloud.ID + "/resolve", "history/", mapping},
	} {
		t.Run(tc.path+tc.prefix, func(t *testing.T) {
			a.Inventory.Backend = inventoryBoundaryFault{base, tc.prefix}
			defer func() { a.Inventory.Backend = base }()
			method := "GET"
			if tc.body != "" {
				method = "POST"
			}
			w := rbacRequest(a, method, tc.path, token, tc.body)
			if w.Code != 500 || strings.Contains(w.Body.String(), "private-model") || strings.Contains(w.Body.String(), "native-model") || strings.Contains(w.Body.String(), "history") {
				t.Fatal("partial evidence escaped storage failure", w.Code, w.Body.String())
			}
		})
	}
	// Invalid bodies, source IDs, explicit mappings and evidence-query selectors
	// fail at the same real authorization/handler boundary as successful requests.
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/inventory/correlations", `{"apple":"missing","native":"missing"}`, 404},
		{"POST", "/inventory/correlations", fmt.Sprintf(`{"apple":%q,"native":"missing"}`, cloud.ID), 404},
		{"POST", "/inventory/correlations", `{"apple":`, 400},
		{"POST", "/inventory/raw/devices/" + cloud.ID + "/resolve", `{"mappings":`, 400},
		{"POST", "/inventory/raw/devices/" + native.ID + "/resolve", mapping, 400},
		{"POST", "/inventory/raw/devices/" + cloud.ID + "/resolve", `{"mappings":[{"name":"model","inputs":[{"resource_id":"unlinked","path":"secret"}]}]}`, 400},
		{"POST", "/inventory/raw/devices/" + cloud.ID + "/resolve", fmt.Sprintf(`{"mappings":[{"name":"","inputs":[{"resource_id":%q,"path":"Model"}]}]}`, native.ID), 400},
		{"GET", "/devices/" + cloud.ID + "/evidence?source=axm.device", "", 400},
		{"GET", "/inventory/raw/devices/missing/evidence", "", 404},
		{"GET", "/inventory/raw/serials/%20", "", 400},
		{"PUT", "/inventory/raw/presets/private", `{"name":`, 400},
		{"PUT", "/inventory/presets/private", `{"name":"private","format":"csv","columns":["data.secret"],"query":{}}`, 400},
	} {
		w := rbacRequest(a, tc.method, tc.path, token, tc.body)
		if w.Code != tc.want {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	// A syntactically valid cursor cannot hide malformed opaque ordering bytes.
	for _, cursor := range []string{"%", base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"v":1,"after":%q,"query":%q}`, strings.Repeat("z", 64), inventoryQueryFingerprint(inventory.ResourceQuery{}))))} {
		escaped := strings.ReplaceAll(cursor, "%", "%25")
		w := rbacRequest(a, "GET", "/devices?cursor="+escaped, token, "")
		if w.Code != 400 {
			t.Fatal("bad cursor broadened query", w.Code, w.Body.String())
		}
	}
	// Collection addressing rejects an unavailable source and a persisted tuple
	// whose opaque reference disagrees with its protocol addressing body.
	for _, tc := range []struct {
		id, raw string
		absent  bool
	}{{"device:mismatch", `{"enrollment_id":"other","enrollment_channel":"device"}`, false}, {"device:gone", `{"enrollment_id":"gone","enrollment_channel":"device"}`, true}} {
		o, err := inventory.ResourceObservation(source.Reference{Kind: "enrollment", ResourceID: tc.id}, json.RawMessage(tc.raw), at, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		o.Absent = tc.absent
		d, err := a.Inventory.Observe(ctx, "", o)
		if err != nil {
			t.Fatal(err)
		}
		w := rbacRequest(a, "POST", "/devices/"+d.ID+"/collect", token, "")
		if w.Code != 400 {
			t.Fatal("invalid target collected", w.Code, w.Body.String())
		}
	}
	o, err := inventory.ResourceObservation(source.Reference{Kind: "enrollment", ResourceID: "device:not-enrolled"}, json.RawMessage(`{"enrollment_id":"not-enrolled","enrollment_channel":"device"}`), at, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.Inventory.Observe(ctx, "", o)
	if err != nil {
		t.Fatal(err)
	}
	if w := rbacRequest(a, "POST", "/devices/"+d.ID+"/collect", token, ""); w.Code != 500 {
		t.Fatal("absent protocol target appeared collected", w.Code, w.Body.String())
	}
}

type inventoryReplacementReadFailure struct{ storage.Store }

// TransitionReplacement propagates a handshake-read outage rather than asserting a pending identity.
func (s inventoryReplacementReadFailure) TransitionReplacement(context.Context, mdm.EnrollmentID, storage.ReplacementChange) (*storage.Replacement, error) {
	return nil, errInventoryStorage
}

// TestInventoryAuthenticateRejectsUnboundReceipts exercises rejection rather than
// manufacturing authenticated success: current pin, exact pending handshake and
// valid receipt shape all remain independently required.
func TestInventoryAuthenticateRejectsUnboundReceipts(t *testing.T) {
	a, id := nativeInventoryApp(t)
	ctx := t.Context()
	at := time.Now().UTC()
	pinned := &x509.Certificate{Raw: []byte("active")}
	candidate := &x509.Certificate{Raw: []byte("unissued")}
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e.CertHash = cms.Fingerprint(pinned)
	if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	raw, err := plist.Marshal(map[string]any{"MessageType": "Authenticate", "UDID": id.ID, "SerialNumber": "SER"})
	if err != nil {
		t.Fatal(err)
	}
	receipt := &mdm.Checkin{Raw: raw}
	request := &mdm.Request{ID: id, Certificate: pinned}
	if err := a.observeInventoryAuthenticate(ctx, nil, receipt, at); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal(err)
	}
	if err := a.observeInventoryAuthenticate(ctx, &mdm.Request{ID: mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "user"}}, receipt, at); err != nil {
		t.Fatal(err)
	}
	base := a.Store
	a.Store = &storagetest.Failing{Store: base, Fail: map[string]error{"Get": errInventoryStorage}}
	if err := a.observeInventoryAuthenticate(ctx, request, receipt, at); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	a.Store = base
	if err := a.observeInventoryAuthenticate(ctx, &mdm.Request{ID: id}, receipt, at); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal(err)
	}
	mismatch := &mdm.Request{ID: id, Certificate: candidate}
	if err := a.observeInventoryAuthenticate(ctx, mismatch, receipt, at); !errors.Is(err, inventory.ErrConflict) {
		t.Fatal("unissued certificate became a pending identity", err)
	}
	a.Store = inventoryReplacementReadFailure{base}
	if err := a.observeInventoryAuthenticate(ctx, mismatch, receipt, at); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	a.Store = base
	for _, bad := range []*mdm.Checkin{nil, {Raw: []byte("not a plist")}, {Raw: []byte(`<?xml version="1.0"?><plist><dict><key>MessageType</key><string>TokenUpdate</string></dict></plist>`)}} {
		if err := a.observeInventoryAuthenticate(ctx, request, bad, at); !errors.Is(err, inventory.ErrInvalid) {
			t.Fatal("non-Authenticate qualified facts", err)
		}
	}
	if _, err := a.Inventory.SourceResource(ctx, inventory.EnrollmentReference("mdm.identity", id)); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatal("rejected receipt changed identity evidence", err)
	}
}

// TestInventorySerialBindingFailsClosed proves partial or contradictory retained
// evidence cannot supply a serial to a fresh serial-omitting native report.
func TestInventorySerialBindingFailsClosed(t *testing.T) {
	a, id := nativeInventoryApp(t)
	ctx := t.Context()
	at := time.Now().UTC()
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e.CertHash = "issued"
	setupJSON(t, a.protocol, "issued-identity:issued", identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, UDID: id.ID, Serial: "SER"})
	ref := inventory.EnrollmentReference("mdm.identity", id)
	o, err := inventory.ResourceObservation(ref, json.RawMessage(`{"SerialNumber":"SER"}`), at, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	o.Native = &inventory.NativeIdentity{Serial: "SER", Method: correlation.AutomatedDeviceEnrollment, Authenticated: true}
	if _, err := a.Inventory.Observe(ctx, "", o); err != nil {
		t.Fatal(err)
	}
	base := a.Inventory.Backend
	for _, prefix := range []string{"resource/", "history/"} {
		a.Inventory.Backend = inventoryBoundaryFault{base, prefix}
		if _, err := a.inventoryReportIdentity(ctx, e, "", false); !errors.Is(err, errInventoryStorage) {
			t.Fatal("source outage supplied a serial", prefix, err)
		}
	}
	a.Inventory.Backend = base
	// Both are retained authenticated originals, but their serial assertions
	// disagree. Neither may silently become a fallback for omitted report fields.
	o, err = inventory.ResourceObservation(inventory.EnrollmentReference("mdm.DeviceInformation", id), json.RawMessage(`{"QueryResponses":{"SerialNumber":"OTHER"}}`), at.Add(time.Second), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	o.Native = &inventory.NativeIdentity{Serial: "OTHER", Method: correlation.AutomatedDeviceEnrollment, Authenticated: true}
	if _, err := a.Inventory.Observe(ctx, "", o); err != nil {
		t.Fatal(err)
	}
	identity, err := a.inventoryReportIdentity(ctx, e, "", false)
	if err != nil || identity == nil || identity.Serial != "" {
		t.Fatal("contradictory originals established a serial binding", identity, err)
	}
}

// TestInventoryResolverRechecksStoredEvidence deliberately damages retained
// records after linking. A checked relationship never authenticates a corrupt
// Apple resource or authorizes unrelated source paths.
func TestInventoryResolverRechecksStoredEvidence(t *testing.T) {
	ctx := t.Context()
	at := time.Now().UTC()
	repo, err := inventory.New(inventory.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Inventory: repo}
	appleRef := source.Reference{Kind: "axm.device", AccountID: "a", ResourceID: "apple"}
	nativeRef := source.Reference{Kind: "mdm.identity", ResourceID: "device:native"}
	observe := func(ref source.Reference, raw string, n *inventory.NativeIdentity) inventory.ResourceRecord {
		t.Helper()
		o, err := inventory.ResourceObservation(ref, json.RawMessage(raw), at, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		o.Native = n
		d, err := repo.Observe(ctx, "", o)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cloud := observe(appleRef, `{"id":"apple","type":"orgDevices","attributes":{"serialNumber":"SER"}}`, nil)
	native := observe(nativeRef, `{"SerialNumber":"SER"}`, &inventory.NativeIdentity{Serial: "SER", Method: correlation.AutomatedDeviceEnrollment, Authenticated: true})
	if _, err := repo.Link(ctx, appleRef, nativeRef, at); err != nil {
		t.Fatal(err)
	}
	mapping := []inventoryMapping{{Name: "serial", Inputs: []struct {
		ResourceID string `json:"resource_id"`
		Path       string `json:"path"`
	}{{native.ID, "SerialNumber"}}}}
	// A persisted relationship involving this source in the native role is not
	// permission to resolve its unrelated Apple peer.
	if err := repo.Backend.Update(ctx, func(tx inventory.Tx) error {
		b, err := json.Marshal(inventory.SerialLink{Apple: source.Reference{Kind: "axm.device", AccountID: "other", ResourceID: "other"}, Native: cloud.Source, Serial: "SER", LinkedAt: at})
		if err != nil {
			return err
		}
		return tx.Put(ctx, "link/alien", b)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.resolveInventory(ctx, cloud.ID, mapping); err != nil {
		t.Fatal("unrelated reverse relationship affected checked native evidence", err)
	}

	// A malformed persisted Apple original must be revalidated even if metadata
	// and the old checked link still claim that collection succeeded.
	cloud.Snapshot.Raw = json.RawMessage(`{}`)
	if err := repo.Backend.Update(ctx, func(tx inventory.Tx) error {
		b, err := json.Marshal(cloud)
		if err != nil {
			return err
		}
		return tx.Put(ctx, "resource/"+cloud.ID, b)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.resolveInventory(ctx, cloud.ID, mapping); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal("corrupt Apple original resolved through old link", err)
	}
}

// TestInventoryNativeReceiptFailuresPreserveOriginalState checks fresh adapters
// cannot accept an unrepresentable plist or turn a lost serial-proof read into
// authenticated omission fallback.
func TestInventoryNativeReceiptFailuresPreserveOriginalState(t *testing.T) {
	a, id := nativeInventoryApp(t)
	ctx := t.Context()
	at := time.Now().UTC()
	cert := &x509.Certificate{Raw: []byte("active")}
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e.CertHash = cms.Fingerprint(cert)
	if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	setupJSON(t, a.protocol, "issued-identity:"+e.CertHash, identityEvidence{EnrollmentMethod: correlation.AutomatedDeviceEnrollment, UDID: id.ID, Serial: "SER"})
	// Plists support nonfinite reals, but retained JSON cannot represent one.
	// Reject the receipt instead of silently omitting the unknown source value.
	raw := []byte(`<?xml version="1.0"?><plist><dict><key>MessageType</key><string>Authenticate</string><key>FutureValue</key><real>NaN</real></dict></plist>`)
	if err := a.observeInventoryAuthenticate(ctx, &mdm.Request{ID: id, Certificate: cert}, &mdm.Checkin{Raw: raw}, at); err == nil {
		t.Fatal("unrepresentable Authenticate became successful evidence")
	}
	if err := a.projectInventoryCommand(ctx, id, "DeviceInformation", raw, at); err == nil {
		t.Fatal("unrepresentable native command became successful evidence")
	}
	proof, err := inventory.ResourceObservation(inventory.EnrollmentReference("mdm.identity", id), json.RawMessage(`{"SerialNumber":"SER"}`), at, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	proof.Native = &inventory.NativeIdentity{Serial: "SER", Method: correlation.AutomatedDeviceEnrollment, Authenticated: true}
	if _, err := a.Inventory.Observe(ctx, "", proof); err != nil {
		t.Fatal(err)
	}
	backing := a.Inventory.Backend
	a.Inventory.Backend = inventoryBoundaryFault{backing, "history/"}
	missingSerial, err := plist.Marshal(map[string]any{"QueryResponses": map[string]any{"OSVersion": "27.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.projectInventoryCommand(ctx, id, "DeviceInformation", missingSerial, at); !errors.Is(err, errInventoryStorage) {
		t.Fatal("command omitted serial-proof outage", err)
	}
	status := ddm.StatusUpdate{Raw: json.RawMessage(`{"StatusItems":{"future":false}}`), ReceivedAt: at, Values: []ddm.StatusValue{{Path: "future", Value: json.RawMessage(`false`)}}}
	if err := a.retainInventoryStatus(ctx, e, status); !errors.Is(err, errInventoryStorage) {
		t.Fatal("status omitted serial-proof outage", err)
	}
	a.Inventory.Backend = backing
	if err := a.retainInventoryStatus(ctx, e, status); err != nil {
		t.Fatal(err)
	}
	record, err := a.Inventory.SourceResource(ctx, inventory.EnrollmentReference("ddm.status", id))
	if err != nil || record.Snapshot.FieldTimes["future"] != at || string(record.Snapshot.Fields["future"]) != "false" {
		t.Fatal("missing per-item time lost receipt time or meaningful false", record, err)
	}
	// Backfill must propagate a retained-identity read failure instead of replacing
	// an unavailable original with merged persistent device fields.
	a.Inventory.Backend = inventoryBoundaryFault{backing, "resource/" + idResourceKey(id)}
	if err := a.projectInventoryEnrollment(ctx, id, true); !errors.Is(err, errInventoryStorage) {
		t.Fatal("missing identity read was replaced by merged state", err)
	}
	a.Inventory.Backend = backing
}

// idResourceKey identifies only the source document whose failure is injected.
func idResourceKey(id mdm.EnrollmentID) string {
	return inventory.EnrollmentReference("mdm.identity", id).Key()
}
