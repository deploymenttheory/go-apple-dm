package appleappidentity_test

import (
	"reflect"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appleappidentity"
)

func TestSearch(t *testing.T) {
	for _, tc := range []struct {
		term string
		want []appleappidentity.App
	}{
		{"  saFARI ", []appleappidentity.App{{Name: "Safari", BundleID: "com.apple.mobilesafari"}}},
		{"COM.APPLE.MOBILESAFARI", []appleappidentity.App{{Name: "Safari", BundleID: "com.apple.mobilesafari"}}},
		{"Final Cut", []appleappidentity.App{
			{Name: "Final Cut Camera", BundleID: "com.apple.FinalCutApp.companion"},
			{Name: "Final Cut Pro", BundleID: "com.apple.FinalCutApp"},
		}},
		{"no such app", []appleappidentity.App{}},
	} {
		if got := appleappidentity.Search(tc.term); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Search(%q) = %+v, want %+v", tc.term, got, tc.want)
		}
	}
}

func TestSnapshotIdentifiers(t *testing.T) {
	all := appleappidentity.Search("")
	if len(all) == 0 || !reflect.DeepEqual(all, appleappidentity.Search("  ")) {
		t.Fatal("empty search must return the catalogue")
	}
	seen := make(map[string]bool)
	for _, app := range all {
		if app.Name == "" || app.BundleID == "" || seen[app.BundleID] {
			t.Fatalf("invalid or duplicate entry: %+v", app)
		}
		seen[app.BundleID] = true
		if got, ok := appleappidentity.Lookup(app.BundleID); !ok || got != app {
			t.Fatalf("Lookup(%q) = %+v, %v", app.BundleID, got, ok)
		}
	}
	for _, id := range []string{"", "com.apple.mobilesafari ", "com.apple.MOBILESAFARI", "com.example.missing"} {
		if app, ok := appleappidentity.Lookup(id); ok || app != (appleappidentity.App{}) {
			t.Errorf("Lookup(%q) = %+v, %v", id, app, ok)
		}
	}
	// Some source identifiers use significant capitals and non-Apple prefixes.
	for _, id := range []string{"com.apple.MobileSMS", "com.apple.Passbook", "developer.apple.wwdc-Release", "com.shazam.Shazam"} {
		if _, ok := appleappidentity.Lookup(id); !ok {
			t.Errorf("source identifier missing: %s", id)
		}
	}
}

func TestResultsDoNotMutateCatalogue(t *testing.T) {
	want := appleappidentity.Search("Safari")
	result := appleappidentity.Search("Safari")
	result[0].Name = "Changed"
	result[0].BundleID = "com.example.changed"
	if got := appleappidentity.Search("Safari"); !reflect.DeepEqual(got, want) {
		t.Fatalf("caller mutated catalogue: %+v", got)
	}
}
