package recovery

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"

	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/mysql"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/postgres"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// OpenDatabase opens a recovery connection without applying migrations. The
// caller owns DB.Close. Use a dedicated database/schema for each deployment.
func OpenDatabase(ctx context.Context, backend, dsn string) (SQL, error) {
	s, err := schemaForBackend(backend)
	if err != nil || dsn == "" {
		return s, ErrInvalid
	}
	driver := backend
	switch backend {
	case "sqlite":
		dsn = sqlite.DSN(dsn, sqlite.Options{})
	case "postgres":
		driver = "pgx"
	case "mysql":
		dsn, err = mysql.NormalizeDSN(dsn)
		if err != nil {
			return s, wrap(err)
		}
	}
	s.DB, err = sql.Open(driver, dsn)
	if err != nil {
		return s, wrap(err)
	}
	if backend == "sqlite" {
		s.DB.SetMaxOpenConns(1)
	}
	if err := s.DB.PingContext(ctx); err != nil {
		_ = s.DB.Close()
		return SQL{}, wrap(err)
	}
	return s, nil
}

func schemaForBackend(backend string) (SQL, error) {
	var d sqlcommon.Dialect
	switch backend {
	case "sqlite":
		d = sqlite.Dialect
	case "postgres":
		d = postgres.Dialect
	case "mysql":
		d = mysql.Dialect
	default:
		return SQL{}, ErrInvalid
	}
	sets, err := ServerSchema(d)
	return SQL{Dialect: d, Schema: sets}, err
}

type BackupOptions struct {
	SetupFile, Destination, StagingParent, Ticket, Revision string
	Overrides                                               map[string]string
	Recipients                                              []age.Recipient
	Limits                                                  Limits
}

// Backup requires a previously requested, fully drained fence. It verifies the
// captured key-to-row bindings before encrypting. The caller retains the fence
// until the archive has been independently verified with a recovery identity.
func (s SQL) Backup(ctx context.Context, o BackupOptions) (Manifest, error) {
	var manifest Manifest
	control, err := maintenance.Open(ctx, s.DB, s.Dialect, false)
	if err != nil {
		return manifest, wrap(err)
	}
	if err := control.WaitDrained(ctx, o.Ticket); err != nil {
		return manifest, wrap(err)
	}
	stage, err := os.MkdirTemp(o.StagingParent, ".recovery-backup-")
	if err != nil {
		return manifest, wrap(err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	b, err := CaptureBootstrap(ctx, o.SetupFile, filepath.Join(stage, "config"), o.Overrides)
	if err != nil {
		return manifest, err
	}
	if b.Environment["DM_STORAGE"] != s.Dialect.Name {
		return manifest, ErrInvalid
	}
	database := filepath.Join(stage, "database")
	if err := s.Snapshot(ctx, database); err != nil {
		return manifest, err
	}
	keys, err := b.Keyring(ctx, filepath.Join(stage, "config"))
	if err != nil {
		return manifest, err
	}
	if err := s.ValidateKeys(ctx, database, keys); err != nil {
		return manifest, err
	}
	status, err := control.Status(ctx)
	if err != nil {
		return manifest, wrap(err)
	}
	if status.Token != o.Ticket || !status.Ready() {
		return manifest, fmt.Errorf("%w: maintenance fence changed during backup", ErrInvalid)
	}
	metadata := Metadata{
		Backend:   s.Dialect.Name,
		PublicURL: b.Environment["DM_PUBLIC_URL"],
		Revision:  o.Revision,
		CreatedAt: time.Now().UTC(),
	}
	return Create(ctx, o.Destination, stage, metadata, o.Recipients, o.Limits)
}

// Prepared authenticates both the encrypted archive and the SQL secret bindings.
// Close removes its isolated plaintext directory. It owns no active server.
type Prepared struct {
	*Verified
	Bootstrap Bootstrap
	SQL       SQL
}

func Prepare(
	ctx context.Context,
	source, parent string,
	identities []age.Identity,
	limits Limits,
) (*Prepared, error) {
	v, err := Verify(ctx, source, parent, identities, limits)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = v.Close()
		}
	}()
	p := &Prepared{Verified: v}
	p.SQL, err = schemaForBackend(v.Manifest.Metadata.Backend)
	if err != nil {
		return nil, err
	}
	config := filepath.Join(v.Directory(), "config")
	if err := readJSONFile(filepath.Join(config, "bootstrap.json"), &p.Bootstrap); err != nil {
		return nil, err
	}
	if p.Bootstrap.Version != 1 ||
		p.Bootstrap.Environment["DM_STORAGE"] != v.Manifest.Metadata.Backend ||
		p.Bootstrap.Environment["DM_PUBLIC_URL"] != v.Manifest.Metadata.PublicURL {
		return nil, ErrInvalid
	}
	if err := p.Bootstrap.validateURL(); err != nil {
		return nil, err
	}
	keys, err := p.Bootstrap.Keyring(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := p.SQL.ValidateKeys(ctx, filepath.Join(v.Directory(), "database"), keys); err != nil {
		return nil, err
	}
	ok = true
	return p, nil
}

// CheckDatabase exercises all schema, row, constraint, and sequence restoration
// against an empty isolated database. It leaves that database paused, never serving.
func (p *Prepared) CheckDatabase(ctx context.Context, dsn string) error {
	s, err := OpenDatabase(ctx, p.Manifest.Metadata.Backend, dsn)
	if err != nil {
		return err
	}
	defer func(cleanup func() error) { _ = cleanup() }(s.DB.Close)
	return s.Restore(ctx, filepath.Join(p.Directory(), "database"))
}

type RestoreResult struct {
	SetupFile  string `json:"setupFile"`
	TicketFile string `json:"ticketFile"`
	PublicURL  string `json:"publicUrl"`
	State      string `json:"state"`
}

// Restore publishes a setup file only after restoring into an absent directory
// and empty database. The original server processes must be stopped before using
// the restored deployment. The restored fence stays closed until explicit Resume.
// PostgreSQL/MySQL DDL failures can leave a partial isolated target; never reuse it.
func (p *Prepared) Restore(
	ctx context.Context,
	destination, dsn string,
	sourceStopped bool,
) (RestoreResult, error) {
	var out RestoreResult
	abs, err := filepath.Abs(destination)
	if err != nil || destination == "" {
		return out, ErrInvalid
	}
	destination = abs
	if !sourceStopped {
		return out, fmt.Errorf("%w: source processes must be stopped before restore", ErrInvalid)
	}
	if _, err := os.Lstat(destination); err == nil {
		return out, ErrOccupied
	} else if !os.IsNotExist(err) {
		return out, wrap(err)
	}
	if dsn == "" && p.Manifest.Metadata.Backend == "sqlite" {
		dsn = filepath.Join(destination, "database.sqlite")
	}
	if dsn == "" {
		return out, ErrInvalid
	}
	if err := p.RestoreFiles(destination); err != nil {
		return out, err
	}
	s, err := OpenDatabase(ctx, p.Manifest.Metadata.Backend, dsn)
	if err != nil {
		return out, err
	}
	defer func(cleanup func() error) { _ = cleanup() }(s.DB.Close)
	if err := s.Restore(ctx, filepath.Join(destination, "database")); err != nil {
		return out, err
	}
	control, err := maintenance.Open(ctx, s.DB, s.Dialect, false)
	if err != nil {
		return out, wrap(err)
	}
	status, err := control.Status(ctx)
	if err != nil {
		return out, wrap(err)
	}
	if !status.Ready() {
		return out, fmt.Errorf("%w: checkpoint was not drained", ErrInvalid)
	}
	for _, member := range status.Members {
		if err := control.Forget(ctx, status.Token, member.ID, true); err != nil {
			return out, wrap(err)
		}
	}
	config := filepath.Join(destination, "config")
	// Re-read the copied document; installation rewrites only the target copy.
	var b Bootstrap
	if err := readJSONFile(filepath.Join(config, "bootstrap.json"), &b); err != nil {
		return out, err
	}
	out.TicketFile = filepath.Join(config, "maintenance-ticket")
	if err := writePrivate(out.TicketFile, []byte(status.Token)); err != nil {
		return out, err
	}
	out.SetupFile, err = b.Install(config, dsn)
	if err != nil {
		return out, err
	}
	out.PublicURL, out.State = p.Manifest.Metadata.PublicURL, "paused"
	return out, nil
}
