package dmctl_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupFailuresReadAsOneSentence pins the text of the failures a person meets while
// setting a server up for the first time. Each line names the problem once, with the
// operand, and never the packages the error passed through: "dmctl: dmctl: app: app:"
// narrates a call path, and a URL or a path printed twice is noise the reader has to
// discard before finding the fault. The operating system's own wording for a missing file
// or a refused connection differs by platform, so those cases pin everything up to it.
func TestSetupFailuresReadAsOneSentence(t *testing.T) {
	dir := t.TempDir()
	setup := filepath.Join(dir, "setup.json")
	absent := filepath.Join(dir, "absent")
	if err := os.WriteFile(setup, []byte(`{"version":1,"environment":{"DM_STORAGE":"sqlite","DM_STORAGE_KEYS":"storage"},"secretFiles":{"DM_BOOTSTRAP_TOKEN":`+fmt.Sprintf("%q", absent)+`},"setup":{"role":"customer"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		args   []string
		want   string // the whole text, or the text up to the operating system's reason
		prefix bool
		is     error
	}{
		"ServerUnreachable": {
			args: []string{"-server", "http://127.0.0.1:1", "setup", "status"},
			want: "cannot reach http://127.0.0.1:1 (", prefix: true,
		},
		"UnknownRole": {
			args: []string{"setup", "init", "-dir", filepath.Join(dir, "d"), "-role", "banana"},
			want: `invalid configuration: setup role "banana" must be customer, vendor or combined`,
		},
		"FileAsPositionalArgument": {
			args: []string{"setup", "adopt", setup},
			want: fmt.Sprintf("usage: setup adopt takes flags only, not %q; the setup document is passed as -setup-file <path>", setup),
		},
		"MissingSecretFile": {
			args: []string{"setup", "adopt", "-setup-file", setup},
			want: "read secret reference DM_BOOTSTRAP_TOKEN: open " + absent + ": ", prefix: true, is: fs.ErrNotExist,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := run(t, noConfig(t), tc.args...)
			if err == nil {
				t.Fatal("no error")
			}
			got := err.Error()
			if (tc.prefix && !strings.HasPrefix(got, tc.want)) || (!tc.prefix && got != tc.want) {
				t.Fatalf("\n got: %s\nwant: %s", got, tc.want)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("cause lost: %v", err)
			}
			for _, noise := range []string{"dmctl:", "app:", "adminclient:", "Get \""} {
				if strings.Contains(got, noise) {
					t.Fatalf("%q survived in: %s", noise, got)
				}
			}
			if tc.prefix && strings.Count(got, "127.0.0.1")+strings.Count(got, absent) > 1 {
				t.Fatalf("an operand is printed twice: %s", got)
			}
		})
	}
}
