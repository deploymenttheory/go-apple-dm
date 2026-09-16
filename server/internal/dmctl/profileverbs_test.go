package dmctl_test

import (
	"strings"
	"testing"

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
