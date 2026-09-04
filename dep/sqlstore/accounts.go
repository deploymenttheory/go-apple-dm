package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/dep"
	"github.com/deploymenttheory/go-apple-dm/paging"
)

var accountCols = []string{
	"name", "consumer_key", "consumer_secret", "access_token", "access_secret", "access_token_expiry", "protocol_version",
	"org_name", "org_id", "server_name", "server_uuid", "admin_id", "limits", "profile_uuid", "terms_expired", "token_invalid",
	"created_at", "updated_at",
}

const selectAccount = "SELECT name, consumer_key, consumer_secret, access_token, access_secret, access_token_expiry, protocol_version, " +
	"org_name, org_id, server_name, server_uuid, admin_id, limits, profile_uuid, terms_expired, token_invalid, created_at, updated_at FROM dep_accounts"

// PutAccount implements dep.AccountStore.
func (t *txStore) PutAccount(ctx context.Context, a *dep.Account) error {
	if a == nil {
		return fmt.Errorf("%w: nil account", dep.ErrInvalid)
	}
	if err := validName("account name", a.Name); err != nil {
		return err
	}
	cs, err := t.s.seal(PurposeConsumerSecret, a.Name, []byte(a.ConsumerSecret))
	if err != nil {
		return err
	}
	at, err := t.s.seal(PurposeAccessToken, a.Name, []byte(a.AccessToken))
	if err != nil {
		return err
	}
	as, err := t.s.seal(PurposeAccessSecret, a.Name, []byte(a.AccessSecret))
	if err != nil {
		return err
	}
	var limits []byte
	if len(a.Limits) > 0 {
		if limits, err = dep.Marshal(a.Limits); err != nil {
			return err
		}
	}
	_, err = t.exec(ctx, "put account", t.upsert("dep_accounts", accountCols, accountCols[:1], []string{"created_at"}),
		a.Name, a.ConsumerKey, cs, at, as, nullTime(a.AccessTokenExpiry), a.ProtocolVersion,
		a.OrgName, a.OrgID, a.ServerName, a.ServerUUID, a.AdminID, limits, a.ProfileUUID, a.State.TermsExpired, a.State.TokenInvalid,
		utc(a.CreatedAt), utc(a.UpdatedAt))
	return err
}

type scanner interface{ Scan(dest ...any) error }

func (t *txStore) scanAccount(row scanner) (*dep.Account, error) {
	var a dep.Account
	var cs, at, as, limits []byte
	var expiry sql.NullTime
	if err := row.Scan(&a.Name, &a.ConsumerKey, &cs, &at, &as, &expiry, &a.ProtocolVersion,
		&a.OrgName, &a.OrgID, &a.ServerName, &a.ServerUUID, &a.AdminID, &limits, &a.ProfileUUID, &a.State.TermsExpired, &a.State.TokenInvalid,
		&a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, wrap("scan account", err)
	}
	for _, f := range []struct {
		purpose string
		raw     []byte
		dst     *string
	}{{PurposeConsumerSecret, cs, &a.ConsumerSecret}, {PurposeAccessToken, at, &a.AccessToken}, {PurposeAccessSecret, as, &a.AccessSecret}} {
		pt, err := t.s.open(f.purpose, a.Name, f.raw)
		if err != nil {
			return nil, err
		}
		*f.dst = string(pt)
	}
	a.AccessTokenExpiry = timePtr(expiry)
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	if len(limits) > 0 {
		if err := dep.Unmarshal(limits, &a.Limits); err != nil {
			return nil, err
		}
	}
	return &a, nil
}

// GetAccount implements dep.AccountStore.
func (t *txStore) GetAccount(ctx context.Context, name string) (*dep.Account, error) {
	if err := validName("account name", name); err != nil {
		return nil, err
	}
	rows, err := t.q.QueryContext(ctx, t.s.d.Rebind(selectAccount+" WHERE name = ?"), name)
	if err != nil {
		return nil, wrap("get account", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, wrap("get account", err)
		}
		return nil, notFound("account", name)
	}
	return t.scanAccount(rows)
}

// accountTables lists every table keyed by account, for DeleteAccount.
var accountTables = []string{"dep_sessions", "dep_cursors", "dep_keypairs", "dep_devices", "dep_profiles", "dep_assignments"}

// DeleteAccount implements dep.AccountStore.
func (t *txStore) DeleteAccount(ctx context.Context, name string) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	res, err := t.exec(ctx, "delete account", "DELETE FROM dep_accounts WHERE name = ?", name)
	if err != nil {
		return err
	}
	n, err := affected("delete account", res)
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound("account", name)
	}
	for _, table := range accountTables {
		if _, err := t.exec(ctx, "delete from "+table, "DELETE FROM "+table+" WHERE account = ?", name); err != nil { // #nosec G202 -- table names are literals
			return err
		}
	}
	return nil
}

// ListAccounts implements dep.AccountStore.
func (t *txStore) ListAccounts(ctx context.Context, p paging.Page) (paging.Result[dep.Account], error) {
	where, args := after(nil, nil, "name", p)
	query := selectAccount
	if len(where) > 0 {
		query += " WHERE " + where[0]
	}
	return keyset(ctx, t, "list accounts", query+" ORDER BY name", args, p, func(rows *sql.Rows) (dep.Account, string, error) {
		a, err := t.scanAccount(rows)
		if err != nil {
			return dep.Account{}, "", err
		}
		return *a, a.Name, nil
	})
}

// SetAccountState implements dep.AccountStore.
func (t *txStore) SetAccountState(ctx context.Context, name string, s dep.AccountState) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	res, err := t.exec(ctx, "set account state", "UPDATE dep_accounts SET terms_expired = ?, token_invalid = ? WHERE name = ?", s.TermsExpired, s.TokenInvalid, name)
	if err != nil {
		return err
	}
	n, err := affected("set account state", res)
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound("account", name)
	}
	return nil
}

func validStage(stage dep.Stage) error {
	if stage != dep.StageStaged && stage != dep.StageCurrent {
		return fmt.Errorf("%w: keypair stage %q", dep.ErrInvalid, stage)
	}
	return nil
}

var keypairCols = []string{"account", "stage", "cert_pem", "key_pem", "created_at"}

// PutKeypair implements dep.AccountStore.
func (t *txStore) PutKeypair(ctx context.Context, name string, stage dep.Stage, kp *dep.Keypair) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	if err := validStage(stage); err != nil {
		return err
	}
	if kp == nil || len(kp.CertPEM) == 0 || len(kp.KeyPEM) == 0 {
		return fmt.Errorf("%w: keypair needs certificate and key PEM", dep.ErrInvalid)
	}
	key, err := t.s.seal(PurposeKeyPEM, name+"/"+string(stage), kp.KeyPEM)
	if err != nil {
		return err
	}
	created := kp.CreatedAt
	if created.IsZero() {
		// MySQL refuses a zero DATETIME; a keypair without a timestamp is
		// dated when it is stored.
		created = time.Now()
	}
	_, err = t.exec(ctx, "put keypair", t.upsert("dep_keypairs", keypairCols, keypairCols[:2], nil), name, string(stage), kp.CertPEM, key, utc(created))
	return err
}

// Keypair implements dep.AccountStore.
func (t *txStore) Keypair(ctx context.Context, name string, stage dep.Stage) (*dep.Keypair, error) {
	if err := validName("account name", name); err != nil {
		return nil, err
	}
	if err := validStage(stage); err != nil {
		return nil, err
	}
	var kp dep.Keypair
	var key []byte
	found, err := t.row(ctx, "get keypair", "SELECT cert_pem, key_pem, created_at FROM dep_keypairs WHERE account = ? AND stage = ?", []any{name, string(stage)}, &kp.CertPEM, &key, &kp.CreatedAt)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %s keypair for %q", dep.ErrNotFound, stage, name)
	}
	if kp.KeyPEM, err = t.s.open(PurposeKeyPEM, name+"/"+string(stage), key); err != nil {
		return nil, err
	}
	kp.CreatedAt = kp.CreatedAt.UTC()
	return &kp, nil
}

// UpstageKeypair implements dep.AccountStore. The key is re-sealed for
// its new row identity.
func (t *txStore) UpstageKeypair(ctx context.Context, name string) error {
	kp, err := t.Keypair(ctx, name, dep.StageStaged)
	if err != nil {
		return err
	}
	if err := t.PutKeypair(ctx, name, dep.StageCurrent, kp); err != nil {
		return err
	}
	_, err = t.exec(ctx, "clear staged keypair", "DELETE FROM dep_keypairs WHERE account = ? AND stage = ?", name, string(dep.StageStaged))
	return err
}

// Session implements dep.SessionStore.
func (t *txStore) Session(ctx context.Context, name string) (string, error) {
	if err := validName("account name", name); err != nil {
		return "", err
	}
	var raw []byte
	found, err := t.row(ctx, "get session", "SELECT token FROM dep_sessions WHERE account = ?", []any{name}, &raw)
	if err != nil || !found {
		return "", err
	}
	pt, err := t.s.open(PurposeSession, name, raw)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

var sessionCols = []string{"account", "token", "updated_at"}

// SetSession implements dep.SessionStore.
func (t *txStore) SetSession(ctx context.Context, name, token string) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	if token == "" {
		_, err := t.exec(ctx, "clear session", "DELETE FROM dep_sessions WHERE account = ?", name)
		return err
	}
	sealed, err := t.s.seal(PurposeSession, name, []byte(token))
	if err != nil {
		return err
	}
	_, err = t.exec(ctx, "set session", t.upsert("dep_sessions", sessionCols, sessionCols[:1], nil), name, sealed, time.Now().UTC())
	return err
}

// Cursor implements dep.CursorStore.
func (t *txStore) Cursor(ctx context.Context, name string) (dep.Cursor, error) {
	if err := validName("account name", name); err != nil {
		return dep.Cursor{}, err
	}
	var c dep.Cursor
	var phase string
	var fetched sql.NullTime
	found, err := t.row(ctx, "get cursor", "SELECT value, phase, fetched_until, updated_at FROM dep_cursors WHERE account = ?", []any{name}, &c.Value, &phase, &fetched, &c.UpdatedAt)
	if err != nil || !found {
		return dep.Cursor{}, err
	}
	c.Phase = dep.Phase(phase)
	c.FetchedUntil = timePtr(fetched)
	c.UpdatedAt = c.UpdatedAt.UTC()
	return c, nil
}

var cursorCols = []string{"account", "value", "phase", "fetched_until", "updated_at"}

// SetCursor implements dep.CursorStore.
func (t *txStore) SetCursor(ctx context.Context, name string, c dep.Cursor) error {
	if err := validName("account name", name); err != nil {
		return err
	}
	if c.IsZero() {
		_, err := t.exec(ctx, "clear cursor", "DELETE FROM dep_cursors WHERE account = ?", name)
		return err
	}
	_, err := t.exec(ctx, "set cursor", t.upsert("dep_cursors", cursorCols, cursorCols[:1], nil), name, c.Value, string(c.Phase), nullTime(c.FetchedUntil), utc(c.UpdatedAt))
	return err
}

// Store methods outside Update run against the pool.

// PutAccount implements dep.AccountStore.
func (s *Store) PutAccount(ctx context.Context, a *dep.Account) error {
	return s.write(ctx, func(t *txStore) error { return t.PutAccount(ctx, a) })
}

// GetAccount implements dep.AccountStore.
func (s *Store) GetAccount(ctx context.Context, name string) (*dep.Account, error) {
	return s.view().GetAccount(ctx, name)
}

// DeleteAccount implements dep.AccountStore.
func (s *Store) DeleteAccount(ctx context.Context, name string) error {
	return s.runInTx(ctx, func(t *txStore) error { return t.DeleteAccount(ctx, name) })
}

// ListAccounts implements dep.AccountStore.
func (s *Store) ListAccounts(ctx context.Context, p paging.Page) (paging.Result[dep.Account], error) {
	return s.view().ListAccounts(ctx, p)
}

// SetAccountState implements dep.AccountStore.
func (s *Store) SetAccountState(ctx context.Context, name string, st dep.AccountState) error {
	return s.view().SetAccountState(ctx, name, st)
}

// PutKeypair implements dep.AccountStore.
func (s *Store) PutKeypair(ctx context.Context, name string, stage dep.Stage, kp *dep.Keypair) error {
	return s.write(ctx, func(t *txStore) error { return t.PutKeypair(ctx, name, stage, kp) })
}

// Keypair implements dep.AccountStore.
func (s *Store) Keypair(ctx context.Context, name string, stage dep.Stage) (*dep.Keypair, error) {
	return s.view().Keypair(ctx, name, stage)
}

// UpstageKeypair implements dep.AccountStore atomically.
func (s *Store) UpstageKeypair(ctx context.Context, name string) error {
	return s.runInTx(ctx, func(t *txStore) error { return t.UpstageKeypair(ctx, name) })
}

// Session implements dep.SessionStore.
func (s *Store) Session(ctx context.Context, name string) (string, error) {
	return s.view().Session(ctx, name)
}

// SetSession implements dep.SessionStore.
func (s *Store) SetSession(ctx context.Context, name, token string) error {
	return s.write(ctx, func(t *txStore) error { return t.SetSession(ctx, name, token) })
}

// Cursor implements dep.CursorStore.
func (s *Store) Cursor(ctx context.Context, name string) (dep.Cursor, error) {
	return s.view().Cursor(ctx, name)
}

// SetCursor implements dep.CursorStore.
func (s *Store) SetCursor(ctx context.Context, name string, c dep.Cursor) error {
	return s.write(ctx, func(t *txStore) error { return t.SetCursor(ctx, name, c) })
}
