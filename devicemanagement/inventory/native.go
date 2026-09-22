package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

// EnrollmentReference prevents device and user channel identities from colliding.
func EnrollmentReference(kind string, id mdm.EnrollmentID) SourceReference {
	return SourceReference{Kind: kind, ResourceID: id.Channel.String() + ":" + id.ID}
}

// ObserveStatus retains the full incoming report and the effective per-item snapshot.
// Missing items in partial reports retain their own original observation times.
func (r *Repository) ObserveStatus(ctx context.Context, serial string, id mdm.EnrollmentID, u ddm.StatusUpdate) error {
	if id.Channel != mdm.ChannelDevice && id.Channel != mdm.ChannelUserEnrollmentDevice {
		return nil
	}
	ref := EnrollmentReference("ddm.status", id)
	return r.Backend.Update(ctx, func(tx Tx) error {
		fields := map[string]json.RawMessage{}
		times := map[string]time.Time{}
		if !u.FullReport {
			local, e := get[string](ctx, tx, "source/"+ref.Key())
			if e != nil && !errors.Is(e, ErrNotFound) {
				return e
			}
			if local != "" {
				d, e := get[DeviceRecord](ctx, tx, "device/"+local)
				if e != nil {
					return e
				}
				old := d.Sources[ref.Key()]
				for k, v := range old.Fields {
					fields[k] = v
					at, ok := old.FieldTimes[k]
					if !ok {
						at = old.ObservedAt
					}
					times[k] = at
				}
			}
		}
		for _, v := range u.Values {
			fields[v.Path] = append(json.RawMessage(nil), v.Value...)
			times[v.Path] = u.ReceivedAt
			if !v.LastSeen.IsZero() {
				times[v.Path] = v.LastSeen
			}
		}
		raw, e := json.Marshal(map[string]any{"report": json.RawMessage(u.Raw), "effective": fields})
		if e != nil {
			return e
		}
		o := Observation{Source: ref, ObservedAt: u.ReceivedAt, AttemptedAt: u.ReceivedAt, ExpiresAt: u.ReceivedAt.Add(24 * time.Hour), Fields: fields, FieldTimes: times, Raw: raw}
		if serial == "" {
			serial = String(fields["device.identifier.serial-number"])
		}
		_, e = r.ObserveTx(ctx, tx, serial, o)
		return e
	})
}
