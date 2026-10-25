package appsbooks

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
)

// UnlimitedTotalCount is Apple's totalCount sentinel when unlimited is omitted.
// It is not a finite license quantity.
const UnlimitedTotalCount int64 = 2147483647

// StockKind distinguishes missing availability, finite availability and unlimited stock.
type StockKind string

const (
	StockUnknown   StockKind = "unknown"
	StockFinite    StockKind = "finite"
	StockUnlimited StockKind = "unlimited"
)

// LicenseStock contains an available count only when Kind is StockFinite.
// Availability does not establish successful licensing or installation.
type LicenseStock struct {
	Kind      StockKind
	Available int64
}

// Stock interprets availability without treating Apple's sentinel as finite stock.
// The original numeric values remain in the record and Raw source.
func (a AssetRecord) Stock() LicenseStock {
	if a.TotalCount != nil && *a.TotalCount == UnlimitedTotalCount {
		return LicenseStock{Kind: StockUnlimited}
	}
	if a.AvailableCount == nil {
		return LicenseStock{Kind: StockUnknown}
	}
	return LicenseStock{Kind: StockFinite, Available: *a.AvailableCount}
}

// UnlimitedAssetRecord models Apple's UnlimitedResponseAsset. The separate
// unlimitedAssets array establishes unlimited stock, including when counts are absent.
// Available, total and retired counts do not apply to this representation.
type UnlimitedAssetRecord struct {
	Asset
	ProductType        string         `json:"productType"`
	DeviceAssignable   bool           `json:"deviceAssignable"`
	Revocable          bool           `json:"revocable"`
	AssignedCount      *int64         `json:"assignedCount,omitempty"`
	SupportedPlatforms []string       `json:"supportedPlatforms"`
	Raw                jsontext.Value `json:"-"`
}

// CheckAssignment applies the same product and assignment restrictions to unlimited assets.
func (a UnlimitedAssetRecord) CheckAssignment(device, removing bool) error {
	return (AssetRecord{
		ProductType: a.ProductType, DeviceAssignable: a.DeviceAssignable,
		Revocable: a.Revocable,
	}).CheckAssignment(device, removing)
}

// AssetInventory is one walker item. Exactly one pointer is populated by WalkAssets,
// retaining whether Apple returned assets or unlimitedAssets. A Record can itself
// represent unlimited stock through Apple's totalCount sentinel; consult Stock.
type AssetInventory struct {
	Record    *AssetRecord
	Unlimited *UnlimitedAssetRecord
}

// Stock reports the represented stock, or unknown for an empty inventory item.
func (a AssetInventory) Stock() LicenseStock {
	if a.Unlimited != nil {
		return LicenseStock{Kind: StockUnlimited}
	}
	if a.Record != nil {
		return a.Record.Stock()
	}
	return LicenseStock{Kind: StockUnknown}
}

// UnmarshalJSON retains the complete source object, including unknown fields,
// explicit nulls, false values and exact integer tokens. Raw is independent of
// subsequent typed-field edits; marshal Raw when exporting the original evidence.
func (a *AssetRecord) UnmarshalJSON(data []byte) error {
	type wire AssetRecord
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*a = AssetRecord(decoded)
	a.Raw = append(jsontext.Value(nil), data...)
	return nil
}

// UnmarshalJSON retains the original unlimited-asset object alongside typed fields.
func (a *UnlimitedAssetRecord) UnmarshalJSON(data []byte) error {
	type wire UnlimitedAssetRecord
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*a = UnlimitedAssetRecord(decoded)
	a.Raw = append(jsontext.Value(nil), data...)
	return nil
}

// UnmarshalJSON retains the original page and the separate asset representations.
func (p *AssetsPage) UnmarshalJSON(data []byte) error {
	type wire AssetsPage
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = AssetsPage(decoded)
	p.Raw = append(jsontext.Value(nil), data...)
	return nil
}
