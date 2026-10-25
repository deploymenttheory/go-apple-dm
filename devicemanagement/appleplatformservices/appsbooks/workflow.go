package appsbooks

import (
	"context"
	"crypto/sha256"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// ErrRevision rejects stale work, including work started before token replacement.
var ErrRevision = errors.New("appsbooks: location revision changed")

// ErrConfigurationPending requires reconciliation of an unfinished configuration
// before another configuration or token replacement can start.
var ErrConfigurationPending = errors.New("appsbooks: configuration requires reconciliation")

// Location is credential-free metadata. ID is caller-owned; UID is Apple's uId
// library identifier. MDMID remains fixed across token replacement and restart.
// Revision changes with every committed operation; CredentialRevision changes only
// on validated token replacement. Records have no automatic expiry.
type Location struct {
	ID                   string    `json:"id"`
	UID                  string    `json:"uid"`
	MDMID                string    `json:"mdmId"`
	Name                 string    `json:"name"`
	OwnerID              string    `json:"ownerId"`
	Revision             uint64    `json:"revision"`
	CredentialRevision   uint64    `json:"credentialRevision"`
	TokenExpiresAt       time.Time `json:"tokenExpiresAt"`
	ConfigurationPending bool      `json:"configurationPending"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// SourceBatch retains an entire successful traversal in page order. Pages are
// byte slices so persistence preserves the original JSON bytes, including number
// tokens, unknown fields, null and false. SinceVersionID is empty for a full read.
// VersionID is exclusively the FIRST page's version, even if later pages differ.
type SourceBatch struct {
	SinceVersionID string    `json:"sinceVersionId"`
	VersionID      string    `json:"versionId"`
	Pages          [][]byte  `json:"pages"`
	ObservedAt     time.Time `json:"observedAt"`
}

// LocationEvidence contains only successful source evidence. Assets is replaced
// by each full refresh; assignment/user batches form an ordered incremental source
// journal, starting with a full read. Applications can derive their own views;
// these records impose no inventory model or inferred deletion semantics.
// Configuration may contain notification credentials: protect access and storage.
type LocationEvidence struct {
	Configuration []byte        `json:"configuration"`
	Assets        SourceBatch   `json:"assets"`
	Assignments   []SourceBatch `json:"assignments"`
	Users         []SourceBatch `json:"users"`
}

type locationCredentials struct {
	SToken   string `json:"sToken"`
	Revision uint64 `json:"revision"`
}

type workflowClient struct {
	revision uint64
	client   *Client
}

// Workflow composes the existing HTTP client and caller-owned atomic state.
// Share one Workflow per process. Separate processes coordinate Apple's aggregate
// request rate. Stores must protect credentials and raw configuration; the server's
// sealed statestore is suitable. No HTTP request runs inside a state transaction.
type Workflow struct {
	store   state.Store
	config  Config
	mu      sync.Mutex
	clients map[string]workflowClient
}

// NewWorkflow accepts client transport/clock options and a stable MDMID. SToken
// and UID must be empty: credentials and Apple's library UID belong to each
// persisted location, never to the workflow's common configuration.
func NewWorkflow(store state.Store, cfg Config) (*Workflow, error) {
	if store == nil || cfg.MDMID == "" || cfg.SToken != "" || cfg.UID != "" || cfg.ReadRetries < 0 || cfg.ReadRetries > 5 {
		return nil, ErrConfig
	}
	return &Workflow{store: store, config: cfg, clients: map[string]workflowClient{}}, nil
}

// locationKey uses a fixed-size digest so caller identifiers never escape their
// state namespace or exceed backend key limits.
func locationKey(id, kind string) string {
	return fmt.Sprintf("appsbooks/location/%x/%s", sha256.Sum256([]byte(id)), kind)
}

// uidKey binds an Apple library to one caller location in this store.
func uidKey(uid string) string { return fmt.Sprintf("appsbooks/uid/%x", sha256.Sum256([]byte(uid))) }

// readState decodes a private state record, preserving backend failures.
func readState(ctx context.Context, r state.Reader, key string, out any) error {
	record, err := r.Get(ctx, key)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(record.Value, out); err != nil {
		return fmt.Errorf("%w: invalid persisted location state", ErrProtocol)
	}
	return nil
}

// putState writes a persistent record without TTL. JSON bytes in []byte fields
// are base64 encoded rather than parsed or normalized during persistence.
func putState(ctx context.Context, tx state.Tx, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: key, Value: data})
}

// readLocation checks the immutable local MDM identity on every operation.
func (w *Workflow) readLocation(ctx context.Context, r state.Reader, id string) (Location, error) {
	var l Location
	if id == "" {
		return l, ErrInput
	}
	if err := readState(ctx, r, locationKey(id, "metadata"), &l); err != nil {
		return l, err
	}
	if l.ID != id || l.UID == "" || l.Revision == 0 || l.CredentialRevision == 0 {
		return Location{}, ErrProtocol
	}
	if l.MDMID != w.config.MDMID {
		return Location{}, ErrOwnership
	}
	return l, nil
}

// Load returns credential-free location metadata without Apple requests. Reads
// never claim ownership. Use LoadEvidence explicitly for sensitive source data.
func (w *Workflow) Load(ctx context.Context, id string) (Location, error) {
	return w.readLocation(ctx, w.store, id)
}

// LoadEvidence returns a consistent metadata/source snapshot. This is privileged
// access: raw configuration can contain notification credentials and user pages
// can contain personal data. It never returns the location sToken. The caller
// controls authorization and retention; returned bytes cannot mutate stored state.
func (w *Workflow) LoadEvidence(ctx context.Context, id string) (Location, LocationEvidence, error) {
	var l Location
	var e LocationEvidence
	err := w.store.Update(ctx, []string{locationKey(id, "metadata")}, func(tx state.Tx) error {
		var err error
		l, err = w.readLocation(ctx, tx, id)
		if err != nil {
			return err
		}
		return readState(ctx, tx, locationKey(id, "evidence"), &e)
	})
	return l, e, err
}

// session atomically captures credentials with the metadata revision they serve.
func (w *Workflow) session(ctx context.Context, id string, revision uint64, allowPending bool) (Location, *Client, error) {
	var l Location
	var credentials locationCredentials
	err := w.store.Update(ctx, []string{locationKey(id, "metadata")}, func(tx state.Tx) error {
		var err error
		l, err = w.readLocation(ctx, tx, id)
		if err != nil {
			return err
		}
		if l.Revision != revision || revision == math.MaxUint64 {
			return ErrRevision
		}
		if l.ConfigurationPending && !allowPending {
			return ErrConfigurationPending
		}
		if err := readState(ctx, tx, locationKey(id, "credentials"), &credentials); err != nil {
			return err
		}
		if credentials.Revision != l.CredentialRevision {
			return ErrProtocol
		}
		return nil
	})
	if err != nil {
		return Location{}, nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	cached, ok := w.clients[id]
	if ok && cached.revision > credentials.Revision {
		return Location{}, nil, ErrRevision
	}
	if !ok || cached.revision != credentials.Revision {
		cfg := w.config
		cfg.SToken, cfg.UID = credentials.SToken, l.UID
		c, err := New(cfg)
		if err != nil {
			return Location{}, nil, err
		}
		cached = workflowClient{revision: credentials.Revision, client: c}
		w.clients[id] = cached
	}
	return l, cached.client, nil
}

// Register explicitly creates a location (revision zero) or replaces its sToken.
// A read-only Apple configuration probe validates UID and ownership first. A
// replacement must identify the SAME Apple library and stable MDM. Failed probes
// or revision races retain the previous credentials, evidence and checkpoints.
// Register never claims an unclaimed location or overwrites foreign ownership.
func (w *Workflow) Register(ctx context.Context, id string, revision uint64, token string) (Location, error) {
	if id == "" || revision == math.MaxUint64 {
		return Location{}, ErrInput
	}
	var prior Location
	if revision != 0 {
		var err error
		prior, err = w.Load(ctx, id)
		if err != nil {
			return Location{}, err
		}
		if prior.Revision != revision {
			return Location{}, ErrRevision
		}
		if prior.ConfigurationPending {
			return Location{}, ErrConfigurationPending
		}
	}
	cfg := w.config
	cfg.SToken, cfg.UID = token, prior.UID
	client, err := New(cfg)
	if err != nil {
		return Location{}, err
	}
	observed, err := client.ClientConfig(ctx)
	if err != nil {
		return Location{}, err
	}
	key, binding := locationKey(id, "metadata"), uidKey(observed.UID)
	var result Location
	err = w.store.Update(ctx, []string{key, binding}, func(tx state.Tx) error {
		current, err := w.readLocation(ctx, tx, id)
		if errors.Is(err, state.ErrNotFound) && revision == 0 {
			current = Location{ID: id, UID: observed.UID, MDMID: w.config.MDMID}
		} else if err != nil {
			return err
		}
		if current.Revision != revision {
			return ErrRevision
		}
		if current.ConfigurationPending {
			return ErrConfigurationPending
		}
		if current.UID != observed.UID {
			return ErrLocation
		}
		var owner string
		err = readState(ctx, tx, binding, &owner)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if err == nil && owner != id {
			return ErrLocation
		}
		var evidence LocationEvidence
		if revision != 0 {
			if err := readState(ctx, tx, locationKey(id, "evidence"), &evidence); err != nil {
				return err
			}
		}
		current.Revision++
		current.CredentialRevision = current.Revision
		current.Name, current.TokenExpiresAt, current.UpdatedAt = observed.LocationName, client.expires, tx.Now()
		current.OwnerID = observedOwner(observed)
		evidence.Configuration = observed.Raw
		if err := putState(ctx, tx, binding, id); err != nil {
			return err
		}
		if err := putState(ctx, tx, locationKey(id, "credentials"), locationCredentials{SToken: client.token, Revision: current.CredentialRevision}); err != nil {
			return err
		}
		if err := putState(ctx, tx, locationKey(id, "evidence"), evidence); err != nil {
			return err
		}
		if err := putState(ctx, tx, key, current); err != nil {
			return err
		}
		result = current
		return nil
	})
	if err != nil {
		return Location{}, err
	}
	return result, nil
}

// commit checks a captured revision under the same location lock used by EVERY
// writer, preventing old HTTP work from publishing after local changes.
func (w *Workflow) commit(ctx context.Context, l Location, fn func(state.Tx, *Location, *LocationEvidence) error) (Location, error) {
	var result Location
	err := w.store.Update(ctx, []string{locationKey(l.ID, "metadata")}, func(tx state.Tx) error {
		current, err := w.readLocation(ctx, tx, l.ID)
		if err != nil {
			return err
		}
		if current.Revision != l.Revision || current.Revision == math.MaxUint64 {
			return ErrRevision
		}
		var e LocationEvidence
		if err := readState(ctx, tx, locationKey(l.ID, "evidence"), &e); err != nil {
			return err
		}
		if err := fn(tx, &current, &e); err != nil {
			return err
		}
		current.Revision++
		current.UpdatedAt = tx.Now()
		if err := putState(ctx, tx, locationKey(l.ID, "evidence"), e); err != nil {
			return err
		}
		if err := putState(ctx, tx, locationKey(l.ID, "metadata"), current); err != nil {
			return err
		}
		result = current
		return nil
	})
	if err != nil {
		return Location{}, err
	}
	return result, nil
}

// Configure explicitly claims an unclaimed location or updates this MDM's remote
// configuration. It first validates identity through a GET, then reserves a durable
// pending revision before the POST. Errors after reservation leave pending state;
// use Probe after the original call has terminated to reconcile before retrying.
// No local lock can provide an ownership CAS across Apple's service: operators
// must coordinate transfers with other MDMs and stop old processes before recovery.
func (w *Workflow) Configure(ctx context.Context, id string, revision uint64, in ClientConfigurationRequest) (Location, error) {
	l, c, err := w.session(ctx, id, revision, false)
	if err != nil {
		return Location{}, err
	}
	if _, err := c.ClientConfig(ctx); err != nil {
		return Location{}, err
	}
	// Validate before reserving; SetClientConfig rechecks under the client gate.
	if err := c.lock(ctx); err != nil {
		return Location{}, err
	}
	err = c.validateConfig(in)
	c.unlock()
	if err != nil {
		return Location{}, err
	}
	l, err = w.commit(ctx, l, func(_ state.Tx, l *Location, _ *LocationEvidence) error { l.ConfigurationPending = true; return nil })
	if err != nil {
		return Location{}, err
	}
	out, err := c.SetClientConfig(ctx, in)
	if err != nil {
		return Location{}, err
	}
	return w.publishConfiguration(ctx, l, out)
}

// Probe reads and validates Apple's configuration without claiming ownership.
// It also reconciles a pending configuration after the original process/call is
// stopped. This is an explicit recovery operation, not permission to replay a POST.
func (w *Workflow) Probe(ctx context.Context, id string, revision uint64) (Location, error) {
	l, c, err := w.session(ctx, id, revision, true)
	if err != nil {
		return Location{}, err
	}
	out, err := c.ClientConfig(ctx)
	if err != nil {
		return Location{}, err
	}
	return w.publishConfiguration(ctx, l, out)
}

// publishConfiguration stores successful raw configuration separately from tokens.
func (w *Workflow) publishConfiguration(ctx context.Context, l Location, out ClientConfiguration) (Location, error) {
	return w.commit(ctx, l, func(_ state.Tx, l *Location, e *LocationEvidence) error {
		l.Name, l.ConfigurationPending = out.LocationName, false
		l.OwnerID = observedOwner(out)
		e.Configuration = out.Raw
		return nil
	})
}

// observedOwner distinguishes an unclaimed location from this MDM's ownership.
func observedOwner(out ClientConfiguration) string {
	if out.MDMInfo == nil {
		return ""
	}
	return out.MDMInfo.ID
}
