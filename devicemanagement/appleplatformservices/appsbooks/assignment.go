package appsbooks

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// ErrAssignmentUnavailable means a complete current read did not establish one
// applicable assignment. Aggregate event completion alone is not this evidence.
var ErrAssignmentUnavailable = errors.New("appsbooks: applicable assignment unavailable")

// AssetEvidence retains a complete current native capability traversal.
type AssetEvidence struct {
	LocationID       string      `json:"locationId"`
	LocationRevision uint64      `json:"locationRevision"`
	UID              string      `json:"uid"`
	MDMID            string      `json:"mdmId"`
	Asset            AssetRecord `json:"asset"`
	Source           SourceBatch `json:"source"`
}

// AssignmentEvidence retains complete fresh asset and target-specific assignment
// reads. User assignments require Apple's Associated state. Source pages may
// contain personal data and require privileged storage and access.
type AssignmentEvidence struct {
	LocationID       string      `json:"locationId"`
	LocationRevision uint64      `json:"locationRevision"`
	Configuration    []byte      `json:"configuration"`
	UID              string      `json:"uid"`
	MDMID            string      `json:"mdmId"`
	Asset            AssetRecord `json:"asset"`
	Assignment       Assignment  `json:"assignment"`
	Assets           SourceBatch `json:"assets"`
	Assignments      SourceBatch `json:"assignments"`
}

// ConfirmAssignment reads current capabilities and exactly one assignment for one
// asset and one serial or Apple clientUserId. It does not assign content or infer
// licensing from stock. Complete, version-consistent traversals are required;
// duplicate, missing, foreign or inactive targets never produce partial proof.
// A concurrent location change invalidates this read, including token renewal.
func (w *Workflow) ConfirmAssignment(ctx context.Context, location string, asset Asset, serial, user string, maxPages int) (AssignmentEvidence, error) {
	if asset.AdamID == "" || (asset.PricingParam != "STDQ" && asset.PricingParam != "PLUS") || (serial == "") == (user == "") || maxPages <= 0 {
		return AssignmentEvidence{}, ErrInput
	}
	l, err := w.Load(ctx, location)
	if err != nil {
		return AssignmentEvidence{}, err
	}
	_, client, err := w.session(ctx, location, l.Revision, false)
	if err != nil {
		return AssignmentEvidence{}, err
	}
	configuration, err := client.ClientConfig(ctx)
	if err != nil {
		return AssignmentEvidence{}, err
	}
	if configuration.MDMInfo == nil || configuration.MDMInfo.ID != l.MDMID {
		return AssignmentEvidence{}, ErrOwnership
	}
	out := AssignmentEvidence{LocationID: location, LocationRevision: l.Revision, UID: l.UID, MDMID: l.MDMID, Configuration: append([]byte(nil), configuration.Raw...)}
	metadata, err := readConfirmedAsset(ctx, client, asset, maxPages)
	if err != nil {
		return AssignmentEvidence{}, err
	}
	out.Asset = metadata.Asset
	out.Assets = metadata.Source
	count := 0
	out.Assignments, err = confirmationPages(ctx, maxPages, func(ctx context.Context, index int) (jsontext.Value, Pagination, error) {
		page, err := client.Assignments(ctx, AssignmentsQuery{PageIndex: index, AdamID: asset.AdamID, PricingParam: asset.PricingParam, SerialNumber: serial, ClientUserID: user, IncludeUserState: new(true), ExcludeInactiveUsers: new(true)})
		if err != nil {
			return nil, Pagination{}, err
		}
		for _, a := range page.Assignments {
			if a.Asset != asset || a.SerialNumber != serial || a.ClientUserID != user {
				return nil, Pagination{}, ErrProtocol
			}
			count++
			out.Assignment = a
		}
		return page.Raw, page.Pagination, nil
	})
	if err != nil {
		return AssignmentEvidence{}, err
	}
	if count != 1 || (user != "" && out.Assignment.UserStatus != "Associated") {
		return AssignmentEvidence{}, ErrAssignmentUnavailable
	}
	err = w.store.Update(ctx, []string{locationKey(location, "metadata")}, func(tx state.Tx) error {
		current, err := w.readLocation(ctx, tx, location)
		if err != nil {
			return err
		}
		if current.Revision != l.Revision {
			return ErrRevision
		}
		out.Assets.ObservedAt = tx.Now()
		out.Assignments.ObservedAt = tx.Now()
		return nil
	})
	if err != nil {
		return AssignmentEvidence{}, err
	}
	return out, nil
}

// ReadAsset returns current native capabilities without assigning a license.
// Missing/duplicate assets or changing page versions do not yield partial proof.
func (w *Workflow) ReadAsset(ctx context.Context, location string, asset Asset, maxPages int) (AssetEvidence, error) {
	if asset.AdamID == "" || (asset.PricingParam != "STDQ" && asset.PricingParam != "PLUS") || maxPages <= 0 {
		return AssetEvidence{}, ErrInput
	}
	l, err := w.Load(ctx, location)
	if err != nil {
		return AssetEvidence{}, err
	}
	_, client, err := w.session(ctx, location, l.Revision, false)
	if err != nil {
		return AssetEvidence{}, err
	}
	out, err := readConfirmedAsset(ctx, client, asset, maxPages)
	if err != nil {
		return AssetEvidence{}, err
	}
	out.LocationID = location
	out.LocationRevision = l.Revision
	out.UID = l.UID
	out.MDMID = l.MDMID
	err = w.store.Update(ctx, []string{locationKey(location, "metadata")}, func(tx state.Tx) error {
		current, err := w.readLocation(ctx, tx, location)
		if err != nil {
			return err
		}
		if current.Revision != l.Revision {
			return ErrRevision
		}
		out.Source.ObservedAt = tx.Now()
		return nil
	})
	if err != nil {
		return AssetEvidence{}, err
	}
	return out, nil
}

// readConfirmedAsset preserves the native representation while requiring one asset.
func readConfirmedAsset(ctx context.Context, client *Client, asset Asset, maxPages int) (AssetEvidence, error) {
	out := AssetEvidence{}
	var err error
	count := 0
	out.Source, err = confirmationPages(ctx, maxPages, func(ctx context.Context, index int) (jsontext.Value, Pagination, error) {
		page, err := client.Assets(ctx, AssetsQuery{PageIndex: index, AdamID: asset.AdamID, PricingParam: asset.PricingParam})
		if err != nil {
			return nil, Pagination{}, err
		}
		for _, a := range page.Assets {
			if a.Asset != asset {
				return nil, Pagination{}, ErrProtocol
			}
			count++
			out.Asset = a
		}
		for _, a := range page.UnlimitedAssets {
			if a.Asset != asset {
				return nil, Pagination{}, ErrProtocol
			}
			count++
			out.Asset = AssetRecord{Asset: a.Asset, ProductType: a.ProductType, DeviceAssignable: a.DeviceAssignable, Revocable: a.Revocable, SupportedPlatforms: a.SupportedPlatforms, Raw: a.Raw}
		}
		return page.Raw, page.Pagination, nil
	})
	if err != nil {
		return AssetEvidence{}, err
	}
	if count != 1 {
		return AssetEvidence{}, ErrAssignmentUnavailable
	}
	return out, nil
}

// confirmationPages requires a coherent observation, rather than publishing the
// first-page checkpoint of a changing synchronization stream.
func confirmationPages(ctx context.Context, maxPages int, fetch func(context.Context, int) (jsontext.Value, Pagination, error)) (SourceBatch, error) {
	version := ""
	total := -1
	return collectPages(ctx, maxPages, "", func(ctx context.Context, index int) (jsontext.Value, Pagination, error) {
		raw, page, err := fetch(ctx, index)
		if err != nil {
			return nil, Pagination{}, err
		}
		if version == "" {
			version = page.VersionID
		} else if page.VersionID != version {
			return nil, Pagination{}, ErrProtocol
		}
		// Apple defines zero-based pages and lastPage=totalPages-1. Missing or
		// null totals remain unreported; never reinterpret them as numeric zero.
		var fields map[string]jsontext.Value
		if json.Unmarshal(raw, &fields) != nil {
			return nil, Pagination{}, ErrProtocol
		}
		if reported, ok := fields["totalPages"]; ok && strings.TrimSpace(string(reported)) != "null" {
			var n int
			if json.Unmarshal(reported, &n) != nil || n < 0 || (total >= 0 && total != n) {
				return nil, Pagination{}, ErrProtocol
			}
			total = n
		}
		if page.NextPageIndex != nil && *page.NextPageIndex != index+1 {
			return nil, Pagination{}, ErrProtocol
		}
		if total == 0 {
			if index != 0 || page.NextPageIndex != nil {
				return nil, Pagination{}, ErrProtocol
			}
			for _, name := range []string{"assets", "unlimitedAssets", "assignments"} {
				if value, ok := fields[name]; ok {
					var items []jsontext.Value
					if json.Unmarshal(value, &items) != nil || len(items) > 0 {
						return nil, Pagination{}, ErrProtocol
					}
				}
			}
		} else if total > 0 {
			if index >= total || (page.NextPageIndex == nil && index != total-1) || (page.NextPageIndex != nil && *page.NextPageIndex >= total) {
				return nil, Pagination{}, ErrProtocol
			}
		}
		return raw, page, nil
	})
}
