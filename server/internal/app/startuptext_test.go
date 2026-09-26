package app_test

import (
	"errors"
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
			"the secrets directory " + filepath.Join(dir, "nonexistent") + " does not exist",
		},
		"StorageKeyNotProvided": {
			app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "b.db"), StorageKeys: []string{"k"}, SecretsDir: dir},
			`storage key "k" is not in the secrets directory ` + dir,
		},
		"StorageWithoutKeys": {
			app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "c.db")},
			"sqlite storage needs DM_STORAGE_KEYS, because it seals unlock tokens, bootstrap tokens and push keys",
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
			if got != tc.want {
				t.Fatalf("\n got: %s\nwant: %s", got, tc.want)
			}
			if !errors.Is(err, app.ErrConfig) {
				t.Fatal("a configuration failure lost ErrConfig")
			}
			if strings.Contains(strings.ReplaceAll(got, dir, ""), ":") {
				t.Fatalf("a chain of layers survived: %s", got)
			}
		})
	}
}
