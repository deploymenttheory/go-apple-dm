package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/status"
)

// ManagedContentDDMEvidence is privileged provenance for the selected native
// declaration and app. Reports retain original authenticated status bytes; the
// enclosing SDK Observation carries the server's folded installation facts.
type ManagedContentDDMEvidence struct {
	BundleID              string                   `json:"bundleId"`
	DeclarationIdentifier string                   `json:"declarationIdentifier"`
	ServerToken           string                   `json:"serverToken"`
	PublishedAt           time.Time                `json:"publishedAt"`
	Declarations          []ddm.DeclarationStatus  `json:"declarations"`
	Reports               []ddm.StatusReportRecord `json:"reports"`
}

type contentDDMFold struct {
	baseline   bool
	terminal   bool
	bundle     string
	current    *status.AppManagedListApp
	observed   time.Time
	generation bool
}

// contentStatusItem reads an exact native status item in nested or dotted-key
// protocol representation, preserving its original bytes rather than projecting
// through a cross-source inventory view.
func contentStatusItem(items map[string]json.RawMessage, path string) (json.RawMessage, bool, error) {
	if raw, ok := items[path]; ok {
		return raw, true, nil
	}
	first, rest, ok := strings.Cut(path, ".")
	if !ok {
		return nil, false, nil
	}
	raw, present := items[first]
	if !present {
		return nil, false, nil
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return nil, false, managedcontent.ErrProtocol
	}
	return contentStatusItem(nested, rest)
}

// declarativeContentObservation folds only this owned declaration's authenticated
// app observations over retained native reports. Full reports reset the app set;
// complete owned incremental rows establish specific app facts. Unrelated entries and same-token heartbeats retain
// existing app facts without changing their original observation timestamps.
func (v contentDelivery) declarativeContentObservation(ctx context.Context, d managedcontent.Dispatch, created time.Time) (managedcontent.Observation, error) {
	out := dispatchObservation(d)
	out.Found = true
	out.DeclarationPublished = true
	out.ObservedAt = created
	rows, err := v.a.Engine.DeclarationStatus(ctx, d.Enrollment)
	if err != nil {
		return out, err
	}
	currentToken := false
	for _, row := range rows {
		if row.Identifier == d.Declarations[0].Identifier && row.ServerToken == d.Declarations[0].ServerToken && row.Active && row.Valid == "valid" {
			currentToken = true
		}
	}
	var reports []ddm.StatusReportRecord
	after := ""
	for {
		page, err := v.a.Engine.StatusReports(ctx, d.Enrollment, paging.Page{Limit: 100, Cursor: after})
		if err != nil {
			return out, err
		}
		reports = append(reports, page.Items...)
		if page.NextCursor == "" {
			break
		}
		after = page.NextCursor
	}
	slices.SortFunc(reports, func(a, b ddm.StatusReportRecord) int {
		if a.Seq < b.Seq {
			return -1
		}
		if a.Seq > b.Seq {
			return 1
		}
		return 0
	})
	fold := contentDDMFold{generation: currentToken}
	for _, record := range reports {
		if err := fold.apply(record, d, created); err != nil {
			return out, err
		}
	}
	out.Terminal = fold.terminal
	if !fold.observed.IsZero() {
		out.ObservedAt = fold.observed
	}
	if currentToken && fold.generation && fold.baseline && fold.current != nil && !fold.observed.Before(created) {
		app := fold.current
		if app.Identifier != "" && app.DeclarationIdentifier != nil && *app.DeclarationIdentifier == d.Declarations[0].Identifier && app.State != nil && *app.State == "managed" && (app.Removed == nil || !*app.Removed) {
			// A fresh identifier cannot appear in an earlier app report, so its
			// current managed installation also proves terminal processing when
			// generation confirmation followed the app report. A successor inherits
			// its predecessor's identifier, and an app report sent before the device
			// applied the new token is indistinguishable from one sent after; it
			// settles only through an app report that follows confirmation.
			if d.Predecessor == nil {
				out.Terminal = true
			}
			out.Installed = true
			if app.Version != nil {
				out.AppVersion = *app.Version
			}
		}
	}
	raw, err := json.Marshal(ManagedContentDDMEvidence{BundleID: fold.bundle, DeclarationIdentifier: d.Declarations[0].Identifier, ServerToken: d.Declarations[0].ServerToken, PublishedAt: created, Declarations: rows, Reports: reports})
	if err != nil {
		return out, err
	}
	out.Raw = raw
	return out, nil
}

// apply maintains a narrow owned-app fold without changing the native raw store
// or treating bundle identity as authorization for a foreign declaration.
func (f *contentDDMFold) apply(record ddm.StatusReportRecord, d managedcontent.Dispatch, created time.Time) error {
	var report struct {
		StatusItems map[string]json.RawMessage `json:"StatusItems"`
	}
	if json.Unmarshal(record.Raw, &report) != nil {
		return managedcontent.ErrProtocol
	}
	if record.FullReport {
		f.baseline = true
		f.current = nil
		f.observed = record.ReceivedAt
		f.generation = false
	}
	declarations, present, err := contentStatusItem(report.StatusItems, "management.declarations")
	if err != nil {
		return err
	}
	if present {
		var value struct {
			Configurations []struct {
				Identifier  string `json:"identifier"`
				ServerToken string `json:"server-token"`
				Active      bool   `json:"active"`
				Valid       string `json:"valid"`
			} `json:"configurations"`
		}
		if json.Unmarshal(declarations, &value) != nil {
			return managedcontent.ErrProtocol
		}
		for _, row := range value.Configurations {
			if row.Identifier != d.Declarations[0].Identifier {
				continue
			}
			if row.ServerToken != d.Declarations[0].ServerToken || !row.Active || row.Valid != "valid" {
				f.current = nil
				f.observed = record.ReceivedAt
				f.generation = false
				continue
			}
			f.generation = true
		}
	}
	apps, present, err := contentStatusItem(report.StatusItems, "app.managed.list")
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	var values []status.AppManagedListApp
	if json.Unmarshal(apps, &values) != nil {
		return managedcontent.ErrProtocol
	}
	for _, app := range values {
		exact := app.DeclarationIdentifier != nil && *app.DeclarationIdentifier == d.Declarations[0].Identifier
		if exact {
			if app.Identifier != "" {
				f.bundle = app.Identifier
			}
			if app.Identifier != "" && app.State != nil {
				f.baseline = true
			}
			copyApp := app
			f.current = &copyApp
			f.observed = record.ReceivedAt
			if app.Identifier != "" && f.generation && !record.ReceivedAt.Before(created) && app.State != nil && (*app.State == "managed" || *app.State == "managed-but-uninstalled" || *app.State == "failed") {
				f.terminal = true
			}
			continue
		}
		if f.bundle != "" && app.Identifier == f.bundle {
			// Only an identifier-only removal can refer to our earlier bundle without
			// the owned declaration ID. A foreign/absent declaration never proves a
			// positive installation, even when its bundle string matches.
			f.current = nil
			f.observed = record.ReceivedAt
		}
	}
	return nil
}
