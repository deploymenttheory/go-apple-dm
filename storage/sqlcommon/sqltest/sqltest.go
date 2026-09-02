// Package sqltest holds helpers for SQL backend tests and benchmarks that
// need large fixtures written faster than the storage API allows. It is
// not part of the storage contract.
package sqltest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/storage/sqlcommon"
)

// seedBatch rows per INSERT keeps the parameter count well under
// PostgreSQL's 65,535 limit.
const seedBatch = 500

// Seed inserts n pending ProfileList commands for the enrollment id using
// multi-row INSERTs. The enrollment row must already exist.
func Seed(ctx context.Context, tb testing.TB, db *sql.DB, d sqlcommon.Dialect, id string, n int) {
	tb.Helper()
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	const cols = "(enrollment_id, command_uuid, request_type, raw, dedupe_key, state, enqueued_at, attempts, not_now_count)"
	for start := 0; start < n; start += seedBatch {
		rows := min(seedBatch, n-start)
		stmt := "INSERT INTO commands " + cols + " VALUES " + strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?, ?, ?, ?, 0, 0), ", rows), ", ")
		args := make([]any, 0, rows*7)
		for i := start; i < start+rows; i++ {
			args = append(args, id, fmt.Sprintf("U%07d", i), "ProfileList", []byte("<plist/>"), "", "pending", t0.Add(time.Duration(i)*time.Millisecond))
		}
		if _, err := db.ExecContext(ctx, d.Rebind(stmt), args...); err != nil {
			tb.Fatalf("seed commands: %v", err)
		}
	}
}
