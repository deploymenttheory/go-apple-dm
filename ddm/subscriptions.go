package ddm

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

// SubscriptionIdentifier names the synthesised status-subscriptions
// declaration. An admin-supplied declaration with this identifier replaces
// the synthesised one.
const SubscriptionIdentifier = "com.deploymenttheory.mdm.status-subscriptions"

// DefaultSubscriptionBaseline is used until a device reports which status
// items it supports.
var DefaultSubscriptionBaseline = []string{
	"device.identifier.serial-number", "device.identifier.udid",
	"device.model.family", "device.model.identifier", "device.model.marketing-name",
	"device.operating-system.build-version", "device.operating-system.family",
	"device.operating-system.marketing-name", "device.operating-system.version",
	"management.client-capabilities", "management.declarations",
}

// DefaultSubscriptionExclude drops Apple's test items from subscriptions.
var DefaultSubscriptionExclude = []string{"test."}

// subscriptionItems picks the status items to subscribe an enrollment to:
// what the device reported it supports, filtered, or the baseline. The
// capabilities are read through tx because this runs inside Update, where
// going back to the store would deadlock on backends that serialise
// transactions.
func (e *Engine) subscriptionItems(ctx context.Context, tx Tx, id mdm.EnrollmentID) []string {
	caps, err := e.clientCapabilities(ctx, tx, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			e.log.WarnContext(ctx, "ddm: client capabilities unreadable, using the baseline", "enrollment", id.ID, "error", err)
		}
		return slices.Clone(e.subs.Baseline)
	}
	var items []string
	for _, name := range caps.SupportedPayloads.StatusItems {
		if name == "" || e.excluded(name) {
			continue
		}
		items = append(items, name)
	}
	if len(items) == 0 {
		return slices.Clone(e.subs.Baseline)
	}
	slices.Sort(items)
	return slices.Compact(items)
}

func (e *Engine) excluded(name string) bool {
	for _, p := range e.subs.Exclude {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// subscriptionItem builds the synthesised declaration as an expanded
// snapshot item so it is served from the snapshot like any other.
func (e *Engine) subscriptionItem(ctx context.Context, tx Tx, id mdm.EnrollmentID) (SnapshotItem, error) {
	names := e.subscriptionItems(ctx, tx, id)
	items := make([]map[string]string, 0, len(names))
	for _, n := range names {
		items = append(items, map[string]string{"Name": n})
	}
	raw, err := json.Marshal(map[string]any{
		"Type":       schemaddm.DeclarationTypeManagementStatusSubscriptions,
		"Identifier": SubscriptionIdentifier,
		"Payload":    map[string]any{"StatusItems": items},
	})
	if err != nil {
		return SnapshotItem{}, fmt.Errorf("ddm: subscriptions: %w", err)
	}
	d, err := ParseDeclaration(raw, e.target(ctx))
	if err != nil {
		return SnapshotItem{}, err
	}
	return SnapshotItem{
		DeclarationRef: DeclarationRef{Kind: d.Kind, Identifier: d.Identifier, ServerToken: d.ServerToken},
		BaseToken:      d.ServerToken,
		Expanded:       d.Canonical,
	}, nil
}
