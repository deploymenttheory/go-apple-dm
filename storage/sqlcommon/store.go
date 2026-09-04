package sqlcommon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/storage"
	"github.com/deploymenttheory/go-apple-dm/storage/crypt"
)

// ClearBatchSize bounds one Clear statement so a large queue never holds
// a long lock (NanoMDM #260). 5,000 keeps a batch well under 100ms on
// PostgreSQL while clearing 100k rows in under a second.
const ClearBatchSize = 5000

// Store implements storage.Store over a *sql.DB.
type Store struct {
	db      *sql.DB
	d       Dialect
	keyring *crypt.Keyring // nil: secret columns stay plaintext
}

var _ storage.Store = (*Store)(nil)

// New wraps an opened database. Call Migrate first.
func New(db *sql.DB, d Dialect, opts ...Option) *Store {
	s := &Store{db: db, d: d}
	for _, o := range opts {
		o(s)
	}
	return s
}

// DB exposes the connection pool for backups, health checks, and tests.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the pool.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("sqlcommon: close: %w", err)
	}
	return nil
}

// Ping checks connectivity.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("sqlcommon: ping %s: %w", s.d.Name, err)
	}
	return nil
}

func (s *Store) q(query string) string { return s.d.Rebind(query) }

func wrap(op string, err error) error { return fmt.Errorf("sqlcommon: %s: %w", op, err) }

// querier is *sql.DB or *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) tx(ctx context.Context, fn func(q querier) error) error {
	return runInTx(ctx, s.db, func(tx *sql.Tx) error { return fn(tx) })
}

func validID(id mdm.EnrollmentID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %w", storage.ErrInvalid, err)
	}
	return nil
}

// exists reports whether the enrollment row is present.
func (s *Store) exists(ctx context.Context, q querier, id string) error {
	var one int
	err := q.QueryRowContext(ctx, s.q("SELECT 1 FROM enrollments WHERE id = ?"), id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: enrollment %s", storage.ErrNotFound, id)
	}
	if err != nil {
		return wrap("lookup enrollment", err)
	}
	return nil
}

func nullTime(t time.Time) sql.NullTime { return sql.NullTime{Time: t.UTC(), Valid: !t.IsZero()} }

func fromNull(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// enrollmentCols is the upsert column list; the value list in
// UpsertAuthenticate and the scan in scanEnrollment are positional and
// must be kept in the same order.
var enrollmentCols = []string{
	"id", "channel", "parent_id", "enabled", "topic", "push_magic", "push_token",
	"serial_number", "model", "model_name", "device_name", "product_name", "os_version", "build_version", "imei", "meid", "device_topic",
	"user_short_name", "user_long_name", "not_on_console", "enrollment_user_id", "unlock_token", "authenticate_raw", "token_update_raw", "cert_hash", "cert_hash_at", "bootstrap_token", "bootstrap_token_at",
	"enrolled_at", "token_updated_at", "last_seen_at", "disabled_at",
}

const selectEnrollment = "SELECT id, channel, parent_id, enabled, topic, push_magic, push_token, " +
	"serial_number, model, model_name, device_name, product_name, os_version, build_version, imei, meid, device_topic, " +
	"user_short_name, user_long_name, not_on_console, enrollment_user_id, unlock_token, authenticate_raw, token_update_raw, cert_hash, cert_hash_at, bootstrap_token_at, " +
	"enrolled_at, token_updated_at, last_seen_at, disabled_at FROM enrollments"

type scanner interface{ Scan(dest ...any) error }

func scanEnrollment(row scanner) (*storage.Enrollment, error) {
	var (
		e                                                   storage.Enrollment
		channel                                             int
		certHash                                            sql.NullString
		certHashAt, bootstrapAt, tokenUpdatedAt, disabledAt sql.NullTime
		enrolledAt, lastSeenAt                              time.Time
		pushToken, unlockToken, authenticate, tokenUpdate   []byte
	)
	err := row.Scan(&e.ID.ID, &channel, &e.ID.ParentID, &e.Enabled, &e.Push.Topic, &e.Push.Magic, &pushToken,
		&e.Device.SerialNumber, &e.Device.Model, &e.Device.ModelName, &e.Device.DeviceName, &e.Device.ProductName,
		&e.Device.OSVersion, &e.Device.BuildVersion, &e.Device.IMEI, &e.Device.MEID, &e.Device.Topic,
		&e.UserShortName, &e.UserLongName, &e.NotOnConsole, &e.EnrollmentUserID, &unlockToken, &authenticate, &tokenUpdate, &certHash, &certHashAt, &bootstrapAt,
		&enrolledAt, &tokenUpdatedAt, &lastSeenAt, &disabledAt)
	if err != nil {
		return nil, err
	}
	e.ID.Channel = mdm.Channel(channel) // #nosec G115 -- stored from a uint8
	e.Push.Token = append([]byte(nil), pushToken...)
	e.UnlockToken = append([]byte(nil), unlockToken...)
	e.AuthenticateRaw = append([]byte(nil), authenticate...)
	e.TokenUpdateRaw = append([]byte(nil), tokenUpdate...)
	e.CertHash = certHash.String
	e.CertHashAt, e.BootstrapTokenAt = fromNull(certHashAt), fromNull(bootstrapAt)
	e.EnrolledAt, e.LastSeenAt = enrolledAt.UTC(), lastSeenAt.UTC()
	e.TokenUpdatedAt, e.DisabledAt = fromNull(tokenUpdatedAt), fromNull(disabledAt)
	return &e, nil
}

// UpsertAuthenticate implements storage.EnrollmentStore.
func (s *Store) UpsertAuthenticate(ctx context.Context, id mdm.EnrollmentID, msg *checkin.Authenticate, raw []byte, at time.Time) error {
	if err := validID(id); err != nil {
		return err
	}
	d := storage.DeviceInfoFromAuthenticate(msg)
	at = at.UTC()
	return s.tx(ctx, func(q querier) error {
		// Reset everything the previous identity owned.
		_, err := q.ExecContext(ctx, s.q(s.d.Upsert("enrollments", enrollmentCols, []string{"id"})),
			id.ID, int(id.Channel), id.ParentID, false, "", "", nil,
			d.SerialNumber, d.Model, d.ModelName, d.DeviceName, d.ProductName, d.OSVersion, d.BuildVersion, d.IMEI, d.MEID, d.Topic,
			"", "", false, "", nil, nullBytes(raw), nil, nil, nil, nil, nil,
			at, nil, at, nil)
		if err != nil {
			return wrap("upsert enrollment", err)
		}
		if _, err := q.ExecContext(ctx, s.q("UPDATE commands SET state = ?, completed_at = ? WHERE enrollment_id = ? AND state IN ("+placeholders(3)+")"),
			storage.StateCleared, at, id.ID, storage.StatePending, storage.StateSent, storage.StateNotNow); err != nil {
			return wrap("clear queue", err)
		}
		if !id.Channel.IsUser() {
			// User channels of this device are stale once it re-enrolls, and
			// so are their UserAuthenticate sessions.
			if _, err := q.ExecContext(ctx, s.q("DELETE FROM user_auth WHERE parent_id = ?"), id.ID); err != nil {
				return wrap("clear user auth", err)
			}
			return s.disableChildren(ctx, q, id.ID, at)
		}
		return nil
	})
}

// disableChildren disables every enabled user channel of a device.
func (s *Store) disableChildren(ctx context.Context, q querier, deviceID string, at time.Time) error {
	if _, err := q.ExecContext(ctx, s.q("UPDATE enrollments SET enabled = ?, disabled_at = ? WHERE parent_id = ? AND enabled = ?"),
		false, at.UTC(), deviceID, true); err != nil {
		return wrap("disable user channels", err)
	}
	return nil
}

// StoreTokenUpdate implements storage.EnrollmentStore.
func (s *Store) StoreTokenUpdate(ctx context.Context, id mdm.EnrollmentID, push mdm.Push, msg *checkin.TokenUpdate, raw []byte, at time.Time) error {
	if !push.Valid() {
		return fmt.Errorf("%w: incomplete push info", storage.ErrInvalid)
	}
	if err := validID(id); err != nil {
		return err
	}
	var unlock []byte
	var short, long, enrollmentUser sql.NullString
	var notOnConsole sql.NullBool
	if msg != nil {
		var err error
		if unlock, err = s.seal(purposeUnlockToken, id.ID, msg.UnlockToken); err != nil {
			return err
		}
		if msg.UserShortName != nil {
			short = sql.NullString{String: *msg.UserShortName, Valid: true}
		}
		long = nullString(msg.UserLongName)
		enrollmentUser = nullString(msg.EnrollmentUserID)
		notOnConsole = sql.NullBool{Bool: msg.NotOnConsole, Valid: true}
	}
	res, err := s.db.ExecContext(ctx, s.q("UPDATE enrollments SET topic = ?, push_magic = ?, push_token = ?, enabled = ?, "+
		"token_updated_at = ?, last_seen_at = ?, disabled_at = NULL, token_update_raw = COALESCE(?, token_update_raw), "+
		"unlock_token = COALESCE(?, unlock_token), user_short_name = COALESCE(?, user_short_name), user_long_name = COALESCE(?, user_long_name), "+
		"not_on_console = COALESCE(?, not_on_console), enrollment_user_id = COALESCE(?, enrollment_user_id) "+
		"WHERE id = ?"),
		push.Topic, push.Magic, push.Token, true, at.UTC(), at.UTC(), nullBytes(raw), unlock, short, long, notOnConsole, enrollmentUser, id.ID)
	if err != nil {
		return wrap("token update", err)
	}
	return notFoundIfNoRows(res, id.ID)
}

// notFoundIfNoRows turns an UPDATE that matched no row into ErrNotFound.
// Every dialect must report matched rows, not changed rows (MySQL needs
// clientFoundRows), or an idempotent write would look like a missing row.
func notFoundIfNoRows(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return wrap("rows affected", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: enrollment %s", storage.ErrNotFound, id)
	}
	return nil
}

// Disable implements storage.EnrollmentStore. Disabling a device channel
// also disables its user channels, in one transaction.
func (s *Store) Disable(ctx context.Context, id mdm.EnrollmentID, at time.Time) error {
	if err := validID(id); err != nil {
		return err
	}
	at = at.UTC()
	return s.tx(ctx, func(q querier) error {
		res, err := q.ExecContext(ctx, s.q("UPDATE enrollments SET enabled = ?, disabled_at = ? WHERE id = ?"), false, at, id.ID)
		if err != nil {
			return wrap("disable", err)
		}
		if err := notFoundIfNoRows(res, id.ID); err != nil {
			return err
		}
		if !id.Channel.IsUser() {
			return s.disableChildren(ctx, q, id.ID, at)
		}
		return nil
	})
}

// Get implements storage.EnrollmentStore.
func (s *Store) Get(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	e, err := scanEnrollment(s.db.QueryRowContext(ctx, s.q(selectEnrollment+" WHERE id = ?"), id.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: enrollment %s", storage.ErrNotFound, id.ID)
	}
	if err != nil {
		return nil, wrap("get enrollment", err)
	}
	if err := s.openEnrollment(e); err != nil {
		return nil, err
	}
	return e, nil
}

// openEnrollment decrypts the sealed columns of a scanned record.
func (s *Store) openEnrollment(e *storage.Enrollment) error {
	unlock, err := s.open(purposeUnlockToken, e.ID.ID, e.UnlockToken)
	if err != nil {
		return err
	}
	e.UnlockToken = unlock
	return nil
}

// List implements storage.EnrollmentStore with a keyset cursor on id.
func (s *Store) List(ctx context.Context, q storage.EnrollmentQuery, p paging.Page) (paging.Result[storage.Enrollment], error) {
	var out paging.Result[storage.Enrollment]
	where := []string{"1 = 1"}
	var args []any
	if q.Channel != mdm.ChannelUnknown {
		where = append(where, "channel = ?")
		args = append(args, int(q.Channel))
	}
	if q.Enabled != nil {
		where = append(where, "enabled = ?")
		args = append(args, *q.Enabled)
	}
	if q.ParentID != "" {
		where = append(where, "parent_id = ?")
		args = append(args, q.ParentID)
	}
	if q.Serial != "" {
		where = append(where, "serial_number = ?")
		args = append(args, q.Serial)
	}
	if p.Cursor != "" {
		where = append(where, "id > ?")
		args = append(args, p.Cursor)
	}
	limit := pageLimit(p)
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, s.q(selectEnrollment+" WHERE "+strings.Join(where, " AND ")+" ORDER BY id LIMIT ?"), args...)
	if err != nil {
		return out, wrap("list enrollments", err)
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEnrollment(rows)
		if err != nil {
			return out, wrap("scan enrollment", err)
		}
		if err := s.openEnrollment(e); err != nil {
			return out, err
		}
		out.Items = append(out.Items, *e)
	}
	if err := rows.Err(); err != nil {
		return out, wrap("list enrollments", err)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].ID.ID
	}
	return out, nil
}

func pageLimit(p paging.Page) int {
	return p.Size()
}

// TouchLastSeen implements storage.EnrollmentStore; it never moves the
// timestamp backwards.
func (s *Store) TouchLastSeen(ctx context.Context, id mdm.EnrollmentID, at time.Time) error {
	if err := validID(id); err != nil {
		return err
	}
	at = at.UTC()
	res, err := s.db.ExecContext(ctx, s.q("UPDATE enrollments SET last_seen_at = CASE WHEN last_seen_at < ? THEN ? ELSE last_seen_at END WHERE id = ?"), at, at, id.ID)
	if err != nil {
		return wrap("touch last seen", err)
	}
	return notFoundIfNoRows(res, id.ID)
}

var nonTerminal = []any{storage.StatePending, storage.StateSent, storage.StateNotNow}

// Enqueue implements storage.CommandQueue.
func (s *Store) Enqueue(ctx context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command, o storage.EnqueueOptions) (storage.EnqueueResult, error) {
	res := storage.EnqueueResult{Skipped: map[mdm.EnrollmentID]error{}}
	if cmd == nil || cmd.UUID == "" || cmd.RequestType == "" {
		return storage.EnqueueResult{}, fmt.Errorf("%w: command needs a UUID and RequestType", storage.ErrInvalid)
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	err := s.tx(ctx, func(q querier) error {
		for _, id := range ids {
			if err := validID(id); err != nil {
				res.Skipped[id] = err
				continue
			}
			var enabled bool
			err := q.QueryRowContext(ctx, s.q("SELECT enabled FROM enrollments WHERE id = ?"), id.ID).Scan(&enabled)
			if errors.Is(err, sql.ErrNoRows) {
				res.Skipped[id] = fmt.Errorf("%w: enrollment %s", storage.ErrNotFound, id.ID)
				continue
			}
			if err != nil {
				return wrap("lookup enrollment", err)
			}
			if !enabled {
				res.Skipped[id] = fmt.Errorf("%w: %s", storage.ErrDisabled, id.ID)
				continue
			}
			var one int
			err = q.QueryRowContext(ctx, s.q("SELECT 1 FROM commands WHERE enrollment_id = ? AND command_uuid = ?"), id.ID, cmd.UUID).Scan(&one)
			if err == nil {
				res.Skipped[id] = fmt.Errorf("%w: command %s already queued for %s", storage.ErrConflict, cmd.UUID, id.ID)
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return wrap("lookup command", err)
			}
			if o.DedupeKey != "" {
				err = q.QueryRowContext(ctx, s.q("SELECT 1 FROM commands WHERE enrollment_id = ? AND dedupe_key = ? AND state IN ("+placeholders(3)+") LIMIT 1"),
					append([]any{id.ID, o.DedupeKey}, nonTerminal...)...).Scan(&one)
				if err == nil {
					res.Skipped[id] = fmt.Errorf("%w: pending command with dedupe key %q", storage.ErrConflict, o.DedupeKey)
					continue
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return wrap("lookup dedupe key", err)
				}
			}
			if _, err := q.ExecContext(ctx, s.q("INSERT INTO commands (enrollment_id, command_uuid, request_type, raw, dedupe_key, state, enqueued_at, attempts, not_now_count) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0)"),
				id.ID, cmd.UUID, cmd.RequestType, nullBytes(cmd.Raw), o.DedupeKey, storage.StatePending, now); err != nil {
				return wrap("insert command", err)
			}
			res.Queued = append(res.Queued, id)
		}
		return nil
	})
	if err != nil {
		return storage.EnqueueResult{}, err
	}
	return res, nil
}

// Next implements storage.CommandQueue.
func (s *Store) Next(ctx context.Context, id mdm.EnrollmentID, skipNotNow bool, now time.Time) (*mdm.Command, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	now = now.UTC()
	var out *mdm.Command
	err := s.tx(ctx, func(q querier) error {
		if err := s.exists(ctx, q, id.ID); err != nil {
			return err
		}
		// Every state term is an equality on the (enrollment_id, state, seq)
		// index, so the planner reads at most three short ranges and merges
		// them by seq; not_now_until is the only residual filter.
		where := "enrollment_id = ? AND state IN (?, ?, ?) AND (state <> ? OR not_now_until <= ?)"
		args := []any{id.ID, storage.StatePending, storage.StateSent, storage.StateNotNow, storage.StateNotNow, now}
		if skipNotNow {
			where = "enrollment_id = ? AND state IN (?, ?)"
			args = []any{id.ID, storage.StatePending, storage.StateSent}
		}
		var (
			seq               int64
			uuid, requestType string
			raw               []byte
		)
		err := q.QueryRowContext(ctx, s.q("SELECT seq, command_uuid, request_type, raw FROM commands WHERE "+where+" ORDER BY seq LIMIT 1 "+s.d.ForUpdate), args...).
			Scan(&seq, &uuid, &requestType, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return wrap("next command", err)
		}
		if _, err := q.ExecContext(ctx, s.q("UPDATE commands SET state = ?, last_sent_at = ?, attempts = attempts + 1 WHERE seq = ?"), storage.StateSent, now, seq); err != nil {
			return wrap("mark sent", err)
		}
		out = &mdm.Command{UUID: uuid, RequestType: requestType, Raw: append([]byte(nil), raw...)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StoreResult implements storage.CommandQueue.
func (s *Store) StoreResult(ctx context.Context, id mdm.EnrollmentID, resp *mdm.Response, now time.Time) error {
	if resp == nil || resp.IsIdle() || resp.CommandUUID == "" {
		return fmt.Errorf("%w: result needs a CommandUUID and a non-Idle status", storage.ErrInvalid)
	}
	if err := validID(id); err != nil {
		return err
	}
	now = now.UTC()
	chain, err := json.Marshal(resp.ErrorChain)
	if err != nil {
		return wrap("encode error chain", err)
	}
	return s.tx(ctx, func(q querier) error {
		if err := s.exists(ctx, q, id.ID); err != nil {
			return err
		}
		var seq int64
		var notNowCount int
		err := q.QueryRowContext(ctx, s.q("SELECT seq, not_now_count FROM commands WHERE enrollment_id = ? AND command_uuid = ? AND state IN ("+placeholders(3)+") "+s.d.ForUpdate),
			append([]any{id.ID, resp.CommandUUID}, nonTerminal...)...).Scan(&seq, &notNowCount)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: no open command %s", storage.ErrNotFound, resp.CommandUUID)
		}
		if err != nil {
			return wrap("lookup open command", err)
		}
		state, completed, until := storage.StateError, nullTime(now), sql.NullTime{}
		switch resp.Status {
		case mdm.StatusAcknowledged:
			state = storage.StateAcknowledged
		case mdm.StatusNotNow:
			notNowCount++
			state, completed = storage.StateNotNow, sql.NullTime{}
			until = nullTime(now.Add(storage.NotNowBackoff(notNowCount)))
		default:
			// Error and CommandFormatError are terminal; the result is kept.
		}
		if _, err := q.ExecContext(ctx, s.q("UPDATE commands SET state = ?, completed_at = ?, not_now_until = ?, not_now_count = ?, "+
			"result_status = ?, result_raw = ?, result_error_chain = ? WHERE seq = ?"),
			state, completed, until, notNowCount, string(resp.Status), nullBytes(resp.Raw), string(chain), seq); err != nil {
			return wrap("store result", err)
		}
		return nil
	})
}

const selectCommand = "SELECT seq, command_uuid, request_type, raw, dedupe_key, state, enqueued_at, last_sent_at, not_now_until, " +
	"attempts, not_now_count, completed_at, result_status, result_raw, result_error_chain FROM commands"

func scanCommand(row scanner, id mdm.EnrollmentID) (int64, storage.QueuedCommand, error) {
	var (
		seq                              int64
		c                                storage.QueuedCommand
		raw, resultRaw                   []byte
		lastSent, notNowUntil, completed sql.NullTime
		resultStatus, resultChain        sql.NullString
		state                            string
		enqueued                         time.Time
	)
	err := row.Scan(&seq, &c.Command.UUID, &c.Command.RequestType, &raw, &c.DedupeKey, &state, &enqueued, &lastSent, &notNowUntil,
		&c.Attempts, &c.NotNowCount, &completed, &resultStatus, &resultRaw, &resultChain)
	if err != nil {
		return 0, c, err
	}
	c.Command.Raw = append([]byte(nil), raw...)
	c.State = storage.State(state)
	c.EnqueuedAt = enqueued.UTC()
	c.LastSentAt, c.NotNowUntil, c.CompletedAt = fromNull(lastSent), fromNull(notNowUntil), fromNull(completed)
	if resultStatus.Valid {
		r := &mdm.Response{ID: id, CommandUUID: c.Command.UUID, Status: mdm.Status(resultStatus.String), Raw: append([]byte(nil), resultRaw...)}
		if resultChain.Valid && resultChain.String != "" && resultChain.String != "null" {
			if err := json.Unmarshal([]byte(resultChain.String), &r.ErrorChain); err != nil {
				return 0, c, fmt.Errorf("decode error chain: %w", err)
			}
		}
		c.Result = r
	}
	return seq, c, nil
}

// Commands implements storage.CommandQueue with a keyset cursor on the
// sequence number, newest first.
func (s *Store) Commands(ctx context.Context, id mdm.EnrollmentID, q storage.CommandQuery, p paging.Page) (paging.Result[storage.QueuedCommand], error) {
	var out paging.Result[storage.QueuedCommand]
	if err := validID(id); err != nil {
		return out, err
	}
	// Nothing deletes enrollment rows, so this read-only check cannot race
	// with the query below; it only distinguishes ErrNotFound from an empty
	// queue.
	if err := s.exists(ctx, s.db, id.ID); err != nil {
		return out, err
	}
	where := []string{"enrollment_id = ?"}
	args := []any{id.ID}
	if q.RequestType != "" {
		where = append(where, "request_type = ?")
		args = append(args, q.RequestType)
	}
	if len(q.States) > 0 {
		where = append(where, "state IN ("+placeholders(len(q.States))+")")
		for _, st := range q.States {
			args = append(args, st)
		}
	}
	if p.Cursor != "" {
		n, err := strconv.ParseInt(p.Cursor, 10, 64)
		if err != nil || n < 0 {
			return out, fmt.Errorf("%w: bad cursor %q", storage.ErrInvalid, p.Cursor)
		}
		where = append(where, "seq < ?")
		args = append(args, n)
	}
	limit := pageLimit(p)
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, s.q(selectCommand+" WHERE "+strings.Join(where, " AND ")+" ORDER BY seq DESC LIMIT ?"), args...)
	if err != nil {
		return out, wrap("list commands", err)
	}
	defer rows.Close()
	seqs := make([]int64, 0, limit+1)
	for rows.Next() {
		seq, c, err := scanCommand(rows, id)
		if err != nil {
			return out, wrap("scan command", err)
		}
		out.Items = append(out.Items, c)
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		return out, wrap("list commands", err)
	}
	if len(out.Items) > limit {
		// The cursor is the sequence of the last item kept.
		out.Items = out.Items[:limit]
		out.NextCursor = strconv.FormatInt(seqs[limit-1], 10)
	}
	return out, nil
}

// Clear implements storage.CommandQueue in indexed batches of
// ClearBatchSize rows. Each batch is its own statement, so a failure part
// way through returns the count applied so far; callers may simply retry.
func (s *Store) Clear(ctx context.Context, id mdm.EnrollmentID, f storage.ClearFilter) (int64, error) {
	if err := validID(id); err != nil {
		return 0, err
	}
	// Read-only check; enrollment rows are never deleted (see Commands).
	if err := s.exists(ctx, s.db, id.ID); err != nil {
		return 0, err
	}
	states := f.States
	if len(states) == 0 {
		states = []storage.State{storage.StatePending, storage.StateSent, storage.StateNotNow}
	}
	args := []any{id.ID}
	for _, st := range states {
		if !st.Terminal() {
			args = append(args, st)
		}
	}
	if len(args) == 1 {
		return 0, nil
	}
	where := []string{"enrollment_id = ?", "state IN (" + placeholders(len(args)-1) + ")"}
	if f.RequestType != "" {
		where = append(where, "request_type = ?")
		args = append(args, f.RequestType)
	}
	if !f.Before.IsZero() {
		where = append(where, "enqueued_at < ?")
		args = append(args, f.Before.UTC())
	}
	now := time.Now().UTC()
	query := s.q("UPDATE commands SET state = ?, completed_at = ? WHERE seq IN (SELECT seq FROM (SELECT seq FROM commands WHERE " +
		strings.Join(where, " AND ") + " ORDER BY seq LIMIT ?) AS batch)")
	var total int64
	for {
		res, err := s.db.ExecContext(ctx, query, append([]any{storage.StateCleared, now}, append(args, ClearBatchSize)...)...)
		if err != nil {
			return total, wrap("clear commands", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, wrap("clear rows affected", err)
		}
		total += n
		if n < ClearBatchSize {
			return total, nil
		}
	}
}

// PushInfo implements storage.PushStore.
func (s *Store) PushInfo(ctx context.Context, ids []mdm.EnrollmentID) (map[mdm.EnrollmentID]mdm.Push, error) {
	out := map[mdm.EnrollmentID]mdm.Push{}
	if len(ids) == 0 {
		return out, nil
	}
	byID := make(map[string]mdm.EnrollmentID, len(ids))
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		byID[id.ID] = id
		args = append(args, id.ID)
	}
	args = append(args, true)
	rows, err := s.db.QueryContext(ctx, s.q("SELECT id, topic, push_magic, push_token FROM enrollments WHERE id IN ("+placeholders(len(ids))+") AND enabled = ?"), args...)
	if err != nil {
		return nil, wrap("push info", err)
	}
	defer rows.Close()
	for rows.Next() {
		var idStr string
		var p mdm.Push
		if err := rows.Scan(&idStr, &p.Topic, &p.Magic, &p.Token); err != nil {
			return nil, wrap("scan push info", err)
		}
		if !p.Valid() {
			continue
		}
		p.Token = append([]byte(nil), p.Token...)
		out[byID[idStr]] = p
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("push info", err)
	}
	return out, nil
}

// StoreBootstrapToken implements storage.BootstrapTokenStore.
func (s *Store) StoreBootstrapToken(ctx context.Context, id mdm.EnrollmentID, token []byte, at time.Time) error {
	if len(token) == 0 {
		return fmt.Errorf("%w: empty bootstrap token", storage.ErrInvalid)
	}
	if err := validID(id); err != nil {
		return err
	}
	dev := id.Device()
	sealed, err := s.seal(purposeBootstrapToken, dev.ID, token)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, s.q("UPDATE enrollments SET bootstrap_token = ?, bootstrap_token_at = ? WHERE id = ?"), sealed, at.UTC(), dev.ID)
	if err != nil {
		return wrap("store bootstrap token", err)
	}
	return notFoundIfNoRows(res, dev.ID)
}

// BootstrapToken implements storage.BootstrapTokenStore.
func (s *Store) BootstrapToken(ctx context.Context, id mdm.EnrollmentID) ([]byte, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	dev := id.Device()
	var tok []byte
	err := s.db.QueryRowContext(ctx, s.q("SELECT bootstrap_token FROM enrollments WHERE id = ?"), dev.ID).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: enrollment %s", storage.ErrNotFound, dev.ID)
	}
	if err != nil {
		return nil, wrap("bootstrap token", err)
	}
	if len(tok) == 0 {
		return nil, fmt.Errorf("%w: bootstrap token for %s", storage.ErrNotFound, dev.ID)
	}
	return s.open(purposeBootstrapToken, dev.ID, tok)
}
