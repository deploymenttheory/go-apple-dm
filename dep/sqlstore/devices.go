package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

var deviceCols = []string{"account", "serial_number", "record", "profile_uuid", "profile_status", "op_type", "op_date", "deleted", "first_seen", "updated_at"}

const selectDevice = "SELECT account, serial_number, record, deleted, first_seen, updated_at FROM dep_devices"

// PutDevices implements dep.DeviceStore. The wire record is stored as the
// bytes dep.Marshal produces, so every key including Extra round-trips
// identically on every engine; the indexed columns are copies.
func (t *txStore) PutDevices(ctx context.Context, account string, devs []dep.Device, at time.Time) error {
	if err := validName("account name", account); err != nil {
		return err
	}
	for _, d := range devs {
		if d.SerialNumber == "" {
			return fmt.Errorf("%w: device without serial_number", dep.ErrInvalid)
		}
	}
	query := t.upsert("dep_devices", deviceCols, deviceCols[:2], []string{"first_seen"})
	for _, d := range devs {
		raw, err := dep.Marshal(d)
		if err != nil {
			return err
		}
		if _, err := t.exec(ctx, "put device", query, account, d.SerialNumber, raw, d.ProfileUUID, d.ProfileStatus, d.OpType, nullTime(d.OpDate), d.OpType == dep.OpDeleted, utc(at), utc(at)); err != nil {
			return err
		}
	}
	return nil
}

func scanDevice(row scanner) (dep.StoredDevice, string, error) {
	var sd dep.StoredDevice
	var raw []byte
	if err := row.Scan(&sd.Account, &sd.SerialNumber, &raw, &sd.Deleted, &sd.FirstSeen, &sd.UpdatedAt); err != nil {
		return dep.StoredDevice{}, "", wrap("scan device", err)
	}
	serial := sd.SerialNumber
	if err := dep.Unmarshal(raw, &sd.Device); err != nil {
		return dep.StoredDevice{}, "", err
	}
	sd.FirstSeen, sd.UpdatedAt = sd.FirstSeen.UTC(), sd.UpdatedAt.UTC()
	return sd, serial, nil
}

// GetDevice implements dep.DeviceStore.
func (t *txStore) GetDevice(ctx context.Context, account, serial string) (*dep.StoredDevice, error) {
	if err := validName("account name", account); err != nil {
		return nil, err
	}
	if err := validName("serial", serial); err != nil {
		return nil, err
	}
	rows, err := t.q.QueryContext(ctx, t.s.d.Rebind(selectDevice+" WHERE account = ? AND serial_number = ?"), account, serial)
	if err != nil {
		return nil, wrap("get device", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, wrap("get device", err)
		}
		return nil, notFound("device", serial)
	}
	sd, _, err := scanDevice(rows)
	if err != nil {
		return nil, err
	}
	return &sd, nil
}

// ListDevices implements dep.DeviceStore.
func (t *txStore) ListDevices(ctx context.Context, account string, q dep.DeviceQuery, p storage.Page) (storage.Result[dep.StoredDevice], error) {
	if err := validName("account name", account); err != nil {
		return storage.Result[dep.StoredDevice]{}, err
	}
	where, args := []string{"account = ?"}, []any{account}
	if !q.IncludeDeleted {
		where = append(where, "deleted = ?")
		args = append(args, false)
	}
	if q.ProfileUUID != "" {
		where = append(where, "profile_uuid = ?")
		args = append(args, q.ProfileUUID)
	}
	where, args = after(where, args, "serial_number", p)
	query := selectDevice + " WHERE " + strings.Join(where, " AND ") + " ORDER BY serial_number"
	return keyset(ctx, t, "list devices", query, args, p, func(rows *sql.Rows) (dep.StoredDevice, string, error) { return scanDevice(rows) })
}

var profileCols = []string{"account", "profile_uuid", "record", "updated_at"}

// PutProfile implements dep.ProfileStore.
func (t *txStore) PutProfile(ctx context.Context, account string, p *dep.Profile) error {
	if err := validName("account name", account); err != nil {
		return err
	}
	if p == nil || p.ProfileUUID == "" {
		return fmt.Errorf("%w: profile needs a profile_uuid", dep.ErrInvalid)
	}
	raw, err := dep.Marshal(p)
	if err != nil {
		return err
	}
	_, err = t.exec(ctx, "put profile", t.upsert("dep_profiles", profileCols, profileCols[:2], nil), account, p.ProfileUUID, raw, time.Now().UTC())
	return err
}

// GetProfile implements dep.ProfileStore.
func (t *txStore) GetProfile(ctx context.Context, account, uuid string) (*dep.Profile, error) {
	if err := validName("account name", account); err != nil {
		return nil, err
	}
	if err := validName("profile uuid", uuid); err != nil {
		return nil, err
	}
	var raw []byte
	found, err := t.row(ctx, "get profile", "SELECT record FROM dep_profiles WHERE account = ? AND profile_uuid = ?", []any{account, uuid}, &raw)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, notFound("profile", uuid)
	}
	var p dep.Profile
	if err := dep.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// DeleteProfile implements dep.ProfileStore.
func (t *txStore) DeleteProfile(ctx context.Context, account, uuid string) error {
	if err := validName("account name", account); err != nil {
		return err
	}
	if err := validName("profile uuid", uuid); err != nil {
		return err
	}
	res, err := t.exec(ctx, "delete profile", "DELETE FROM dep_profiles WHERE account = ? AND profile_uuid = ?", account, uuid)
	if err != nil {
		return err
	}
	n, err := affected("delete profile", res)
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound("profile", uuid)
	}
	return nil
}

// ListProfiles implements dep.ProfileStore.
func (t *txStore) ListProfiles(ctx context.Context, account string, p storage.Page) (storage.Result[dep.Profile], error) {
	if err := validName("account name", account); err != nil {
		return storage.Result[dep.Profile]{}, err
	}
	where, args := after([]string{"account = ?"}, []any{account}, "profile_uuid", p)
	query := "SELECT profile_uuid, record FROM dep_profiles WHERE " + strings.Join(where, " AND ") + " ORDER BY profile_uuid"
	return keyset(ctx, t, "list profiles", query, args, p, func(rows *sql.Rows) (dep.Profile, string, error) {
		var uuid string
		var raw []byte
		if err := rows.Scan(&uuid, &raw); err != nil {
			return dep.Profile{}, "", wrap("scan profile", err)
		}
		var pr dep.Profile
		if err := dep.Unmarshal(raw, &pr); err != nil {
			return dep.Profile{}, "", err
		}
		return pr, uuid, nil
	})
}

var assignmentCols = []string{"account", "serial_number", "profile_uuid", "status", "attempts", "last_error", "attempted_at", "next_attempt_at"}

const selectAssignment = "SELECT account, serial_number, profile_uuid, status, attempts, last_error, attempted_at, next_attempt_at FROM dep_assignments"

// PutAssignment implements dep.AssignmentStore.
func (t *txStore) PutAssignment(ctx context.Context, a *dep.Assignment) error {
	if a == nil {
		return fmt.Errorf("%w: nil assignment", dep.ErrInvalid)
	}
	if err := validName("account name", a.Account); err != nil {
		return err
	}
	if err := validName("serial", a.SerialNumber); err != nil {
		return err
	}
	_, err := t.exec(ctx, "put assignment", t.upsert("dep_assignments", assignmentCols, assignmentCols[:2], nil),
		a.Account, a.SerialNumber, a.ProfileUUID, a.Status, a.Attempts, a.LastError, nullTime(&a.AttemptedAt), nullTime(&a.NextAttemptAt))
	return err
}

func scanAssignment(row scanner) (dep.Assignment, string, error) {
	var a dep.Assignment
	var attempted, next sql.NullTime
	if err := row.Scan(&a.Account, &a.SerialNumber, &a.ProfileUUID, &a.Status, &a.Attempts, &a.LastError, &attempted, &next); err != nil {
		return dep.Assignment{}, "", wrap("scan assignment", err)
	}
	a.AttemptedAt, a.NextAttemptAt = fromNull(attempted), fromNull(next)
	return a, a.SerialNumber, nil
}

// GetAssignment implements dep.AssignmentStore.
func (t *txStore) GetAssignment(ctx context.Context, account, serial string) (*dep.Assignment, error) {
	if err := validName("account name", account); err != nil {
		return nil, err
	}
	if err := validName("serial", serial); err != nil {
		return nil, err
	}
	rows, err := t.q.QueryContext(ctx, t.s.d.Rebind(selectAssignment+" WHERE account = ? AND serial_number = ?"), account, serial)
	if err != nil {
		return nil, wrap("get assignment", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, wrap("get assignment", err)
		}
		return nil, notFound("assignment", serial)
	}
	a, _, err := scanAssignment(rows)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAssignments implements dep.AssignmentStore.
func (t *txStore) ListAssignments(ctx context.Context, account string, q dep.AssignmentQuery, p storage.Page) (storage.Result[dep.Assignment], error) {
	if err := validName("account name", account); err != nil {
		return storage.Result[dep.Assignment]{}, err
	}
	where, args := []string{"account = ?"}, []any{account}
	if q.Status != "" {
		where = append(where, "status = ?")
		args = append(args, q.Status)
	}
	where, args = after(where, args, "serial_number", p)
	query := selectAssignment + " WHERE " + strings.Join(where, " AND ") + " ORDER BY serial_number"
	return keyset(ctx, t, "list assignments", query, args, p, func(rows *sql.Rows) (dep.Assignment, string, error) { return scanAssignment(rows) })
}

// Store methods outside Update run against the pool.

// PutDevices implements dep.DeviceStore.
func (s *Store) PutDevices(ctx context.Context, account string, devs []dep.Device, at time.Time) error {
	return s.write(ctx, func(t *txStore) error { return t.PutDevices(ctx, account, devs, at) })
}

// GetDevice implements dep.DeviceStore.
func (s *Store) GetDevice(ctx context.Context, account, serial string) (*dep.StoredDevice, error) {
	return s.view().GetDevice(ctx, account, serial)
}

// ListDevices implements dep.DeviceStore.
func (s *Store) ListDevices(ctx context.Context, account string, q dep.DeviceQuery, p storage.Page) (storage.Result[dep.StoredDevice], error) {
	return s.view().ListDevices(ctx, account, q, p)
}

// PutProfile implements dep.ProfileStore.
func (s *Store) PutProfile(ctx context.Context, account string, p *dep.Profile) error {
	return s.write(ctx, func(t *txStore) error { return t.PutProfile(ctx, account, p) })
}

// GetProfile implements dep.ProfileStore.
func (s *Store) GetProfile(ctx context.Context, account, uuid string) (*dep.Profile, error) {
	return s.view().GetProfile(ctx, account, uuid)
}

// DeleteProfile implements dep.ProfileStore.
func (s *Store) DeleteProfile(ctx context.Context, account, uuid string) error {
	return s.view().DeleteProfile(ctx, account, uuid)
}

// ListProfiles implements dep.ProfileStore.
func (s *Store) ListProfiles(ctx context.Context, account string, p storage.Page) (storage.Result[dep.Profile], error) {
	return s.view().ListProfiles(ctx, account, p)
}

// PutAssignment implements dep.AssignmentStore.
func (s *Store) PutAssignment(ctx context.Context, a *dep.Assignment) error {
	return s.write(ctx, func(t *txStore) error { return t.PutAssignment(ctx, a) })
}

// GetAssignment implements dep.AssignmentStore.
func (s *Store) GetAssignment(ctx context.Context, account, serial string) (*dep.Assignment, error) {
	return s.view().GetAssignment(ctx, account, serial)
}

// ListAssignments implements dep.AssignmentStore.
func (s *Store) ListAssignments(ctx context.Context, account string, q dep.AssignmentQuery, p storage.Page) (storage.Result[dep.Assignment], error) {
	return s.view().ListAssignments(ctx, account, q, p)
}
