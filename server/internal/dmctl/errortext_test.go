package dmctl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupFailuresReadAsOneSentence pins the exact text of the failures a person meets
// while setting a server up for the first time. Each line names the problem once, with
// the operand, and never the packages the error passed through: "dmctl: dmctl: app:
// app:" narrates a call path, and a URL or a path printed twice is noise the reader has
// to discard before finding the fault.
func TestSetupFailuresReadAsOneSentence(t *testing.T) {
	dir := t.TempDir()
	setup := filepath.Join(dir, "setup.json")
	if err := os.WriteFile(setup, []byte(`{"version":1,"environment":{"DM_STORAGE":"sqlite","DM_STORAGE_KEYS":"storage"},"secretFiles":{"DM_BOOTSTRAP_TOKEN":"`+filepath.Join(dir, "absent")+`"},"setup":{"role":"customer"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		args []string
		want string
	}{
		"ServerUnreachable": {
			[]string{"-server", "http://127.0.0.1:1", "setup", "status"},
			"cannot reach http://127.0.0.1:1 (connection refused)",
		},
		"UnknownRole": {
			[]string{"setup", "init", "-dir", filepath.Join(dir, "d"), "-role", "banana"},
			`invalid configuration: setup role "banana" must be customer, vendor or combined`,
		},
		"FileAsPositionalArgument": {
			[]string{"setup", "adopt", setup},
			`usage: setup adopt takes flags only, not "` + setup + `"; the setup document is passed as -setup-file <path>`,
		},
		"MissingSecretFile": {
			[]string{"setup", "adopt", "-setup-file", setup},
			"read secret reference DM_BOOTSTRAP_TOKEN: open " + filepath.Join(dir, "absent") + ": no such file or directory",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := run(t, noConfig(t), tc.args...)
			if err == nil {
				t.Fatal("no error")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("\n got: %s\nwant: %s", got, tc.want)
			}
			if strings.Contains(err.Error(), "dmctl:") || strings.Contains(err.Error(), "app:") {
				t.Fatalf("a package label survived: %s", err)
			}
		})
	}
}
