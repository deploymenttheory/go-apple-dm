package recovery

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func deploymentFixture(t *testing.T) (SQL, BackupOptions, *age.X25519Identity) {
	t.Helper()
	source, b := bootstrapFixture(t)
	s, err := OpenDatabase(t.Context(), "sqlite", b.Environment["DM_DSN"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DB.Close() })
	s = sqlFixture(t, s.DB, sqlite.Dialect)
	control, err := maintenance.Open(t.Context(), s.DB, s.Dialect, false)
	if err != nil {
		t.Fatal(err)
	}
	ticket := rand.Text()
	if err := control.Request(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	return s, BackupOptions{SetupFile: source, Destination: filepath.Join(dir, "backup.age"), StagingParent: dir, Ticket: ticket, Revision: "test", Recipients: []age.Recipient{key.Recipient()}}, key
}

func TestBackupRefusesIncompleteOrUnfencedDeployment(t *testing.T) {
	for _, fault := range []string{"no maintenance", "wrong ticket", "missing parent", "missing setup", "different backend", "unregistered table", "unsealed value"} {
		t.Run(fault, func(t *testing.T) {
			s, o, _ := deploymentFixture(t)
			var query string
			switch fault {
			case "no maintenance":
				query = "DROP TABLE maintenance_state"
			case "wrong ticket":
				o.Ticket = rand.Text()
			case "missing parent":
				o.StagingParent = filepath.Join(t.TempDir(), "missing")
			case "missing setup":
				o.SetupFile = "missing"
			case "different backend":
				o.Overrides = map[string]string{"DM_STORAGE": "mysql"}
			case "unregistered table":
				query = "CREATE TABLE application_extension (value TEXT)"
			case "unsealed value":
				query = "INSERT INTO protocol_state(record_key, value, expires_at) VALUES ('private', x'010203', 0)"
			}
			if query != "" {
				if _, err := s.DB.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Backup(t.Context(), o); err == nil {
				t.Fatal("published incomplete checkpoint")
			}
			if _, err := os.Stat(o.Destination); !os.IsNotExist(err) {
				t.Fatal("published failed backup", err)
			}
			entries, err := os.ReadDir(o.StagingParent)
			if fault != "missing parent" && (err != nil || len(entries) != 0) {
				t.Fatal("leaked plaintext staging", err)
			}
		})
	}
}

// Authentication alone is insufficient: the database and key/configuration
// bindings must also be valid before a deployment can be prepared.
func TestPrepareRejectsAuthenticatedButIncompleteDeployment(t *testing.T) {
	s, o, key := deploymentFixture(t)
	if _, err := s.Backup(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"missing config", "bad config", "version", "backend", "URL", "unsafe URL", "keys", "missing database", "database schema"} {
		t.Run(fault, func(t *testing.T) {
			v, err := Verify(t.Context(), o.Destination, t.TempDir(), []age.Identity{key}, Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer func(cleanup func() error) { _ = cleanup() }(v.Close)
			path := filepath.Join(v.Directory(), "config", "bootstrap.json")
			var b Bootstrap
			if err := readJSONFile(path, &b); err != nil {
				t.Fatal(err)
			}
			metadata := v.Manifest.Metadata
			switch fault {
			case "missing config":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "bad config":
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing database":
				if err := os.Remove(filepath.Join(v.Directory(), "database", "database.json")); err != nil {
					t.Fatal(err)
				}
			case "database schema":
				if err := os.WriteFile(filepath.Join(v.Directory(), "database", "database.json"), []byte(`{"version":99}`), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				switch fault {
				case "version":
					b.Version = 2
				case "backend":
					b.Environment["DM_STORAGE"] = "postgres"
				case "URL":
					b.Environment["DM_PUBLIC_URL"] = "https://other.example.test"
				case "unsafe URL":
					b.Environment["DM_PUBLIC_URL"] = "https://user:secret@mdm.example.test"
					metadata.PublicURL = b.Environment["DM_PUBLIC_URL"]
				case "keys":
					b.Environment["DM_SECRETS_DIR"] = "../outside"
				}
				raw, err := json.Marshal(b)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			archive := filepath.Join(t.TempDir(), "invalid.age")
			if _, err := Create(t.Context(), archive, v.Directory(), metadata, o.Recipients, Limits{}); err != nil {
				t.Fatal(err)
			}
			parent := t.TempDir()
			if p, err := Prepare(t.Context(), archive, parent, []age.Identity{key}, Limits{}); err == nil {
				_ = p.Close()
				t.Fatal("accepted incomplete deployment")
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed prepare retained plaintext", err)
			}
		})
	}
	if _, err := Prepare(t.Context(), "missing", t.TempDir(), []age.Identity{key}, Limits{}); err == nil {
		t.Fatal("prepared missing archive")
	}
}

func TestRestoreRefusesUnsafeTargetsAndUnpausedSnapshots(t *testing.T) {
	s, o, key := deploymentFixture(t)
	if _, err := s.Backup(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	p, err := Prepare(t.Context(), o.Destination, t.TempDir(), []age.Identity{key}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(p.Close)
	if err := p.CheckDatabase(t.Context(), filepath.Join(t.TempDir(), "missing", "verify.sqlite")); err == nil {
		t.Fatal("verified inaccessible target")
	}
	for _, tc := range []struct {
		target, dsn string
		stopped     bool
	}{
		{"", "", true},
		{filepath.Join(t.TempDir(), "live"), "", false},
		{t.TempDir(), "", true},
		{filepath.Join(t.TempDir(), "bad\x00path"), "", true},
		{filepath.Join(t.TempDir(), "missing", "restored"), "", true},
		{filepath.Join(t.TempDir(), "restored"), filepath.Join(t.TempDir(), "missing", "database.sqlite"), true},
		{filepath.Join(t.TempDir(), "occupied"), p.Bootstrap.Environment["DM_DSN"], true},
	} {
		if _, err := p.Restore(t.Context(), tc.target, tc.dsn, tc.stopped); err == nil {
			t.Fatal("restored unsafe target", tc.target)
		}
	}
	p.Manifest.Metadata.Backend = "postgres"
	if _, err := p.Restore(t.Context(), filepath.Join(t.TempDir(), "no-dsn"), "", true); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	p.Manifest.Metadata.Backend = "sqlite"
	// Construct an authenticated checkpoint of an unfenced database; SQL export
	// is public, but activating such a deployment through Restore must fail.
	control, err := maintenance.Open(t.Context(), s.DB, s.Dialect, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Resume(t.Context(), o.Ticket); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	if _, err := CaptureBootstrap(t.Context(), o.SetupFile, filepath.Join(stage, "config"), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Snapshot(t.Context(), filepath.Join(stage, "database")); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "unpaused.age")
	if _, err := Create(t.Context(), archive, stage, Metadata{Backend: "sqlite", PublicURL: p.Manifest.Metadata.PublicURL, Revision: "test", CreatedAt: time.Now()}, o.Recipients, Limits{}); err != nil {
		t.Fatal(err)
	}
	unpaused, err := Prepare(t.Context(), archive, t.TempDir(), []age.Identity{key}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(unpaused.Close)
	dest := filepath.Join(t.TempDir(), "restored")
	if _, err := unpaused.Restore(t.Context(), dest, "", true); !errors.Is(err, ErrInvalid) {
		t.Fatal("activated unpaused checkpoint", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "config", "setup.json")); !os.IsNotExist(err) {
		t.Fatal("published setup despite failed restore", err)
	}
}
