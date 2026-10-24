package inventorystore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

//go:embed fences/mysql.sql
var mysqlFenceDDL string

// installMySQLFence recovers partially committed DDL without dropping guards or
// resetting an active model. A connection-scoped advisory lock serializes only
// inventory fence installation across replicas, including concurrent opens.
// Metadata inspection supports the existing MySQL 8.0.19 minimum, which predates
// CREATE TRIGGER IF NOT EXISTS. All schema mutations remain inventory-specific.
func installMySQLFence(ctx context.Context, db *sql.DB) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	discarded := false
	defer func() {
		if !discarded {
			err = errors.Join(err, conn.Close())
		}
	}()
	discard := func() { discarded = true; _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	var acquired int
	const lockName = "SHA2(CONCAT(DATABASE(), '.go-apple-dm.inventory.evidence'), 256)"
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK("+lockName+", 30)").Scan(&acquired); err != nil {
		discard()
		return err
	}
	if acquired != 1 {
		return fmt.Errorf("inventory: schema fence installation lock unavailable")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var released int
		releaseErr := conn.QueryRowContext(cleanup, "SELECT RELEASE_LOCK("+lockName+")").Scan(&released)
		if releaseErr == nil && released != 1 {
			releaseErr = fmt.Errorf("inventory: schema fence installation lock lost")
		}
		if releaseErr != nil {
			discard()
		}
		err = errors.Join(err, releaseErr)
	}()
	checks := []string{
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'inventory_lock' AND column_name = 'evidence_model'",
		"SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = DATABASE() AND trigger_name = 'inventory_evidence_insert'",
		"SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = DATABASE() AND trigger_name = 'inventory_evidence_update'",
	}
	statements := strings.Split(strings.TrimSpace(mysqlFenceDDL), "\n")
	for i, query := range checks {
		var present int
		if err := conn.QueryRowContext(ctx, query).Scan(&present); err != nil {
			return err
		}
		if present == 0 {
			if _, err := conn.ExecContext(ctx, strings.TrimSuffix(statements[i], ";")); err != nil {
				return err
			}
		}
	}
	return nil
}

// fenceMigrationFS binds recovery's existing migration signature to the complete
// installer-owned DDL. The SQL marker remains executable on MySQL 8.0.19, while
// schema comparison detects a changed compiled fence instead of trusting version
// numbers alone. This wrapper affects only inventory's MySQL migration set.
type fenceMigrationFS struct{ fs.FS }

// ReadFile binds the installer DDL digest into the ordinary migration signature.
func (f fenceMigrationFS) ReadFile(name string) ([]byte, error) {
	raw, err := fs.ReadFile(f.FS, name)
	if err != nil {
		return nil, err
	}
	if name == "0002_source_evidence.sql" {
		digest := sha256.Sum256([]byte(mysqlFenceDDL))
		raw = []byte(strings.ReplaceAll(string(raw), "@FENCE_SHA256@", hex.EncodeToString(digest[:])))
	}
	return raw, nil
}
