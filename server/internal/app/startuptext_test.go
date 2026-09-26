package app_test

import (
	"errors"
	"io/fs"
	"os"
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
	// A regular file where a directory is expected, and a key too short to use.
	notDir := filepath.Join(dir, "file")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	weak := filepath.Join(dir, "weak")
	if err := os.MkdirAll(weak, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(weak, "k"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		cfg    app.Config
		want   string
		prefix bool  // the operating system's own words follow in parentheses
		is     error // a cause that must stay reachable through the sentence
	}{
		"SecretsPathIsNotADirectory": {
			cfg:    app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "d.db"), StorageKeys: []string{"storage"}, SecretsDir: notDir},
			want:   "the secrets directory " + notDir + " cannot be opened (",
			prefix: true,
		},
		"StorageKeyTooShort": {
			cfg:    app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "e.db"), StorageKeys: []string{"k"}, SecretsDir: weak},
			want:   "the storage keyring cannot be built (",
			prefix: true,
		},
		"SecretsDirectoryMissing": {
			cfg:  app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "a.db"), StorageKeys: []string{"storage"}, SecretsDir: filepath.Join(dir, "nonexistent")},
			want: "the secrets directory " + filepath.Join(dir, "nonexistent") + " does not exist",
			is:   fs.ErrNotExist,
		},
		"StorageKeyNotProvided": {
			cfg:  app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "b.db"), StorageKeys: []string{"k"}, SecretsDir: dir},
			want: `storage key "k" is not in the secrets directory ` + dir,
		},
		"StorageWithoutKeys": {
			cfg:  app.Config{Storage: "sqlite", DSN: filepath.Join(dir, "c.db")},
			want: "sqlite storage needs DM_STORAGE_KEYS, because it seals unlock tokens, bootstrap tokens and push keys",
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
			if (tc.prefix && !strings.HasPrefix(got, tc.want)) || (!tc.prefix && got != tc.want) {
				t.Fatalf("\n got: %s\nwant: %s", got, tc.want)
			}
			if !errors.Is(err, app.ErrConfig) || (tc.is != nil && !errors.Is(err, tc.is)) {
				t.Fatalf("a configuration failure lost its classification: %v", err)
			}
			// Colons belong only to an operand or to the operating system's words in
			// parentheses, never to a chain of layers.
			bare := strings.ReplaceAll(got, dir, "")
			if i := strings.Index(bare, "("); i >= 0 {
				bare = bare[:i]
			}
			if strings.Contains(bare, ":") {
				t.Fatalf("a chain of layers survived: %s", got)
			}
		})
	}
}
