package dmctl

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/recovery"
)

type recoveryFlags struct {
	setup, archive, parent, identity, recipient, ticket, target, verifyDSNEnv, targetDSNEnv, member, revision string
	sourceStopped, processStopped                                                                             bool
	wait                                                                                                      time.Duration
}

func runRecovery(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(
			"%w: recovery needs keygen, pause, status, resume, forget, backup, verify, or restore",
			ErrUsage,
		)
	}
	op := args[0]
	f := recoveryFlags{}
	fs := e.verbFlags("recovery " + op)
	fs.StringVar(&f.setup, "setup-file", e.getenv("DM_SETUP_FILE"), "source or restored setup file")
	fs.StringVar(&f.archive, "archive", "", "encrypted checkpoint file")
	fs.StringVar(
		&f.parent,
		"staging-dir",
		"test-lab/local/certs",
		"existing private parent for verification and staging",
	)
	fs.StringVar(&f.identity, "identity-file", "", "age X25519 identity file; keygen creates it")
	fs.StringVar(&f.recipient, "recipient", "", "age X25519 public recipient")
	fs.StringVar(
		&f.ticket,
		"ticket-file",
		"",
		"persistent maintenance ownership ticket; pause creates it",
	)
	fs.StringVar(
		&f.target,
		"target-dir",
		"",
		"absent directory for restored configuration and identities",
	)
	fs.StringVar(
		&f.verifyDSNEnv,
		"verify-dsn-env",
		"",
		"environment variable for an EMPTY isolated verification database",
	)
	fs.StringVar(
		&f.targetDSNEnv,
		"target-dsn-env",
		"",
		"environment variable for an EMPTY restored database; SQLite defaults inside target-dir",
	)
	fs.StringVar(&f.member, "member", "", "stopped process registration to forget")
	fs.StringVar(&f.revision, "revision", "", "source binary revision recorded in the manifest")
	fs.BoolVar(
		&f.sourceStopped,
		"source-stopped",
		false,
		"assert all source processes are stopped before restoring",
	)
	fs.BoolVar(
		&f.processStopped,
		"process-stopped",
		false,
		"assert the named process is stopped before forgetting it",
	)
	fs.DurationVar(&f.wait, "wait", time.Minute, "maximum time to wait for every process to drain")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("%w: unexpected recovery arguments", ErrUsage)
	}
	if len(args) > 1 && (args[1] == "-h" || args[1] == "--help") {
		return nil
	}
	if op == "keygen" {
		if f.identity == "" {
			return fmt.Errorf("%w: keygen requires -identity-file", ErrUsage)
		}
		key, err := age.GenerateX25519Identity()
		if err != nil {
			return wrapError(err)
		}
		if err := writeNewPrivateFile(f.identity, []byte(key.String()+"\n")); err != nil {
			return err
		}
		return wrapError(
			json.NewEncoder(e.stdout).
				Encode(map[string]string{"recipient": key.Recipient().String()}),
		)
	}
	if op == "verify" || op == "restore" {
		return e.recoveryArchive(ctx, op, f)
	}
	return e.recoveryControl(ctx, op, f)
}

func (e *env) recoveryControl(ctx context.Context, op string, f recoveryFlags) error {
	if op != "pause" && op != "status" && op != "resume" && op != "forget" && op != "backup" {
		return fmt.Errorf("%w: unknown recovery operation", ErrUsage)
	}
	if f.setup == "" {
		return fmt.Errorf("%w: -setup-file required", ErrUsage)
	}
	cfg, err := app.LoadSetupFile(f.setup, e.getenv)
	if err != nil {
		return wrapError(err)
	}
	s, err := recovery.OpenDatabase(ctx, cfg.Storage, cfg.DSN)
	if err != nil {
		return wrapError(err)
	}
	defer s.DB.Close()
	control, err := maintenance.Open(ctx, s.DB, s.Dialect, false)
	if err != nil {
		return wrapError(err)
	}
	if op == "status" {
		status, err := control.Status(ctx)
		if err != nil {
			return wrapError(err)
		}
		return wrapError(json.NewEncoder(e.stdout).Encode(status))
	}
	if f.ticket == "" {
		return fmt.Errorf("%w: -ticket-file required", ErrUsage)
	}
	if op == "pause" {
		if err := writeNewPrivateFile(f.ticket, []byte(rand.Text())); err != nil {
			return err
		}
	}
	ticket, err := readRecoveryIdentityFile(f.ticket)
	if err != nil {
		return err
	}
	switch op {
	case "pause":
		if err := control.Request(ctx, ticket); err != nil {
			return wrapError(err)
		}
		wait, cancel := context.WithTimeout(ctx, f.wait)
		defer cancel()
		if err := control.WaitDrained(wait, ticket); err != nil {
			return fmt.Errorf(
				"dmctl: fence remains paused; inspect recovery status and use this ticket to resume: %w",
				err,
			)
		}
	case "resume":
		if err := control.Resume(ctx, ticket); err != nil {
			return wrapError(err)
		}
	case "forget":
		if err := control.Forget(ctx, ticket, f.member, f.processStopped); err != nil {
			return wrapError(err)
		}
	case "backup":
		return e.recoveryBackup(ctx, s, f, ticket)
	}
	return wrapError(
		json.NewEncoder(e.stdout).Encode(map[string]string{"operation": op, "state": "complete"}),
	)
}

func readRecoveryIdentityFile(path string) (string, error) {
	// #nosec G304 -- Explicit local operator credential path, never remote input.
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", wrapError(err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func (e *env) recoveryArchive(ctx context.Context, op string, f recoveryFlags) error {
	if f.archive == "" || f.identity == "" {
		return fmt.Errorf("%w: -archive and -identity-file required", ErrUsage)
	}
	raw, err := readRecoveryIdentityFile(f.identity)
	if err != nil {
		return err
	}
	identities, err := age.ParseIdentities(strings.NewReader(raw))
	if err != nil {
		return wrapError(err)
	}
	p, err := recovery.Prepare(ctx, f.archive, f.parent, identities, recovery.Limits{})
	if err != nil {
		return wrapError(err)
	}
	defer p.Close()
	if op == "verify" {
		dsn := e.getenv(f.verifyDSNEnv)
		if dsn == "" && p.Manifest.Metadata.Backend == "sqlite" {
			dsn = filepath.Join(p.Directory(), "verify.sqlite")
		}
		if dsn == "" {
			return fmt.Errorf(
				"%w: this backend requires -verify-dsn-env naming an empty isolated database",
				ErrUsage,
			)
		}
		if err := p.CheckDatabase(ctx, dsn); err != nil {
			return wrapError(err)
		}
		return wrapError(json.NewEncoder(e.stdout).Encode(p.Manifest))
	}
	if f.target == "" {
		return fmt.Errorf("%w: restore requires -target-dir", ErrUsage)
	}
	result, err := p.Restore(ctx, f.target, e.getenv(f.targetDSNEnv), f.sourceStopped)
	if err != nil {
		return wrapError(err)
	}
	return wrapError(json.NewEncoder(e.stdout).Encode(result))
}

func (e *env) recoveryBackup(
	ctx context.Context,
	s recovery.SQL,
	f recoveryFlags,
	ticket string,
) error {
	if f.archive == "" || f.recipient == "" || f.revision == "" {
		return fmt.Errorf("%w: backup requires -archive, -recipient and -revision", ErrUsage)
	}
	recipient, err := age.ParseX25519Recipient(f.recipient)
	if err != nil {
		return wrapError(err)
	}
	overrides := map[string]string{}
	// Read only DM_ settings. Values go into the encrypted archive, never output.
	for _, raw := range os.Environ() {
		name, _, _ := strings.Cut(raw, "=")
		if strings.HasPrefix(name, "DM_") {
			overrides[name] = e.getenv(name)
		}
	}
	manifest, err := s.Backup(
		ctx,
		recovery.BackupOptions{
			SetupFile:     f.setup,
			Destination:   f.archive,
			StagingParent: f.parent,
			Ticket:        ticket,
			Revision:      f.revision,
			Overrides:     overrides,
			Recipients:    []age.Recipient{recipient},
		},
	)
	if err != nil {
		return wrapError(err)
	}
	return wrapError(json.NewEncoder(e.stdout).Encode(manifest))
}
