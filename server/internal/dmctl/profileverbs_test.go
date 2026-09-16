package dmctl_test

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

func TestProfileLintCLI(t *testing.T) {
	const profile = `<?xml version="1.0"?><plist version="1.0"><dict><key>PayloadType</key><string>Configuration</string><key>PayloadIdentifier</key><string>example</string><key>PayloadUUID</key><string>id</string><key>PayloadVersion</key><integer>1</integer><key>PayloadContent</key><array><dict><key>PayloadType</key><string>com.apple.wifi.managed</string><key>PayloadIdentifier</key><string>example.wifi</string><key>PayloadUUID</key><string>wifi</string><key>PayloadVersion</key><integer>1</integer><key>SSID_STR</key><string>Example</string></dict></array></dict></plist>`
	for _, tc := range []struct {
		data string
		args []string
		code int
		want string
	}{
		{profile, []string{"-output", "json"}, 0, `"signature":"unsigned"`},
		{profile, nil, 0, "signature=unsigned"},
		{strings.Replace(profile, "<key>PayloadContent</key>", "<key>FutureKey</key><true/><key>PayloadContent</key>", 1), nil, 3, "unvalidated"},
		{"broken", nil, 1, "parse"},
		{profile, []string{"-require-signature"}, 1, "signature"},
	} {
		args := append([]string{"profile", "lint", "-file", "-", "-target", "macos:26"}, tc.args...)
		out, _, err := runWithStdin(t, noConfig(t), tc.data, args...)
		if dmctl.ExitCode(err) != tc.code || !strings.Contains(out, tc.want) {
			t.Fatalf("%v: %q %v", args, out, err)
		}
	}
	_, _, err := run(t, noConfig(t), "profile", "lint", "-file", "-")
	if dmctl.ExitCode(err) != 2 {
		t.Fatal(err)
	}
}

func TestProfileLintArgumentsAndFiles(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	badRoots := privateFixture(t, dir, "bad.pem", []byte("not a certificate"))
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"missing verb", []string{"profile"}, 2},
		{"unknown verb", []string{"profile", "unknown"}, 2},
		{"unknown flag", []string{"profile", "lint", "-unknown"}, 2},
		{"invalid target", []string{"profile", "lint", "-file", "-", "-target", "unknown:26"}, 2},
		{"unversioned target", []string{"profile", "lint", "-file", "-", "-target", "macos"}, 2},
		{"missing file", []string{"profile", "lint", "-file", missing, "-target", "macos:26"}, 1},
		{"missing roots", []string{"profile", "lint", "-file", "-", "-target", "macos:26", "-trust-roots", missing}, 1},
		{"invalid roots", []string{"profile", "lint", "-file", "-", "-target", "macos:26", "-trust-roots", badRoots}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := run(t, noConfig(t), tc.args...)
			if dmctl.ExitCode(err) != tc.code {
				t.Fatalf("exit = %d, want %d: %v", dmctl.ExitCode(err), tc.code, err)
			}
		})
	}
	ca, err := testpki.NewCA("profile lint")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := plist.Marshal(map[string]any{
		"PayloadType": "Configuration", "PayloadIdentifier": "example", "PayloadUUID": "profile",
		"PayloadVersion": int64(1), "PayloadContent": []any{map[string]any{
			"PayloadType": "com.apple.wifi.managed", "PayloadIdentifier": "example.wifi",
			"PayloadUUID": "wifi", "PayloadVersion": int64(1), "SSID_STR": "Example",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := cms.SignAttached(plain, ca.Cert, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	file := privateFixture(t, dir, "signed.mobileconfig", signed)
	roots := privateFixture(t, dir, "roots.pem", certificatePEM(ca.Cert.Raw))
	out, _, err := run(t, noConfig(t), "profile", "lint", "-file", file,
		"-target", "macos:26", "-trust-roots", roots, "-require-signature", "-output", "ndjson")
	if err != nil || !strings.Contains(out, `"trust":"trusted"`) || !strings.Contains(out, `"signature":"valid"`) {
		t.Fatalf("trusted file: %s %v", out, err)
	}
}

func TestProfileLintIOFailures(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		reader     io.Reader
		writer     io.Writer
		want       error
	}{
		{"read", "json", failReader{}, io.Discard, nil},
		{"json body", "json", strings.NewReader("broken"), &failWriter{}, errWrite},
		{"json newline", "ndjson", strings.NewReader("broken"), &failWriter{remaining: 1}, errWrite},
		{"text header", "human", strings.NewReader("broken"), &failWriter{}, errWrite},
		{"text issue", "human", strings.NewReader("broken"), &failWriter{remaining: 1}, errWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := noConfig(t)
			err := dmctl.Run(t.Context(), []string{"profile", "lint", "-file", "-", "-target", "macos:26", "-output", tc.mode},
				func(k string) string { return env[k] }, tc.reader, tc.writer, io.Discard)
			if dmctl.ExitCode(err) != 1 || (tc.want != nil && !errors.Is(err, tc.want)) ||
				(tc.want == nil && !strings.Contains(err.Error(), "read profile")) {
				t.Fatalf("I/O failure not propagated: %v", err)
			}
		})
	}
}
