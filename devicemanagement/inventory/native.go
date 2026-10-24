package inventory

import (
	"context"
	"encoding/json"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
)

// EnrollmentReference prevents device and user channel identities from colliding.
func EnrollmentReference(kind string, id mdm.EnrollmentID) source.Reference {
	return source.Reference{Kind: kind, ResourceID: id.Channel.String() + ":" + id.ID}
}

// ObserveStatus retains an original native report and its reported per-item times.
// Omitted fields remain in successful history for explicit caller-owned resolution.
// Enrollment authentication/method provenance is not inferred from a channel ID.
func (r *Repository) ObserveStatus(ctx context.Context, serial string, id mdm.EnrollmentID, u ddm.StatusUpdate) error {
	if id.Channel != mdm.ChannelDevice && id.Channel != mdm.ChannelUserEnrollmentDevice {
		return nil
	}
	ref := EnrollmentReference("ddm.status", id)
	fields := map[string]json.RawMessage{}
	times := map[string]time.Time{}
	for _, v := range u.Values {
		fields[v.Path] = append(json.RawMessage(nil), v.Value...)
		at := u.ReceivedAt
		if !v.LastSeen.IsZero() {
			at = v.LastSeen
		}
		times[v.Path] = at
	}
	o := Observation{Source: ref, ObservedAt: u.ReceivedAt, ExpiresAt: u.ReceivedAt.Add(24 * time.Hour), Fields: fields, FieldTimes: times, Raw: append(json.RawMessage(nil), u.Raw...)}
	_, err := r.Observe(ctx, serial, o)
	return err
}
