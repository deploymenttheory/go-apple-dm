package webhook

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

//go:embed migrations/*/*.sql
var migrations embed.FS

// Config controls capture bounds and transport. PrivateNetworks explicitly opts
// an operator's trusted receiver networks into the outbound dial policy.
type Config struct {
	Enabled           bool
	Source            string
	PayloadRetention  time.Duration
	MetadataRetention time.Duration
	MaxBody           int
	PrivateNetworks   []string
	RootCAFile        string
	Client            *http.Client
	Now               func() time.Time
	// CommandType looks up already-authorized command metadata in the caller's
	// transaction. Missing/cleared commands return an empty type.
	CommandType func(context.Context, mdm.EnrollmentID, string) (string, error)
}

type Store struct {
	db              *sql.DB
	d               sqlcommon.Dialect
	unit            sqlcommon.UnitOfWork
	keys            *crypt.Keyring
	outbox          *eventstore.Store
	cfg             Config
	client          *http.Client
	captureFailures atomic.Uint64
}

type storedSubscription struct {
	Subscription
	Key           string    `json:"key"`
	PreviousKey   string    `json:"previous_key,omitempty"`
	PreviousUntil time.Time `json:"previous_until"`
	TokenHash     string    `json:"token_hash"`
}

func MigrationSet(d sqlcommon.Dialect) (sqlcommon.MigrationSet, error) {
	switch d.Name {
	case "sqlite", "postgres", "mysql":
		return sqlcommon.MigrationSet{Table: "webhook_schema_migrations", FS: sqlcommon.MustSub(migrations, "migrations/"+d.Name)}, nil
	default:
		return sqlcommon.MigrationSet{}, ErrInvalid
	}
}

// Open requires encryption even for summary-only subscriptions because signing
// keys are recoverable secrets. It never owns the supplied SQL pool.
func Open(ctx context.Context, db *sql.DB, d sqlcommon.Dialect, keys *crypt.Keyring, outbox *eventstore.Store, cfg Config) (*Store, error) {
	if db == nil || keys == nil || keys.Active() == "" || outbox == nil {
		return nil, ErrInvalid
	}
	if cfg.PayloadRetention == 0 {
		cfg.PayloadRetention = DefaultPayloadRetention
	}
	if cfg.MetadataRetention == 0 {
		cfg.MetadataRetention = DefaultMetadataRetention
	}
	if cfg.MaxBody == 0 {
		cfg.MaxBody = DefaultMaxBody
	}
	if cfg.PayloadRetention <= 0 || cfg.MetadataRetention < cfg.PayloadRetention || cfg.MaxBody < 1024 || cfg.MaxBody > 64<<20 {
		return nil, ErrInvalid
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Source == "" {
		cfg.Source = "server"
	}
	client, err := newHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	set, err := MigrationSet(d)
	if err != nil {
		return nil, err
	}
	if _, err = sqlcommon.MigrateSet(ctx, db, d, set); err != nil {
		return nil, err
	}
	return &Store{db: db, d: d, unit: sqlcommon.UnitOfWork{DB: db, Dialect: d}, keys: keys, outbox: outbox, cfg: cfg, client: client}, nil
}

func (s *Store) seal(v any, purpose, id string) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return sqlcommon.SealBlob(s.keys, purpose, b, id)
}

func (s *Store) open(b []byte, v any, purpose, id string) error {
	if !crypt.IsSealed(b) {
		return crypt.ErrUnsealed
	}
	b, err := sqlcommon.OpenBlob(s.keys, purpose, b, id)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Store) query(ctx context.Context) sqlcommon.Queryer { return sqlcommon.Query(ctx, s.db) }
func (s *Store) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return s.query(ctx).ExecContext(ctx, s.d.Rebind(q), args...)
}

func (s *Store) load(ctx context.Context, id string, lock bool) (storedSubscription, error) {
	q := "SELECT config FROM webhook_subscriptions WHERE id = ?"
	if lock {
		q += " " + s.d.ForUpdate
	}
	var b []byte
	err := s.query(ctx).QueryRowContext(ctx, s.d.Rebind(q), id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return storedSubscription{}, ErrNotFound
	}
	var sub storedSubscription
	if err == nil {
		err = s.open(b, &sub, "webhook_subscriptions.config", id)
	}
	return sub, err
}

func (s *Store) save(ctx context.Context, sub storedSubscription, create bool) error {
	b, err := s.seal(sub, "webhook_subscriptions.config", sub.ID)
	if err != nil {
		return err
	}
	if create {
		_, err = s.exec(ctx, "INSERT INTO webhook_subscriptions (id, revision, config) VALUES (?, ?, ?)", sub.ID, sub.Revision, b)
	} else {
		_, err = s.exec(ctx, "UPDATE webhook_subscriptions SET revision = ?, config = ? WHERE id = ?", sub.Revision, b, sub.ID)
	}
	return err
}

func validate(spec Spec) error {
	if spec.Name == "" || len(spec.Name) > 128 || len(spec.URL) > 4096 || len(spec.Events) == 0 || len(spec.Events) > 128 {
		return ErrInvalid
	}
	if err := validateURL(spec.URL); err != nil {
		return err
	}
	for _, pattern := range spec.Events {
		found := false
		for _, e := range Catalogue() {
			if eventMatch(pattern, e.Type) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: event selection", ErrInvalid)
		}
	}
	for _, values := range [][]string{spec.Filters.Operations, spec.Filters.Outcomes, spec.Filters.Channels, spec.Filters.Subjects, spec.Filters.CommandTypes} {
		if len(values) > 256 {
			return ErrInvalid
		}
		for _, v := range values {
			if v == "" || len(v) > 256 {
				return ErrInvalid
			}
		}
	}
	for _, v := range spec.Filters.Outcomes {
		if !slices.Contains([]string{"succeeded", "rejected", "failed", "incomplete", "unknown"}, v) {
			return ErrInvalid
		}
	}
	for _, v := range spec.Filters.Channels {
		valid := false
		for ch := mdm.ChannelDevice; ch.Valid(); ch++ {
			valid = valid || ch.String() == v
		}
		if !valid {
			return ErrInvalid
		}
	}
	return nil
}

func newCredentials() Credentials {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return Credentials{SigningSecret: "whsec_" + base64.StdEncoding.EncodeToString(key), PayloadToken: rand.Text()}
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *Store) Create(ctx context.Context, spec Spec, root bool) (Change, error) {
	if err := validate(spec); err != nil {
		return Change{}, err
	}
	if spec.Payload.Sensitive() && !root {
		return Change{}, ErrForbidden
	}
	now := s.cfg.Now().UTC()
	cred := newCredentials()
	sub := storedSubscription{Subscription: Subscription{ID: rand.Text(), Revision: 1, Spec: spec, Enabled: true, CreatedAt: now, UpdatedAt: now}, Key: cred.SigningSecret, TokenHash: hashToken(cred.PayloadToken)}
	err := s.unit.Run(ctx, func(ctx context.Context) error { return s.save(ctx, sub, true) })
	return Change{Subscription: sub.Subscription, Credentials: &cred}, err
}

func (s *Store) Get(ctx context.Context, id string) (Subscription, error) {
	sub, err := s.load(ctx, id, false)
	return sub.Subscription, err
}

func (s *Store) List(ctx context.Context, after string, limit int) ([]Subscription, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	rows, err := s.query(ctx).QueryContext(ctx, s.d.Rebind("SELECT id, config FROM webhook_subscriptions WHERE id > ? ORDER BY id LIMIT ?"), after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Subscription{}
	for rows.Next() {
		var id string
		var b []byte
		var sub storedSubscription
		if err = rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		if err = s.open(b, &sub, "webhook_subscriptions.config", id); err != nil {
			return nil, err
		}
		out = append(out, sub.Subscription)
	}
	return out, rows.Err()
}

// Update uses an expected revision to prevent lost operator updates. Old
// deliveries remain paused; only explicit replay can move them to the new URL.
func (s *Store) Update(ctx context.Context, id string, revision int, spec Spec, root bool) (Change, error) {
	if err := validate(spec); err != nil {
		return Change{}, err
	}
	var out Change
	err := s.unit.Run(ctx, func(ctx context.Context) error {
		sub, err := s.load(ctx, id, true)
		if err != nil {
			return err
		}
		if (sub.Payload.Sensitive() || spec.Payload.Sensitive()) && !root {
			return ErrForbidden
		}
		if sub.Deleted || sub.Revision != revision {
			return ErrConflict
		}
		if err = s.deliveryState(ctx, id, "paused", false); err != nil {
			return err
		}
		sub.Spec = spec
		sub.Revision++
		sub.UpdatedAt = s.cfg.Now().UTC()
		cred := newCredentials()
		sub.Key = cred.SigningSecret
		sub.TokenHash = hashToken(cred.PayloadToken)
		sub.PreviousKey = ""
		if err = s.save(ctx, sub, false); err != nil {
			return err
		}
		out = Change{Subscription: sub.Subscription, Credentials: &cred}
		return nil
	})
	return out, err
}

func (s *Store) SetState(ctx context.Context, id, action string, root bool) (Subscription, error) {
	var out Subscription
	err := s.unit.Run(ctx, func(ctx context.Context) error {
		sub, err := s.load(ctx, id, true)
		if err != nil {
			return err
		}
		if sub.Payload.Sensitive() && !root {
			return ErrForbidden
		}
		if sub.Deleted {
			return ErrConflict
		}
		switch action {
		case "pause":
			sub.Paused = true
		case "resume":
			sub.Paused = false
		case "disable":
			sub.Enabled = false
		case "enable":
			sub.Enabled = true
		case "delete":
			sub.Enabled = false
			sub.Deleted = true
		default:
			return ErrInvalid
		}
		sub.UpdatedAt = s.cfg.Now().UTC()
		if err = s.save(ctx, sub, false); err != nil {
			return err
		}
		state := "paused"
		if sub.Enabled && !sub.Paused {
			state = "pending"
		}
		if sub.Deleted {
			state = "cancelled"
		}
		if err = s.deliveryState(ctx, id, state, state == "pending"); err != nil {
			return err
		}
		out = sub.Subscription
		return nil
	})
	return out, err
}

func (s *Store) deliveryState(ctx context.Context, id, state string, currentOnly bool) error {
	query := `UPDATE event_deliveries SET state = ?, lease_token = '', lease_until = 0 WHERE state IN ('pending','paused','blocked') AND event_id IN (SELECT delivery_id FROM webhook_messages WHERE subscription_id = ?`
	args := []any{state, id}
	if currentOnly {
		query += " AND revision = (SELECT revision FROM webhook_subscriptions WHERE id = ?)"
		args = append(args, id)
	}
	query += ")"
	_, err := s.exec(ctx, query, args...)
	return err
}

func (s *Store) Rotate(ctx context.Context, id string, overlap time.Duration, root bool) (Change, error) {
	if overlap < 0 || overlap > 7*24*time.Hour {
		return Change{}, ErrInvalid
	}
	var out Change
	err := s.unit.Run(ctx, func(ctx context.Context) error {
		sub, err := s.load(ctx, id, true)
		if err != nil {
			return err
		}
		if sub.Payload.Sensitive() && !root {
			return ErrForbidden
		}
		if sub.Deleted {
			return ErrConflict
		}
		cred := newCredentials()
		sub.PreviousKey = sub.Key
		sub.PreviousUntil = s.cfg.Now().Add(overlap)
		sub.Key = cred.SigningSecret
		sub.TokenHash = hashToken(cred.PayloadToken)
		sub.UpdatedAt = s.cfg.Now().UTC()
		if err = s.save(ctx, sub, false); err != nil {
			return err
		}
		out = Change{Subscription: sub.Subscription, Credentials: &cred}
		return nil
	})
	return out, err
}

// BlobColumns registers every sealed column for backup verification and rekey.
func BlobColumns() []sqlcommon.BlobColumn {
	return []sqlcommon.BlobColumn{
		{Table: "webhook_subscriptions", Column: "config", Keys: []string{"id"}},
		{Table: "webhook_messages", Column: "payload", Keys: []string{"delivery_id"}},
	}
}

func (s *Store) Rewrap(ctx context.Context) (int, error) {
	return sqlcommon.RewrapBlobs(ctx, s.db, s.d, s.keys, BlobColumns())
}

func destination(id string, revision int) string {
	return fmt.Sprintf("native-webhook:%s:%d", id, revision)
}
func isDestination(id string) bool { return strings.HasPrefix(id, "native-webhook:") }
