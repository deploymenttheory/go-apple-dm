package inventorystore

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

var (
	errFenceIO          = errors.New("schema connection interrupted")
	fenceDriverSequence atomic.Uint64
)

// fenceWire models connection ownership and metadata across interrupted DDL.
// It exercises error cleanup without requiring database administrative outages.
type fenceWire struct {
	calls, failAt    int
	acquire, release int64
	present          [3]bool
	held, closed     bool
}

type fenceDriver struct{ wire *fenceWire }

// Open supplies an isolated schema connection with controlled failure boundaries.
func (d fenceDriver) Open(string) (driver.Conn, error) { return &fenceConnection{d.wire}, nil }

type fenceConnection struct{ wire *fenceWire }

// Prepare rejects an unexpected departure from direct schema statements.
func (c *fenceConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}

// Begin rejects transactions around MySQL implicit-commit DDL.
func (c *fenceConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}

// Close releases connection-owned locks when the installer discards a connection.
func (c *fenceConnection) Close() error { c.wire.closed = true; c.wire.held = false; return nil }

// step injects a failure at one chosen schema wire operation.
func (c *fenceConnection) step() error {
	c.wire.calls++
	if c.wire.calls == c.wire.failAt {
		return errFenceIO
	}
	return nil
}

// QueryContext models schema metadata and connection-scoped lock ownership.
func (c *fenceConnection) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := c.step(); err != nil {
		return nil, err
	}
	value := int64(0)
	switch {
	case strings.Contains(q, "GET_LOCK"):
		value = c.wire.acquire
		c.wire.held = value == 1
	case strings.Contains(q, "RELEASE_LOCK"):
		value = c.wire.release
		if value == 1 {
			c.wire.held = false
		}
	default:
		i := 0
		if strings.Contains(q, "inventory_evidence_insert") {
			i = 1
		}
		if strings.Contains(q, "inventory_evidence_update") {
			i = 2
		}
		if c.wire.present[i] {
			value = 1
		}
	}
	return &fenceRows{value: value}, nil
}

// ExecContext retains installed DDL metadata across a subsequent retry.
func (c *fenceConnection) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if err := c.step(); err != nil {
		return nil, err
	}
	i := 0
	if strings.Contains(q, "inventory_evidence_insert") {
		i = 1
	}
	if strings.Contains(q, "inventory_evidence_update") {
		i = 2
	}
	c.wire.present[i] = true
	return driver.RowsAffected(1), nil
}

type fenceRows struct {
	value int64
	done  bool
}

// Columns exposes the single scalar metadata or lock result.
func (*fenceRows) Columns() []string { return []string{"value"} }

// Close ends the fixture cursor without retaining connection ownership.
func (*fenceRows) Close() error { return nil }

// Next emits one scalar result followed by the end of the cursor.
func (r *fenceRows) Next(out []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	out[0] = r.value
	return nil
}

// fencePool isolates driver registration and closes every fixture pool.
func fencePool(t *testing.T, wire *fenceWire) *sql.DB {
	t.Helper()
	name := "inventory-fence-" + fmt.Sprint(fenceDriverSequence.Add(1))
	sql.Register(name, fenceDriver{wire})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

// TestFenceInstallationFailureCleanup verifies that every metadata/DDL failure
// propagates, releases ownership and permits a safe retry of committed statements.
func TestFenceInstallationFailureCleanup(t *testing.T) {
	baseline := &fenceWire{acquire: 1, release: 1}
	if err := installMySQLFence(t.Context(), fencePool(t, baseline)); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= baseline.calls; n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			wire := &fenceWire{acquire: 1, release: 1, failAt: n}
			db := fencePool(t, wire)
			if err := installMySQLFence(t.Context(), db); !errors.Is(err, errFenceIO) {
				t.Fatal("installation failure hidden", err)
			}
			if wire.held {
				t.Fatal("installation lock retained after failure")
			}
			wire.failAt = 0
			if err := installMySQLFence(t.Context(), db); err != nil {
				t.Fatal("partial DDL retry", err)
			}
			if wire.present != [3]bool{true, true, true} || wire.held {
				t.Fatal("incomplete retry or retained lock", wire)
			}
		})
	}
	for _, scenario := range []struct {
		name             string
		acquire, release int64
	}{{"lock unavailable", 0, 1}, {"ownership lost", 1, 0}} {
		t.Run(scenario.name, func(t *testing.T) {
			wire := &fenceWire{acquire: scenario.acquire, release: scenario.release}
			db := fencePool(t, wire)
			if err := installMySQLFence(t.Context(), db); err == nil {
				t.Fatal("lost schema lock reported success")
			}
			if wire.held {
				t.Fatal("unreleasable connection returned to pool")
			}
		})
	}
	db := fencePool(t, &fenceWire{acquire: 1, release: 1})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := installMySQLFence(t.Context(), db); err == nil {
		t.Fatal("closed pool accepted")
	}
}

// TestFenceSchemaSignature includes installer DDL in recovery's existing compiled
// migration fingerprint, rather than accepting a matching numeric version alone.
func TestFenceSchemaSignature(t *testing.T) {
	files := fenceMigrationFS{fstest.MapFS{"0002_source_evidence.sql": &fstest.MapFile{Data: []byte("SELECT '@FENCE_SHA256@';")}, "other.sql": &fstest.MapFile{Data: []byte("SELECT 1;")}}}
	first, err := files.ReadFile("0002_source_evidence.sql")
	if err != nil || bytes.Contains(first, []byte("@FENCE_SHA256@")) {
		t.Fatal(string(first), err)
	}
	original := mysqlFenceDDL
	defer func() { mysqlFenceDDL = original }()
	mysqlFenceDDL += "\nchanged fence"
	changed, err := files.ReadFile("0002_source_evidence.sql")
	if err != nil || bytes.Equal(first, changed) {
		t.Fatal("fence change missing from compiled signature", err)
	}
	other, err := files.ReadFile("other.sql")
	if err != nil || string(other) != "SELECT 1;" {
		t.Fatal(string(other), err)
	}
	if _, err := files.ReadFile("missing.sql"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}
