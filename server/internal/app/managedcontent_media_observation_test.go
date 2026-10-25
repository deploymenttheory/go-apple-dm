package app

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/micromdm/plist"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// TestManagedContentMediaReportChronology tests the retained-journal reducer's
// exact asset and temporal scope independently of transport and SQL ownership.
// Historical processing survives a newer absent or pending report; installation
// and version must come from the latest applicable report, never another book.
func TestManagedContentMediaReportChronology(t *testing.T) {
	since := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	row := func(at time.Time, books ...commands.ManagedMediaListResponseBooks) storage.QueuedCommand {
		t.Helper()
		raw, err := plist.Marshal(struct {
			UDID        string `plist:"UDID"`
			CommandUUID string `plist:"CommandUUID"`
			Status      string `plist:"Status"`
			commands.ManagedMediaListResponse
		}{"media-device", "media-query", "Acknowledged", commands.ManagedMediaListResponse{Books: books}})
		if err != nil {
			t.Fatal(err)
		}
		return storage.QueuedCommand{Command: mdm.Command{RequestType: "ManagedMediaList"}, State: storage.StateAcknowledged, CompletedAt: at, Result: &mdm.Response{Raw: raw}}
	}
	selected := commands.ManagedMediaListResponseBooks{ITunesStoreID: 1, State: new("Installed"), Version: new("book-2")}
	old := row(since.Add(-time.Second), selected)
	missing := row(since.Add(time.Second), commands.ManagedMediaListResponseBooks{ITunesStoreID: 2, State: new("Installed"), Version: new("foreign-version")})
	queued := row(since.Add(2*time.Second), selected)
	queued.State = storage.StatePending
	noResult := queued
	noResult.State, noResult.Result = storage.StateAcknowledged, nil
	unrelated := row(since.Add(3*time.Second), selected)
	unrelated.Command.RequestType = "InstalledApplicationList"
	base := managedcontent.Observation{Found: true, Raw: []byte("original install response")}
	observation, err := currentNativeMedia(base, "1", []storage.QueuedCommand{old, queued, noResult, unrelated, missing}, since)
	if err != nil || observation.Terminal || observation.Installed || observation.AppVersion != "" || !observation.ObservedAt.Equal(missing.CompletedAt) {
		t.Fatal("inapplicable or foreign book established installation", observation, err)
	}
	installed := row(since.Add(4*time.Second), selected)
	for _, tc := range []struct {
		name    string
		books   []commands.ManagedMediaListResponseBooks
		present bool
		version string
	}{
		{name: "absent"},
		{name: "pending", books: []commands.ManagedMediaListResponseBooks{{ITunesStoreID: 1, State: new("Downloading"), Version: new("uninstalled-version")}}},
		{name: "missing-state", books: []commands.ManagedMediaListResponseBooks{{ITunesStoreID: 1, Version: new("unknown-version")}}},
		{name: "managed-without-version", books: []commands.ManagedMediaListResponseBooks{{ITunesStoreID: 1, State: new("Managed")}}, present: true},
		{name: "installed", books: []commands.ManagedMediaListResponseBooks{selected}, present: true, version: "book-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			latest := row(since.Add(5*time.Second), tc.books...)
			// Deliberately reverse the journal order: chronology, rather than
			// storage pagination order, selects the current report.
			got, err := currentNativeMedia(base, "1", []storage.QueuedCommand{latest, unrelated, installed, old}, since)
			if err != nil || !got.Terminal || got.Installed != tc.present || got.AppVersion != tc.version || !got.ObservedAt.Equal(latest.CompletedAt) {
				t.Fatal("book chronology conflated historical processing and current facts", got, err)
			}
			var evidence struct {
				AssetID              string `json:"assetId"`
				InstallationResponse []byte `json:"installationResponse"`
				CurrentReport        []byte `json:"currentReport"`
			}
			if err := json.Unmarshal(got.Raw, &evidence); err != nil || evidence.AssetID != "1" || !bytes.Equal(evidence.InstallationResponse, base.Raw) || !bytes.Equal(evidence.CurrentReport, latest.Result.Raw) {
				t.Fatal("book evidence lost the original response bytes", err)
			}
		})
	}
	malformed := installed
	malformed.Result = &mdm.Response{Raw: []byte("invalid native response")}
	if _, err := currentNativeMedia(base, "1", []storage.QueuedCommand{malformed}, since); err == nil {
		t.Fatal("malformed acknowledged native evidence was accepted")
	}
	got, err := currentNativeMedia(base, "1", []storage.QueuedCommand{old}, since)
	if err != nil || got.Installed || got.Terminal || !got.ObservedAt.IsZero() || !bytes.Equal(got.Raw, base.Raw) {
		t.Fatal("pre-dispatch media report changed current evidence", got, err)
	}
}
