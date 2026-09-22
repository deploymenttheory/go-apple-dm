package app

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/ddmtest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
)

// nativeInventoryApp builds a worker-free reference server with a native enrollment.
func nativeInventoryApp(t *testing.T) (*App, mdm.EnrollmentID) {
	t.Helper()
	a, err := Build(t.Context(), Config{Storage: "inmem", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "native"}
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id, Enabled: true, Device: storage.DeviceInfo{SerialNumber: "SER", ProductName: "Mac16,1", OSVersion: "27.0"}, EnrolledAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	return a, id
}

// TestInventoryNativeCollection verifies queued collection, backfill, native errors and channel isolation.
func TestInventoryNativeCollection(t *testing.T) {
	a, id := nativeInventoryApp(t)
	ctx := t.Context()
	now := time.Now().UTC()
	if err := a.observeInventoryEnrollment(ctx, id); err != nil {
		t.Fatal(err)
	}
	if n, err := a.collectNative(ctx, id, false); err != nil || n == 0 {
		t.Fatal(n, err)
	}
	if n, err := a.collectNative(ctx, id, false); err != nil || n != 0 {
		t.Fatal("fresh commands repeated", n, err)
	}
	record, err := a.Inventory.SourceDevice(ctx, inventory.EnrollmentReference("enrollment", id))
	if err != nil {
		t.Fatal(err)
	}
	mux := inventoryTestMux(a)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(ctx, "POST", "/devices/"+record.ID+"/collect", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	command, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"OSVersion"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.Enqueue(ctx, []mdm.EnrollmentID{id}, command, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, err := plist.Marshal(map[string]any{"Status": "Acknowledged", "UDID": id.ID, "CommandUUID": command.UUID, "QueryResponses": map[string]any{"OSVersion": "27.1"}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := mdm.DecodeResponse(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store.StoreResult(ctx, id, response, now); err != nil {
		t.Fatal(err)
	}
	for _, status := range []mdm.Status{mdm.StatusError, mdm.StatusCommandFormatError, mdm.StatusNotNow, mdm.StatusIdle} {
		copy := *response
		copy.Status = status
		if err := a.observeInventoryResult(ctx, id, &copy, now); err != nil {
			t.Fatal(err)
		}
	}
	// Persist DDM status without the application observer to model an upgraded database.
	engine, err := ddm.New(ddm.Config{Store: a.Engine.Store()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Status(ctx, id, []byte(`{"StatusItems":{"device":{"operating-system":{"version":"27.2"}}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNativeInventory(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := a.Inventory.Device(ctx, record.ID)
	if err != nil || inventory.String(d.Fields["os_version"].Value) != "27.2" {
		t.Fatal(d, err)
	}
	if err := a.refreshNativeInventory(ctx); err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{NotBefore: now, NotAfter: now.Add(90 * 24 * time.Hour)}
	if err := a.observeInventoryCertificate(ctx, id, cert, now); err != nil {
		t.Fatal(err)
	}
	user := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "user", ParentID: id.ID}
	if err := a.observeInventoryEnrollment(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := a.observeInventoryResult(ctx, user, response, now); err != nil {
		t.Fatal(err)
	}
	if err := a.observeInventoryCertificate(ctx, user, cert, now); err != nil {
		t.Fatal(err)
	}
	if err := a.projectInventoryCommand(ctx, id, "RestartDevice", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := a.projectInventoryCommand(ctx, id, "DeviceInformation", []byte("bad plist"), now); err == nil {
		t.Fatal("invalid plist accepted")
	}
	if err := a.observeInventoryCertificate(ctx, id, nil, now); err != nil {
		t.Fatal(err)
	}
	missing := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "missing"}
	if err := a.observeInventoryEnrollment(ctx, missing); err != nil {
		t.Fatal(err)
	}
	if err := a.observeInventoryCertificate(ctx, missing, cert, now); err != nil {
		t.Fatal(err)
	}
	if err := a.observeInventoryStatus(ctx, missing, ddm.StatusUpdate{FullReport: true, Raw: []byte(`{}`), ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Disable(ctx, id, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.collectNative(ctx, id, true); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal(err)
	}
}

// TestInventoryNativeFailurePropagation verifies storage outages cannot appear as successful collection.
func TestInventoryNativeFailurePropagation(t *testing.T) {
	a, id := nativeInventoryApp(t)
	ctx := t.Context()
	now := time.Now().UTC()
	base := a.Store
	cert := &x509.Certificate{NotAfter: now}
	a.Store = &storagetest.Failing{Store: base, Fail: map[string]error{"Get": errInventoryStorage}}
	for _, op := range []func() error{
		func() error { return a.observeInventoryEnrollment(ctx, id) }, func() error { return a.observeInventoryCertificate(ctx, id, cert, now) },
		func() error { return a.observeInventoryStatus(ctx, id, ddm.StatusUpdate{}) }, func() error { return a.projectInventoryCommand(ctx, id, "DeviceInformation", nil, now) },
		func() error { _, e := a.collectNative(ctx, id, true); return e },
	} {
		if err := op(); !errors.Is(err, errInventoryStorage) {
			t.Fatal(err)
		}
	}
	a.Store = &storagetest.Failing{Store: base, Fail: map[string]error{"Commands": errInventoryStorage}}
	if err := a.observeInventoryResult(ctx, id, &mdm.Response{}, now); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	if _, err := a.collectNative(ctx, id, false); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	if err := a.refreshNativeInventory(ctx); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	a.Store = &storagetest.Failing{Store: base, Fail: map[string]error{"List": errInventoryStorage}}
	if err := a.refreshNativeInventory(ctx); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	a.Store = base
	backend := a.Inventory.Backend
	a.Inventory.Backend = inventoryFailureBackend{Backend: backend, updates: true}
	if err := a.observeInventoryEnrollment(ctx, id); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	if err := a.refreshNativeInventory(ctx); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	a.Inventory.Backend = inventoryFailureBackend{Backend: backend, reads: true}
	enrollment, err := base.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.backfillInventoryStatus(ctx, *enrollment); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	a.Inventory.Backend = backend
	engine, err := ddm.New(ddm.Config{Store: &ddmtest.Failing{Store: a.Engine.Store(), Fail: map[string]error{"StatusValues": errInventoryStorage}}})
	if err != nil {
		t.Fatal(err)
	}
	a.Engine = engine
	if err := a.backfillInventoryStatus(ctx, *enrollment); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	if err := a.refreshNativeInventory(ctx); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	cert.NotAfter = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := a.observeInventoryCertificate(ctx, id, cert, now); err == nil {
		t.Fatal("invalid date accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := a.runNativeInventory(cancelled); err != nil {
		t.Fatal(err)
	}
}

// TestInventoryNativeBootstrapAndPagination covers unknown platforms and multi-page enrollment backfills.
func TestInventoryNativeBootstrapAndPagination(t *testing.T) {
	a, id := nativeInventoryApp(t)
	ctx := t.Context()
	for i := range 102 {
		e := storage.Enrollment{ID: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: fmt.Sprintf("seed-%03d", i)}}
		if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: e}); err != nil {
			t.Fatal(err)
		}
	}
	user := storage.Enrollment{ID: mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "user", ParentID: id.ID}}
	if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: user}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNativeInventory(ctx); err != nil {
		t.Fatal(err)
	}
	for _, channel := range []mdm.Channel{mdm.ChannelDevice, mdm.ChannelUserEnrollmentDevice} {
		e := storage.Enrollment{ID: mdm.EnrollmentID{Channel: channel, ID: "bootstrap-" + channel.String()}, Enabled: true}
		if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: e}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.collectNative(ctx, e.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	// A malformed stored enrollment snapshot must not trigger a command for another identity.
	d, err := a.Inventory.SourceDevice(ctx, inventory.EnrollmentReference("enrollment", id))
	if err != nil {
		t.Fatal(err)
	}
	for k, o := range d.Sources {
		if o.Source.Kind == "enrollment" {
			o.Raw = json.RawMessage(`[]`)
			d.Sources[k] = o
		}
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Inventory.Backend.Update(ctx, func(tx inventory.Tx) error { return tx.Put(ctx, "device/"+d.ID, b) }); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	inventoryTestMux(a).ServeHTTP(w, httptest.NewRequestWithContext(ctx, "POST", "/devices/"+d.ID+"/collect", nil))
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestInventoryNativeConfigurationFailures checks invalid persisted dates and outbound trust failures.
func TestInventoryNativeConfigurationFailures(t *testing.T) {
	a, id := nativeInventoryApp(t)
	ctx := t.Context()
	a.cfg.AxM.RootCAFile = "/missing-inventory-ca.pem"
	if _, err := a.inventoryClient(ctx, inventory.Account{}, nil); err == nil {
		t.Fatal("missing CA accepted")
	}
	if err := a.Inventory.Backend.Update(ctx, func(tx inventory.Tx) error {
		return tx.Put(ctx, "account/disabled", json.RawMessage(`{"id":"disabled","enabled":false}`))
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.initialInventorySync(ctx, inventory.Account{ID: "disabled"}); err != nil {
		t.Fatal(err)
	}
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e.EnrolledAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: *e}); err != nil {
		t.Fatal(err)
	}
	if err := a.observeInventoryEnrollment(ctx, id); err == nil {
		t.Fatal("invalid enrollment timestamp accepted")
	}
	a.cfg.AxM = AxMConfig{ClientID: "BUSINESSAPI.bad", KeyID: "key", KeyPEM: []byte("invalid key")}
	if err := a.openInventory(ctx); err == nil {
		t.Fatal("invalid legacy key imported")
	}
}

// TestInventoryStatusBackfillPagination retains all effective values across storage pages.
func TestInventoryStatusBackfillPagination(t *testing.T) {
	a, id := nativeInventoryApp(t)
	ctx := t.Context()
	now := time.Now().UTC()
	values := make([]ddm.StatusValue, 251)
	for i := range values {
		values[i] = ddm.StatusValue{Path: fmt.Sprintf("future.item%03d", i), Value: []byte(`false`), FirstSeen: now, LastSeen: now}
	}
	if err := a.Engine.Store().Update(ctx, func(tx ddm.Tx) error {
		_, e := tx.PutStatus(ctx, id, ddm.StatusUpdate{FullReport: true, Values: values, Raw: []byte(`{}`), ReceivedAt: now})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	e, err := a.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.backfillInventoryStatus(ctx, *e); err != nil {
		t.Fatal(err)
	}
	d, err := a.Inventory.SourceDevice(ctx, inventory.EnrollmentReference("ddm.status", id))
	if err != nil {
		t.Fatal(err)
	}
	if string(d.Fields["ddm.status.future.item250"].Value) != "false" {
		t.Fatal("last status page omitted")
	}
}

// TestInventoryEnrollmentDates omits unset source dates and preserves real lifecycle timestamps.
func TestInventoryEnrollmentDates(t *testing.T) {
	a, id := nativeInventoryApp(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	for i, disabled := range []bool{false, true, false} {
		e, err := a.Store.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		e.Enabled = !disabled
		e.EnrolledAt = time.Time{}
		e.LastSeenAt = now.Add(time.Duration(i) * time.Minute)
		e.DisabledAt = time.Time{}
		if disabled {
			e.DisabledAt = e.LastSeenAt
		}
		if err := a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: *e}); err != nil {
			t.Fatal(err)
		}
		if err := a.observeInventoryEnrollment(ctx, id); err != nil {
			t.Fatal(err)
		}
		ref := inventory.EnrollmentReference("enrollment", id)
		d, err := a.Inventory.SourceDevice(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(d.Sources[ref.Key()].Raw, &raw); err != nil {
			t.Fatal(err)
		}
		if _, exists := raw["enrolled_at"]; exists {
			t.Fatal("zero enrollment date retained")
		}
		if _, exists := raw["disabled_at"]; exists != disabled {
			t.Fatal("raw disable date presence", disabled, raw)
		}
		if _, exists := d.Fields["disabled_at"]; exists != disabled {
			t.Fatal("normalized disable date presence", disabled, d.Fields)
		}
		if disabled && inventory.String(d.Fields["disabled_at"].Value) != e.DisabledAt.Format(time.RFC3339) {
			t.Fatal("actual disable timestamp changed")
		}
		if inventory.String(d.Fields["last_seen"].Value) != e.LastSeenAt.Format(time.RFC3339) {
			t.Fatal("last seen timestamp changed")
		}
	}
}
