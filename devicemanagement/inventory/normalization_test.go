package inventory

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

// TestIdentifierNormalization preserves source evidence while omitting unusable identifiers from queries and exports.
func TestIdentifierNormalization(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"scalar", `"valid"`, `["valid"]`},
		{"mixed", `["", "one", " ", null, "two"]`, `["one","two"]`},
		{"empty", `""`, ""},
		{"whitespace", `"  "`, ""},
		{"empty_array", `[]`, ""},
		{"empty_members", `["", null, " "]`, ""},
		{"null", `null`, ""},
		{"invalid_type", `123`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := repository(t)
			now := time.Now().UTC()
			raw := `{"attributes":{"imei":` + tc.value + `,"meid":` + tc.value + `,"ethernetMacAddress":` + tc.value + `}}`
			d := observe(t, r, "SER", "axm.device", "account", "device", raw, now)
			for _, field := range []string{"imei", "meid", "ethernet_mac_addresses"} {
				if got := string(d.Fields[field].Value); got != tc.want {
					t.Fatalf("%s = %s, want %s", field, got, tc.want)
				}
				page, err := r.Devices(t.Context(), DeviceQuery{Conditions: []Condition{{Field: field, Operator: "exists"}}})
				if err != nil || (len(page.Items) == 1) != (tc.want != "") {
					t.Fatalf("%s existence: %+v %v", field, page, err)
				}
			}
			ref := SourceReference{Kind: "axm.device", AccountID: "account", ResourceID: "device"}
			if string(d.Sources[ref.Key()].Raw) != raw {
				t.Fatal("modified original Apple evidence")
			}
			if string(d.Fields["axm.device.attributes.imei"].Value) != tc.value {
				t.Fatal("lost raw identifier field")
			}
			var buf bytes.Buffer
			if err := r.Export(t.Context(), &buf, DeviceQuery{}, "csv", []string{"id", "imei"}, false); err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(&buf).ReadAll()
			if err != nil || len(rows) != 2 || rows[1][0] != d.ID || rows[1][1] != tc.want {
				t.Fatalf("CSV: %v %v", rows, err)
			}
			observe(t, r, "SER", "axm.device", "account", "device", `{"attributes":{"imei":""}}`, now.Add(time.Minute))
			page, err := r.Devices(t.Context(), DeviceQuery{Conditions: []Condition{{Field: "imei", Operator: "contains", Value: json.RawMessage(`"valid"`)}}})
			if err != nil || len(page.Items) != 0 {
				t.Fatal("stale identifier index", page, err)
			}
		})
	}
	for _, value := range []string{`""`, `" "`, `null`, `"address"`} {
		r := repository(t)
		d := observe(t, r, "SER", "axm.device", "a", "d", `{"attributes":{"wifiMacAddress":`+value+`,"bluetoothMacAddress":`+value+`,"eid":`+value+`}}`, time.Now())
		for _, field := range []string{"wifi_mac_address", "bluetooth_mac_address", "eid"} {
			_, present := d.Fields[field]
			if present != (value == `"address"`) {
				t.Fatalf("%s: unexpected presence for %s", field, value)
			}
		}
	}
}

// TestDDMNormalizedEvidence retains per-item serial and FileVault provenance across partial reports.
func TestDDMNormalizedEvidence(t *testing.T) {
	r := repository(t)
	ctx := t.Context()
	now := time.Now().UTC()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "mac"}
	cloud := observe(t, r, "SERIAL", "axm.device", "account", "device", `{"attributes":{"serialNumber":"serial"}}`, now)
	serial := cloud.Fields["serial_number"]
	if String(serial.Value) != "SERIAL" || serial.Source.Kind != "axm.device" || !serial.ObservedAt.Equal(now) {
		t.Fatal("cloud serial provenance lost", serial)
	}
	ref := EnrollmentReference("ddm.status", id)
	status := ddm.StatusUpdate{ReceivedAt: now.Add(time.Minute), Raw: []byte(`{}`)}
	status.Values = append(status.Values, ddm.StatusValue{Path: "diskmanagement.filevault.enabled", Value: json.RawMessage(`false`), LastSeen: now}, ddm.StatusValue{Path: "device.identifier.serial-number", Value: json.RawMessage(`"serial"`), LastSeen: now})
	if err := r.ObserveStatus(ctx, "SERIAL", id, status); err != nil {
		t.Fatal(err)
	}
	d, err := r.Device(ctx, cloud.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"serial_number": `"SERIAL"`, "filevault_enabled": `false`} {
		f := d.Fields[name]
		if string(f.Value) != want || f.Source != ref || !f.ObservedAt.Equal(now) {
			t.Fatalf("%s: %+v", name, f)
		}
	}
	page, err := r.Devices(ctx, DeviceQuery{Conditions: []Condition{{Field: "filevault_enabled", Operator: "eq", Value: json.RawMessage(`false`)}}})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("FileVault index", page, err)
	}
	report, err := r.Report(ctx, DeviceQuery{})
	if err != nil || report.Facets["filevault_enabled"]["false"] != 1 {
		t.Fatal("FileVault report", report, err)
	}
	var buf bytes.Buffer
	if err := r.Export(ctx, &buf, DeviceQuery{}, "csv", []string{"serial_number", "filevault_enabled"}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "SERIAL,false") {
		t.Fatal(buf.String())
	}
	observe(t, r, "SERIAL", "mdm.SecurityInfo", "", "device:mac", `{"SecurityInfo":{"FDE_Enabled":true}}`, now.Add(2*time.Minute))
	status.ReceivedAt = now.Add(3 * time.Minute)
	status.Values = nil
	if err := r.ObserveStatus(ctx, "SERIAL", id, status); err != nil {
		t.Fatal(err)
	}
	d, err = r.Device(ctx, cloud.ID)
	if err != nil || string(d.Fields["filevault_enabled"].Value) != "true" || d.Fields["filevault_enabled"].Source.Kind != "mdm.SecurityInfo" || !d.Fields["serial_number"].ObservedAt.Equal(now) {
		t.Fatal("partial DDM report refreshed unchanged evidence", d.Fields, err)
	}
	observe(t, r, "SERIAL", "mdm.DeviceInformation", "", "device:mac", `{"QueryResponses":{"SerialNumber":"serial"}}`, now.Add(4*time.Minute))
	d, err = r.Device(ctx, cloud.ID)
	if err != nil || d.Fields["serial_number"].Source.Kind != "mdm.DeviceInformation" || !d.Fields["serial_number"].ObservedAt.Equal(now.Add(4*time.Minute)) {
		t.Fatal("fresh native serial lost provenance", d.Fields, err)
	}
}

// TestLegacyZeroEnrollmentDates excludes old zero-date sentinels while retaining raw evidence and real dates.
func TestLegacyZeroEnrollmentDates(t *testing.T) {
	r := repository(t)
	now := time.Now().UTC()
	raw := `{"managed":true,"enrolled_at":"0001-01-01T00:00:00Z","last_seen":"0001-01-01T00:00:00Z","disabled_at":"0001-01-01T00:00:00Z"}`
	d := observe(t, r, "SER", "enrollment", "", "device:mac", raw, now)
	for _, name := range []string{"enrolled_at", "last_seen", "disabled_at"} {
		if _, ok := d.Fields[name]; ok {
			t.Fatal("zero date exposed", name)
		}
		if _, ok := d.Fields["enrollment."+name]; !ok {
			t.Fatal("original field removed", name)
		}
	}
	var buf bytes.Buffer
	if err := r.Export(t.Context(), &buf, DeviceQuery{}, "csv", []string{"disabled_at"}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "0001") {
		t.Fatal("zero date exported")
	}
	d = observe(t, r, "SER", "enrollment", "", "device:mac", `{"managed":false,"disabled_at":"2026-09-22T03:00:00Z"}`, now.Add(time.Minute))
	if String(d.Fields["disabled_at"].Value) != "2026-09-22T03:00:00Z" {
		t.Fatal("actual disable date lost")
	}
}
