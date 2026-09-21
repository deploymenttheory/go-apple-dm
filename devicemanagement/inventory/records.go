package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Repository reconciles observations and exposes persistent device records.
type Repository struct{ Backend Backend }

// New validates the persistence dependency.
func New(b Backend) (*Repository, error) {
	if b == nil {
		return nil, ErrInvalid
	}
	return &Repository{Backend: b}, nil
}

// Device loads a record by its local identity.
func (r *Repository) Device(ctx context.Context, id string) (DeviceRecord, error) {
	return loadRecord(ctx, r.Backend, id)
}

// SourceDevice resolves a previously observed external identity.
func (r *Repository) SourceDevice(ctx context.Context, ref SourceReference) (DeviceRecord, error) {
	id, err := get[string](ctx, r.Backend, "source/"+ref.Key())
	if err != nil {
		return DeviceRecord{}, err
	}
	return r.Device(ctx, id)
}

// Observe atomically reconciles a successful source snapshot and its indexes.
func (r *Repository) Observe(ctx context.Context, serial string, o Observation) (DeviceRecord, error) {
	var out DeviceRecord
	err := r.Backend.Update(ctx, func(tx Tx) error { var err error; out, err = r.ObserveTx(ctx, tx, serial, o); return err })
	return out, err
}

// ObserveTx allows a page checkpoint and its observations to share one transaction.
func (r *Repository) ObserveTx(ctx context.Context, tx Tx, serial string, o Observation) (DeviceRecord, error) {
	if o.Source.Kind == "" || o.Source.ResourceID == "" || o.ObservedAt.IsZero() || !json.Valid(o.Raw) {
		return DeviceRecord{}, ErrInvalid
	}
	serial = CanonicalSerial(serial)
	sourceKey := "source/" + o.Source.Key()
	id, err := get[string](ctx, tx, sourceKey)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return DeviceRecord{}, err
	}
	if id == "" && (strings.HasPrefix(o.Source.Kind, "mdm.") || strings.HasPrefix(o.Source.Kind, "ddm.")) {
		anchor := SourceReference{Kind: "enrollment", ResourceID: o.Source.ResourceID}
		id, err = get[string](ctx, tx, "source/"+anchor.Key())
		if err != nil && !errors.Is(err, ErrNotFound) {
			return DeviceRecord{}, err
		}
	}
	sourceMatched := id != ""
	ambiguous := false
	if id == "" && serial != "" {
		_, conflictErr := tx.Get(ctx, "serialconflict/"+digest(serial))
		ambiguous = conflictErr == nil
		if conflictErr != nil && !errors.Is(conflictErr, ErrNotFound) {
			return DeviceRecord{}, conflictErr
		}
		id, err = get[string](ctx, tx, "serial/"+digest(serial))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return DeviceRecord{}, err
		}
	}
	var record DeviceRecord
	if ambiguous && !sourceMatched {
		id = ""
	}
	if id != "" {
		record, err = loadRecord(ctx, tx, id)
		if err != nil {
			return record, err
		}
	} else {
		record = DeviceRecord{ID: ID(), SerialNumber: serial, CreatedAt: o.ObservedAt, Sources: map[string]Observation{}, Fields: map[string]FieldValue{}}
	}

	if !sourceMatched && id != "" {
		for _, existing := range record.Sources {
			if existing.Source.Kind == o.Source.Kind && existing.Source.AccountID == o.Source.AccountID && existing.Source.ResourceID != o.Source.ResourceID && !existing.Absent && !existing.Disconnected && o.Source.Kind == "axm.device" {
				record.Conflicts = appendUnique(record.Conflicts, "ambiguous serial reported by multiple resources in one Apple account")
				if e := r.saveRecord(ctx, tx, record); e != nil {
					return record, e
				}
				if e := put(ctx, tx, "serialconflict/"+digest(serial), true); e != nil {
					return record, e
				}
				ambiguous = true
				record = DeviceRecord{ID: ID(), SerialNumber: serial, CreatedAt: o.ObservedAt, Sources: map[string]Observation{}, Fields: map[string]FieldValue{}}
				break
			}
		}
	}
	if ambiguous {
		record.Conflicts = appendUnique(record.Conflicts, "ambiguous serial; source identity retained without automatic merge")
	}
	if serial != "" && record.SerialNumber != "" && record.SerialNumber != serial {
		msg := "serial mismatch for " + o.Source.Kind + " resource " + o.Source.Key()
		record.Conflicts = appendUnique(record.Conflicts, msg)
		if err := put(ctx, tx, "device/"+record.ID, record); err != nil {
			return record, err
		}
		return record, nil
	}
	if record.SerialNumber == "" && serial != "" {
		other, e := get[string](ctx, tx, "serial/"+digest(serial))
		if e != nil && !errors.Is(e, ErrNotFound) {
			return record, e
		}
		if other != "" && other != record.ID {
			target, e := loadRecord(ctx, tx, other)
			if e != nil {
				return record, e
			}
			_, conflict := tx.Get(ctx, "serialconflict/"+digest(serial))
			if conflict != nil && !errors.Is(conflict, ErrNotFound) {
				return record, conflict
			}
			if len(record.Conflicts) > 0 || len(target.Conflicts) > 0 || conflict == nil {
				record.Conflicts = appendUnique(record.Conflicts, "serial is already linked to device "+other)
			} else {
				// A provisional enrollment with no serial can safely enrich an unambiguous cloud record.
				previous := record.ID
				for k, source := range record.Sources {
					if old, ok := target.Sources[k]; !ok || source.ObservedAt.After(old.ObservedAt) {
						target.Sources[k] = source
					}
				}
				if record.CreatedAt.Before(target.CreatedAt) {
					target.CreatedAt = record.CreatedAt
				}
				indexes, e := get[[]string](ctx, tx, "indexes/"+previous)
				if e != nil && !errors.Is(e, ErrNotFound) {
					return record, e
				}
				for _, key := range indexes {
					if e := tx.Delete(ctx, key); e != nil {
						return record, e
					}
				}
				for _, key := range []string{"device/" + previous, "indexes/" + previous} {
					if e := tx.Delete(ctx, key); e != nil {
						return record, e
					}
				}
				if e := put(ctx, tx, "alias/"+previous, target.ID); e != nil {
					return record, e
				}
				for _, source := range target.Sources {
					if e := put(ctx, tx, "source/"+source.Source.Key(), target.ID); e != nil {
						return record, e
					}
				}
				record = target
			}
		} else {
			record.SerialNumber = serial
		}
	}
	if old, ok := record.Sources[o.Source.Key()]; ok && (old.ObservedAt.After(o.ObservedAt) || (old.ObservedAt.Equal(o.ObservedAt) && old.Error == "" && old.Generation == o.Generation && old.Absent == o.Absent && old.Disconnected == o.Disconnected)) {
		return record, nil
	}
	if o.AttemptedAt.IsZero() {
		o.AttemptedAt = o.ObservedAt
	}
	if o.Fields == nil {
		o.Fields = Flatten(o.Raw)
	}
	record.Sources[o.Source.Key()] = o
	record.UpdatedAt = o.ObservedAt
	rebuild(&record)
	if err := r.saveRecord(ctx, tx, record); err != nil {
		return record, err
	}
	if err := put(ctx, tx, sourceKey, record.ID); err != nil {
		return record, err
	}
	if record.SerialNumber != "" && !ambiguous {
		if err := put(ctx, tx, "serial/"+digest(record.SerialNumber), record.ID); err != nil {
			return record, err
		}
	}
	return record, nil
}

// Attempt records failure metadata without replacing successful evidence.
func (r *Repository) Attempt(ctx context.Context, ref SourceReference, at time.Time, code string) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		id, err := get[string](ctx, tx, "source/"+ref.Key())
		if errors.Is(err, ErrNotFound) && strings.HasPrefix(ref.Kind, "mdm.") {
			anchor := SourceReference{Kind: "enrollment", ResourceID: ref.ResourceID}
			id, err = get[string](ctx, tx, "source/"+anchor.Key())
		}
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		d, err := get[DeviceRecord](ctx, tx, "device/"+id)
		if err != nil {
			return err
		}
		o := d.Sources[ref.Key()]
		o.Source = ref
		o.AttemptedAt = at
		o.Error = code
		d.Sources[ref.Key()] = o
		if e := put(ctx, tx, "source/"+ref.Key(), id); e != nil {
			return e
		}
		return put(ctx, tx, "device/"+id, d)
	})
}

// saveRecord replaces the query indexes and record together.
func (r *Repository) saveRecord(ctx context.Context, tx Tx, d DeviceRecord) error {
	old, err := get[[]string](ctx, tx, "indexes/"+d.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	for _, key := range old {
		if err := tx.Delete(ctx, key); err != nil {
			return err
		}
	}
	keys := map[string]bool{}
	for path, f := range d.Fields {
		values := []json.RawMessage{f.Value}
		var a []json.RawMessage
		if json.Unmarshal(f.Value, &a) == nil {
			values = append(values, a...)
		}
		for _, v := range values {
			key := indexPrefix(path, v) + d.ID
			keys[key] = true
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		if err := put(ctx, tx, key, d.ID); err != nil {
			return err
		}
	}
	if err := put(ctx, tx, "indexes/"+d.ID, ordered); err != nil {
		return err
	}
	return put(ctx, tx, "device/"+d.ID, d)
}

// indexPrefix indexes exact field values, including individual array members.
func indexPrefix(path string, v json.RawMessage) string {
	if canonical, err := canonicalJSON(v); err == nil {
		v = canonical
	}
	return "field/" + digest(path+"\x00"+string(v)) + "/"
}

// Flatten retains containers and leaves as dotted paths without coercing values.
func Flatten(raw json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	var visit func(string, json.RawMessage)
	visit = func(path string, value json.RawMessage) {
		if path != "" {
			out[path] = append(json.RawMessage(nil), value...)
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) != nil {
			return
		}
		for k, v := range object {
			escaped := strings.ReplaceAll(strings.ReplaceAll(k, `\`, `\`), ".", `\.`)
			next := escaped
			if path != "" {
				next = path + "." + escaped
			}
			visit(next, v)
		}
	}
	visit("", raw)
	return out
}

// aliases maps Apple field names to stable inventory presentation names.
var aliases = map[string]string{
	"serialNumber": "serial_number", "SerialNumber": "serial_number", "UDID": "udid",
	"deviceModel": "model", "ModelName": "model", "deviceName": "device_name", "DeviceName": "device_name",
	"productFamily": "product_family", "productType": "product_type", "ProductName": "product_type",
	"deviceCapacity": "capacity", "DeviceCapacity": "capacity", "partNumber": "part_number", "color": "color",
	"orderNumber": "order_number", "orderDateTime": "order_date", "purchaseSourceId": "purchase_source_id", "purchaseSourceUid": "purchase_source_uid", "purchaseSourceType": "purchase_source_type",
	"addedToOrgDateTime": "added_to_org", "updatedDateTime": "apple_updated_at", "releasedFromOrgDateTime": "released_from_org",
	"wifiMacAddress": "wifi_mac_address", "WiFiMAC": "wifi_mac_address", "bluetoothMacAddress": "bluetooth_mac_address", "BluetoothMAC": "bluetooth_mac_address",
	"ethernetMacAddress": "ethernet_mac_addresses", "EthernetMAC": "ethernet_mac_addresses", "imei": "imei", "IMEI": "imei", "meid": "meid", "MEID": "meid", "eid": "eid",
	"isMdmMigrationCapable": "migration_capable", "mdmMigrationStatus": "migration_status", "mdmMigrationDeadlineDateTime": "migration_deadline",
	"osVersion": "os_version", "OSVersion": "os_version", "BuildVersion": "build_version", "IsAppleSilicon": "apple_silicon",
	"IsSupervised": "supervised", "isFileVaultEnabled": "filevault_enabled", "FDE_Enabled": "filevault_enabled", "isFirewallEnabled": "firewall_enabled",
	"lastCheckInDateTime": "last_seen", "storageTotalCapacity": "storage_total_bytes", "storageFreeCapacity": "storage_free_bytes",
}

// rebuild deterministically projects evidence without destroying source-specific values.
func rebuild(d *DeviceRecord) {
	d.Fields = map[string]FieldValue{}
	d.Coverage = nil
	keys := make([]string, 0, len(d.Sources))
	for k := range d.Sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		o := d.Sources[key]
		if o.Absent || o.Disconnected {
			continue
		}
		paths := make([]string, 0, len(o.Fields))
		for path := range o.Fields {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			v := o.Fields[path]
			f := FieldValue{Value: v, Source: o.Source, ObservedAt: o.ObservedAt}
			if at, ok := o.FieldTimes[path]; ok {
				f.ObservedAt = at
			}
			selectField(d, o.Source.Kind+"."+path, f)
			leaf := path[strings.LastIndex(path, ".")+1:]
			name := ""
			switch o.Source.Kind {
			case "axm.device", "axm.detail", "axm.mdm", "axm.mdm.detail":
				if path == "attributes."+leaf {
					name = aliases[leaf]
				}
			case "mdm.identity":
				if path == leaf {
					name = aliases[leaf]
				}
			case "mdm.DeviceInformation":
				if path == "QueryResponses."+leaf {
					name = aliases[leaf]
				}
			case "mdm.SecurityInfo":
				if path == "SecurityInfo."+leaf {
					name = aliases[leaf]
				}
			}
			if strings.HasPrefix(o.Source.Kind, "ddm") {
				name = ddmAlias(path)
			}
			if o.Source.Kind == "enrollment" || o.Source.Kind == "axm.assignment" || o.Source.Kind == "mdm.certificate" {
				name = path
			}
			if name != "" {
				if name == "imei" || name == "meid" || name == "ethernet_mac_addresses" {
					var scalar string
					if len(f.Value) > 0 && f.Value[0] == '"' && json.Unmarshal(f.Value, &scalar) == nil {
						f.Value, _ = json.Marshal([]string{scalar})
					}
				}
				selectField(d, name, f)
			}
		}
		if o.Source.Kind == "axm.coverage" {
			var resources []struct {
				ID         string                     `json:"id"`
				Attributes map[string]json.RawMessage `json:"attributes"`
			}
			if json.Unmarshal(o.Raw, &resources) == nil {
				agreements := []string{}
				for _, p := range resources {
					d.Coverage = append(d.Coverage, CoveragePlan{p.ID, o.Source.AccountID, p.Attributes})
					if agreement := String(p.Attributes["agreementNumber"]); agreement != "" {
						agreements = append(agreements, agreement)
					}
					raw, _ := json.Marshal(p.Attributes)
					for path, value := range Flatten(raw) {
						selectField(d, "axm.coverage."+p.ID+"."+path, FieldValue{Value: value, Source: o.Source, ObservedAt: o.ObservedAt})
					}
				}
				raw, _ := json.Marshal(agreements)
				selectField(d, "coverage_agreement_numbers", FieldValue{Value: raw, Source: o.Source, ObservedAt: o.ObservedAt})
			}
		}
	}
	var managed *FieldValue
	for _, o := range d.Sources {
		if o.Source.Kind != "enrollment" || o.Absent || o.Disconnected {
			continue
		}
		raw, ok := o.Fields["managed"]
		if !ok {
			continue
		}
		if managed == nil || string(raw) == "true" {
			value := FieldValue{Value: raw, Source: o.Source, ObservedAt: o.ObservedAt}
			managed = &value
		}
	}
	if managed != nil {
		d.Fields["managed"] = *managed
	}
	if d.SerialNumber != "" {
		raw, _ := json.Marshal(d.SerialNumber)
		d.Fields["serial_number"] = FieldValue{Value: raw}
	}
}

// selectField prefers newer evidence, with native source priority for exact ties.
func selectField(d *DeviceRecord, name string, f FieldValue) {
	old, ok := d.Fields[name]
	if !ok || f.ObservedAt.After(old.ObservedAt) || (f.ObservedAt.Equal(old.ObservedAt) && priority(f.Source.Kind) > priority(old.Source.Kind)) {
		d.Fields[name] = f
	}
}

// priority orders equally fresh native and cloud observations.
func priority(kind string) int {
	switch {
	case strings.HasPrefix(kind, "ddm"):
		return 4
	case strings.HasPrefix(kind, "mdm"):
		return 3
	case kind == "enrollment":
		return 2
	default:
		return 1
	}
}

// ddmAlias maps the operational status items that have cross-source counterparts.
func ddmAlias(path string) string {
	return map[string]string{"device.identifier.serial-number": "serial_number", "device.identifier.udid": "udid", "device.model.family": "product_family", "device.model.identifier": "product_type", "device.model.marketing-name": "model", "device.operating-system.version": "os_version", "device.operating-system.build-version": "build_version", "device.operating-system.family": "os_family"}[path]
}

// appendUnique avoids repeating a persistent conflict on every refresh.
func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}

// String reads a scalar string without coercing null or other types.
func String(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }

// Time reads an RFC3339 Apple timestamp.
func Time(raw json.RawMessage) (time.Time, bool) {
	t, e := time.Parse(time.RFC3339Nano, String(raw))
	return t, e == nil
}

// Coverage derives active coverage and the next finite expiry at a supplied clock time.
func (d DeviceRecord) CoverageAt(now time.Time) CoverageSummary {
	s := CoverageSummary{State: "unknown"}
	seen := false
	for _, o := range d.Sources {
		if o.Source.Kind == "axm.coverage" && !o.Absent && !o.Disconnected && !o.ObservedAt.IsZero() {
			seen = true
			s.Stale = s.Stale || o.Error != "" || (!o.ExpiresAt.IsZero() && !now.Before(o.ExpiresAt))
		}
	}
	if !seen {
		return s
	}
	s.State = "none"
	if len(d.Coverage) > 0 {
		s.State = "inactive"
	}
	unknown := false
	for _, p := range d.Coverage {
		a := p.Attributes
		status := String(a["status"])
		if status != "ACTIVE" && status != "INACTIVE" {
			unknown = true
			continue
		}
		if status != "ACTIVE" || string(a["isCanceled"]) == "true" {
			continue
		}
		if start, ok := Time(a["startDateTime"]); ok && now.Before(start) {
			continue
		}
		end, finite := Time(a["endDateTime"])
		if finite && !now.Before(end) {
			continue
		}
		s.State = "active"
		if finite && (s.NextExpiry == nil || end.Before(*s.NextExpiry)) {
			v := end
			s.NextExpiry = &v
		}
	}
	if unknown && s.State != "active" {
		s.State = "unknown"
	}
	return s
}

// ChangeSource updates lifecycle metadata without fabricating a new observation.
func (r *Repository) ChangeSource(ctx context.Context, ref SourceReference, absent, disconnected bool) error {
	return r.Backend.Update(ctx, func(tx Tx) error {
		id, e := get[string](ctx, tx, "source/"+ref.Key())
		if e != nil {
			return e
		}
		d, e := get[DeviceRecord](ctx, tx, "device/"+id)
		if e != nil {
			return e
		}
		o := d.Sources[ref.Key()]
		o.Absent = absent
		o.Disconnected = disconnected
		d.Sources[ref.Key()] = o
		rebuild(&d)
		return r.saveRecord(ctx, tx, d)
	})
}

// ResourceObservation constructs a lossless snapshot from a JSON resource.
func ResourceObservation(ref SourceReference, raw json.RawMessage, at time.Time, ttl time.Duration) (Observation, error) {
	if !json.Valid(raw) {
		return Observation{}, fmt.Errorf("%w: source JSON", ErrInvalid)
	}
	return Observation{Source: ref, Raw: append(json.RawMessage(nil), raw...), Fields: Flatten(raw), ObservedAt: at, AttemptedAt: at, ExpiresAt: at.Add(ttl)}, nil
}

// loadRecord follows aliases created when an enrollment learns its serial after discovery.
func loadRecord(ctx context.Context, reader Reader, id string) (DeviceRecord, error) {
	for range 16 {
		d, e := get[DeviceRecord](ctx, reader, "device/"+id)
		if !errors.Is(e, ErrNotFound) {
			return d, e
		}
		next, e := get[string](ctx, reader, "alias/"+id)
		if e != nil {
			return DeviceRecord{}, e
		}
		id = next
	}
	return DeviceRecord{}, ErrConflict
}
