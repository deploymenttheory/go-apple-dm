package ddm

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/schema/status"
)

// DeclarationStatus lists what an enrollment last reported per declaration.
func (e *Engine) DeclarationStatus(ctx context.Context, id mdm.EnrollmentID) ([]DeclarationStatus, error) {
	return e.store.DeclarationStatus(ctx, id)
}

// DeclarationStatusByIdentifier pages through every enrollment's status for
// one declaration.
func (e *Engine) DeclarationStatusByIdentifier(ctx context.Context, identifier string, p paging.Page) (paging.Result[EnrollmentDeclarationStatus], error) {
	return e.store.DeclarationStatusByIdentifier(ctx, identifier, p)
}

// StatusValues pages through an enrollment's status item values.
func (e *Engine) StatusValues(ctx context.Context, id mdm.EnrollmentID, q StatusValueQuery, p paging.Page) (paging.Result[StatusValue], error) {
	return e.store.StatusValues(ctx, id, q, p)
}

// StatusErrors pages through an enrollment's reported errors, newest first.
func (e *Engine) StatusErrors(ctx context.Context, id mdm.EnrollmentID, p paging.Page) (paging.Result[StatusError], error) {
	return e.store.StatusErrors(ctx, id, p)
}

// StatusReports pages through retained raw reports, newest first.
func (e *Engine) StatusReports(ctx context.Context, id mdm.EnrollmentID, p paging.Page) (paging.Result[StatusReportRecord], error) {
	return e.store.StatusReports(ctx, id, p)
}

// StatusItemClientCapabilities is the status item devices always report.
const StatusItemClientCapabilities = status.StatusItemTypeManagementClientCapabilities

// ClientCapabilities decodes the last reported management.client-capabilities
// item, or ErrNotFound when the device never reported it. The item is read
// defensively (decision record 0021, claim 7): a member that does not fit
// Apple's schema is logged and left empty rather than failing the caller,
// because devices have sent partial or oddly shaped capabilities and the
// check-in path must keep serving them.
func (e *Engine) ClientCapabilities(ctx context.Context, id mdm.EnrollmentID) (*status.ManagementClientCapabilitiesCapabilities, error) {
	return e.clientCapabilities(ctx, e.store, id)
}

// clientCapabilities reads the item through st: the store outside a
// transaction, or the Tx while a manifest is computed inside Update.
func (e *Engine) clientCapabilities(ctx context.Context, st StatusStore, id mdm.EnrollmentID) (*status.ManagementClientCapabilitiesCapabilities, error) {
	res, err := st.StatusValues(ctx, id, StatusValueQuery{PathPrefix: StatusItemClientCapabilities}, paging.Page{Limit: 1})
	if err != nil {
		return nil, err
	}
	for _, v := range res.Items {
		if v.Path != StatusItemClientCapabilities {
			continue
		}
		return e.decodeCapabilities(ctx, id, v.Value), nil
	}
	return nil, fmt.Errorf("%w: %s for %s", ErrNotFound, StatusItemClientCapabilities, id.ID)
}

// decodeCapabilities decodes each top-level member on its own so one
// malformed member does not discard the others. A value that is not an
// object yields an empty capability set.
func (e *Engine) decodeCapabilities(ctx context.Context, id mdm.EnrollmentID, raw []byte) *status.ManagementClientCapabilitiesCapabilities {
	var caps status.ManagementClientCapabilitiesCapabilities
	var members map[string]jsontext.Value
	if err := json.Unmarshal(raw, &members); err != nil {
		e.log.WarnContext(ctx, "ddm: malformed "+StatusItemClientCapabilities+", treating it as empty", "enrollment", id.ID, "error", err)
		return &caps
	}
	decode := func(key string, dst any, reset func()) {
		v, ok := members[key]
		if !ok {
			return
		}
		if err := json.Unmarshal(v, dst); err != nil {
			reset()
			e.log.WarnContext(ctx, "ddm: malformed "+StatusItemClientCapabilities+" member, ignoring it", "enrollment", id.ID, "member", key, "error", err)
		}
	}
	decode("supported-versions", &caps.SupportedVersions, func() { caps.SupportedVersions = nil })
	decode("supported-features", &caps.SupportedFeatures, func() { caps.SupportedFeatures = nil })
	decode("supported-payloads", &caps.SupportedPayloads, func() {
		caps.SupportedPayloads = status.ManagementClientCapabilitiesCapabilitiesSupportedPayloads{}
	})
	return &caps
}
