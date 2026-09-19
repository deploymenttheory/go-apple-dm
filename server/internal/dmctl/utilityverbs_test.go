package dmctl_test

import (
	"encoding/csv"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

type utilityTransport func(*http.Request) (*http.Response, error)

func (f utilityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func publicIdentityTransport(t *testing.T, body string, status int) {
	t.Helper()
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: utilityTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "itunes.apple.com" || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected endpoint or credentials: %s", r.URL)
		}
		return &http.Response{StatusCode: status, Request: r, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = old })
}

const (
	publicIdentityFixture = `{"resultCount":1,"results":[{"trackId":123,"bundleId":"com.example.app","trackName":"Example","artistName":"Developer, Inc","version":"1.2"}]}`
	binaryIdentityFixture = `{"path":"/Example.app","executable":"/Example.app/Contents/MacOS/Example","bundleID":"com.example.bundle","architectures":[{"name":"arm64","cdhash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","signingID":"com.example.signed","teamID":"EXAMPLETEAM","signature":{"status":"valid","category":"DeveloperID"}},{"name":"arm64e.x1","cdhash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","signingID":"com.example.signed","teamID":"EXAMPLETEAM","signature":{"status":"valid","category":"DeveloperID"}}]}`
)

func TestPublicAppStoreIdentityCLI(t *testing.T) {
	publicIdentityTransport(t, publicIdentityFixture, 200)
	env := noConfig(t)
	env["DMCTL_TOKEN"] = "@/this-token-must-not-be-read"
	for _, action := range []string{"search", "lookup"} {
		for _, mode := range []string{"human", "json", "ndjson", "csv"} {
			term := "Example"
			if action == "lookup" {
				term = "123"
			}
			out, _, err := run(t, env, "utility", "public-app-store-identity", action, term, "-country", "GB", "-entity", "software", "-output", mode)
			if err != nil || !strings.Contains(out, "com.example.app") {
				t.Fatalf("%s/%s: %s %v", action, mode, out, err)
			}
			if mode == "csv" {
				rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
				if err != nil || len(rows) != 2 || rows[1][3] != "Developer, Inc" {
					t.Fatalf("CSV: %v %v", rows, err)
				}
			}
		}
	}
}

func TestUtilityAppListsAndPrivacy(t *testing.T) {
	for _, action := range []string{"allow", "deny"} {
		out, _, err := run(t, noConfig(t), "utility", "appsettings", action, "-bundle-id", "com.example.app", "-bundle-id", "com.apple.webapp", "-target", "ios:27,channel=device,supervised")
		var payload ddm.AppSettings
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Allowed == nil || len(payload.Allowed.AllowedApps)+len(payload.Allowed.DeniedApps) != 2 {
			t.Fatal(out)
		}
	}
	privacy := `[{"bundleID":"com.example.app","designatedRequirement":"anchor apple","permissions":{"OrganizationJustification":"Video meetings","Camera":"Allow"}}]`
	file := privateFixture(t, t.TempDir(), "privacy.json", []byte(privacy))
	for _, source := range []string{"-", file} {
		out, _, err := runWithStdin(t, noConfig(t), privacy, "utility", "appsettings", "privacy", "-input", source, "-target", "macos:27,channel=user,supervised")
		if err != nil || !strings.Contains(out, `"com.example.app {anchor apple}"`) {
			t.Fatalf("%s %v", out, err)
		}
	}
}

func TestUtilityBinaryRules(t *testing.T) {
	for _, action := range []string{"allow", "deny"} {
		for _, mode := range []string{"cdhash", "app", "team"} {
			args := []string{"utility", "appsettings", action, "-identity", "-", "-match", mode, "-target", "macos:27,channel=device,supervised"}
			if action == "allow" {
				args = append(args, "-always-allow-managed-apps=false")
			}
			out, _, err := runWithStdin(t, noConfig(t), binaryIdentityFixture, args...)
			if err != nil {
				t.Fatal(err)
			}
			var payload ddm.AppSettings
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatal(err)
			}
			count := len(payload.Allowed.AllowedBinaries) + len(payload.Allowed.DeniedBinaries)
			want := 1
			if mode == "cdhash" {
				want = 2
			}
			if count != want {
				t.Fatal(out)
			}
			if action == "allow" && (payload.Allowed.AlwaysAllowManagedApps == nil || *payload.Allowed.AlwaysAllowManagedApps) {
				t.Fatal("explicit false lost")
			}
		}
	}
}

func TestUtilityNativeInspection(t *testing.T) {
	if runtime.GOOS != "darwin" {
		_, _, err := run(t, noConfig(t), "utility", "appidentity", "inspect", "ignored")
		if !errors.Is(err, appidentity.ErrUnsupported) {
			t.Fatal(err)
		}
		return
	}
	for _, mode := range []string{"human", "json", "ndjson", "csv"} {
		out, _, err := run(t, noConfig(t), "utility", "appidentity", "inspect", "/usr/bin/true", "-output", mode)
		if err != nil || !strings.Contains(out, "com.apple.true") {
			t.Fatalf("%s: %s %v", mode, out, err)
		}
		if mode == "json" {
			payload, _, err := runWithStdin(t, noConfig(t), out, "utility", "appsettings", "deny", "-identity", "-", "-match", "cdhash", "-target", "macos:27,channel=device,supervised")
			if err != nil || !strings.Contains(payload, "DeniedBinaries") {
				t.Fatalf("native pipeline: %s %v", payload, err)
			}
		}
	}
}

func TestUtilityUsageFailures(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"bad"},
		{"public-app-store-identity"},
		{"public-app-store-identity", "bad"},
		{"public-app-store-identity", "search"},
		{"public-app-store-identity", "search", "Example", "-country", "GB", "-entity", "software", "-all"},
		{"public-app-store-identity", "search", "Example", "-country", "GB", "-entity", "software", "-output", "bad"},
		{"public-app-store-identity", "search", "Example", "-country", "GB", "-entity", "software", "-timeout", "0s"},
		{"public-app-store-identity", "search", "Example", "-country", "GB", "-entity", "software", "-limit", "201"},
		{"public-app-store-identity", "lookup", "abc", "-country", "GB", "-entity", "software"},
		{"public-app-store-identity", "lookup", "0", "-country", "GB", "-entity", "software"},
		{"public-app-store-identity", "search", "-bad"},
		{"appidentity"},
		{"appidentity", "bad"},
		{"appidentity", "inspect"},
		{"appidentity", "inspect", "path", "-output", "bad"},
		{"appidentity", "inspect", "-bad"},
		{"appsettings"},
		{"appsettings", "bad"},
		{"appsettings", "allow"},
		{"appsettings", "allow", "-bad"},
		{"appsettings", "allow", "-target", "bad"},
		{"appsettings", "allow", "-target", "ios:27", "-output", "csv"},
		{"appsettings", "allow", "-target", "ios:27"},
		{"appsettings", "allow", "-target", "ios:27", "-bundle-id", "com.example", "-identity", "-"},
		{"appsettings", "allow", "-target", "ios:27", "-bundle-id", "com.example", "-match", "cdhash"},
		{"appsettings", "allow", "-target", "ios:27", "-bundle-id", "com.example"},
		{"appsettings", "allow", "-target", "macos:27,channel=device,supervised", "-identity", "-"},
		{"appsettings", "allow", "-target", "macos:27,channel=device,supervised", "-identity", "-", "-match", "signing-id"},
		{"appsettings", "privacy", "-target", "macos:27,channel=user,supervised"},
	} {
		_, _, err := runWithStdin(t, noConfig(t), binaryIdentityFixture, append([]string{"utility"}, args...)...)
		if dmctl.ExitCode(err) != 2 {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{{"-h"}, {"public-app-store-identity", "search", "-h"}, {"appidentity", "inspect", "-h"}, {"appsettings", "privacy", "-h"}} {
		if _, _, err := run(t, noConfig(t), append([]string{"utility"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUtilityInputFailures(t *testing.T) {
	args := []string{"utility", "appsettings", "deny", "-identity", "-", "-match", "cdhash", "-target", "macos:27,channel=device,supervised"}
	for _, data := range []string{"bad", binaryIdentityFixture + "{}", strings.Replace(binaryIdentityFixture, `"path"`, `"typo"`, 1), strings.Repeat("x", (8<<20)+1)} {
		if _, _, err := runWithStdin(t, noConfig(t), data, args...); dmctl.ExitCode(err) != 2 {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		_, _, err := run(t, noConfig(t), "utility", "appsettings", "privacy", "-input", file, "-target", "ios:27,channel=device,supervised")
		if dmctl.ExitCode(err) != 1 {
			t.Fatal(err)
		}
	}
	_, _, err := runWithStdin(t, noConfig(t), "bad", "utility", "appsettings", "privacy", "-input", "-", "-target", "ios:27,channel=device,supervised")
	if dmctl.ExitCode(err) != 2 {
		t.Fatal(err)
	}
	_, _, err = run(t, noConfig(t), "utility", "appidentity", "inspect", filepath.Join(t.TempDir(), "missing"))
	if dmctl.ExitCode(err) != 1 {
		t.Fatal(err)
	}
}

func TestUtilityIOFailures(t *testing.T) {
	publicIdentityTransport(t, publicIdentityFixture, 200)
	for _, mode := range []string{"human", "json", "ndjson", "csv"} {
		args := []string{"utility", "public-app-store-identity", "search", "Example", "-country", "GB", "-entity", "software", "-output", mode}
		err := dmctl.Run(t.Context(), args, nil, strings.NewReader(""), &failWriter{}, io.Discard)
		if !errors.Is(err, errWrite) {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	args := []string{"utility", "appsettings", "privacy", "-input", "-", "-target", "ios:27,channel=device,supervised"}
	if err := dmctl.Run(t.Context(), args, nil, failReader{}, io.Discard, io.Discard); dmctl.ExitCode(err) != 1 {
		t.Fatal(err)
	}
	args = []string{"utility", "appsettings", "deny", "-bundle-id", "com.example", "-target", "ios:27,channel=device,supervised"}
	if err := dmctl.Run(t.Context(), args, nil, strings.NewReader(""), &failWriter{remaining: 1}, io.Discard); !errors.Is(err, errWrite) {
		t.Fatal(err)
	}
	if err := dmctl.Run(t.Context(), []string{"utility", "-h"}, nil, strings.NewReader(""), io.Discard, &failWriter{}); !errors.Is(err, errWrite) {
		t.Fatal(err)
	}
}

func TestUtilityPublicLookupFailure(t *testing.T) {
	publicIdentityTransport(t, "unavailable", 503)
	_, _, err := run(t, noConfig(t), "utility", "public-app-store-identity", "lookup", "123", "-country", "GB", "-entity", "software")
	if dmctl.ExitCode(err) != 1 {
		t.Fatal(err)
	}
}
