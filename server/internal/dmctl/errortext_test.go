package dmctl_test

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
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
	// A port that was just listening and is now closed refuses a connection at once on
	// every platform; a well-known low port may be silently dropped by a packet filter.
	closed, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + closed.Addr().String()
	_ = closed.Close()
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
			args: []string{"-server", origin, "setup", "status"},
			want: "cannot reach " + origin + " (", prefix: true,
		},
		"UnknownRole": {
			args: []string{"setup", "init", "-dir", filepath.Join(dir, "d"), "-role", "banana"},
			want: `setup role "banana" is not valid; use customer, vendor or combined`,
		},
		"FileAsPositionalArgument": {
			args: []string{"setup", "adopt", setup},
			want: fmt.Sprintf("setup adopt takes flags only, not %q; pass the setup document as -setup-file <path>", setup),
		},
		"MissingSecretFile": {
			args: []string{"setup", "adopt", "-setup-file", setup},
			want: "the secret file " + absent + " for DM_BOOTSTRAP_TOKEN does not exist", is: fs.ErrNotExist,
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
			if strings.Count(got, "127.0.0.1")+strings.Count(got, absent) > 1 {
				t.Fatalf("an operand is printed twice: %s", got)
			}
			// One sentence: colons appear only inside a quoted operand, never as the joints
			// of a chain of layers.
			if strings.Count(strings.ReplaceAll(got, origin, ""), ":") > 0 {
				t.Fatalf("a chain of layers survived: %s", got)
			}
		})
	}
}
