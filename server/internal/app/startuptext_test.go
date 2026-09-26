package app_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestStartupFailuresReadAsOneSentence pins the text an operator sees when dmserver
// refuses to start: the setting at fault and the reason, once, with no package labels
// and no operand repeated by every layer it passed through.
func TestStartupFailuresReadAsOneSentence(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		cfg  app.Config
		want string
	}{
		"SecretsDirectoryMissing": {
			app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "a.db"), StorageKeys: []string{"storage"}, SecretsDir: filepath.Join(dir, "nonexistent")},
			// The operating system's wording for a missing directory follows; only
			// the operation and the path, once, are pinned.
			"secrets directory: open " + filepath.Join(dir, "nonexistent") + ": ",
		},
		"StorageKeyNotProvided": {
			app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "b.db"), StorageKeys: []string{"k"}, SecretsDir: dir},
			`storage keyring: secret not found: key "k" is not in the secrets provider`,
		},
		"StorageWithoutKeys": {
			app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "c.db")},
			"invalid configuration: sqlite storage seals unlock tokens, bootstrap tokens and push keys, so it needs DM_STORAGE_KEYS",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tc.cfg.Logger = quiet
			_, err := app.Build(t.Context(), tc.cfg)
			if err == nil {
				t.Fatal("built")
			}
			got := err.Error()
			if strings.HasSuffix(tc.want, ": ") {
				if !strings.HasPrefix(got, tc.want) || strings.Count(got, tc.cfg.SecretsDir) != 1 {
					t.Fatalf("\n got: %s\nwant prefix: %s", got, tc.want)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}
