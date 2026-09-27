package applications

import (
	"context"
	"errors"
	"io"
	"math"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

var (
	ErrInvalid     = errors.New("applications: invalid request")
	ErrNotFound    = errors.New("applications: not found")
	ErrConflict    = errors.New("applications: revision conflict")
	ErrIntegrity   = errors.New("applications: content integrity mismatch")
	ErrUnsupported = errors.New("applications: unsupported native installer options")
	ErrIneligible  = errors.New("applications: device is ineligible")
	ErrTooLarge    = errors.New("applications: content exceeds size limit")
)

// BlobStore retains package bytes under manager-generated keys. Put receives a seekable
// stream of the declared size. Open reads from offset for length bytes; -1 means to EOF.
// Delete is idempotent. Implementations must not make objects publicly readable.
type BlobStore interface {
	Put(context.Context, string, io.ReadSeeker, int64) error
	Open(context.Context, string, int64, int64) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

// Source records provenance, not credentials. Location excludes URL queries and userinfo.
type Source struct {
	Kind     string `json:"kind"`
	Location string `json:"location,omitempty"`
}

// Content is an immutable, verified upload. Revision identifies the storage snapshot;
// SHA256 identifies its bytes. Metadata changes never alter a content revision.
type Content struct {
	Revision string `json:"revision"`
	Backend  string `json:"backend"`
	Key      string `json:"key"`
	Digests
	Format       string       `json:"format"`
	Size         int64        `json:"size"`
	Metadata     Metadata     `json:"metadata"`
	Source       Source       `json:"source"`
	CreatedAt    time.Time    `json:"createdAt"`
	Verification Verification `json:"verification"`
}

// Record is a mutable package catalogue entry. Revision is an optimistic concurrency
// token. A nil Content means metadata exists but no upload has completed verification.
type Record struct {
	ID        string    `json:"id"`
	Revision  string    `json:"revision"`
	Metadata  Metadata  `json:"metadata"`
	Content   *Content  `json:"content,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Deleting  bool      `json:"deleting,omitempty"`
	// Indexed is nil when no package payload index is available.
	Indexed             *bool     `json:"indexed"`
	CloudTransferStatus string    `json:"cloudTransferStatus"`
	HistorySequence     uint64    `json:"historySequence"`
	Manifest            *Manifest `json:"manifest,omitempty"`
}

// Config separates transactional metadata from streaming byte storage. Backend names
// are deployment-defined. MaxBytes defaults to 2 GiB. ScratchDir must be private and
// have capacity for one package per concurrent upload. Verification controls the required
// signer trust and archive checks; package contents are never executed.
type Config struct {
	State        state.Store
	Backends     map[string]BlobStore
	ScratchDir   string
	MaxBytes     int64
	Verification VerificationPolicy
}

// Manager owns package catalogue operations over caller-supplied persistence.
type Manager struct{ cfg Config }

// New validates storage configuration without contacting any backend.
func New(cfg Config) (*Manager, error) {
	if cfg.State == nil || len(cfg.Backends) == 0 || cfg.MaxBytes < 0 || cfg.MaxBytes == math.MaxInt64 || cfg.Verification.MaxExpandedBytes < 0 || cfg.Verification.AllowPrivateSigner && cfg.Verification.Anchors == nil {
		return nil, ErrInvalid
	}
	copy := make(map[string]BlobStore, len(cfg.Backends))
	for name, b := range cfg.Backends {
		if !ValidID(name) || b == nil {
			return nil, ErrInvalid
		}
		copy[name] = b
	}
	cfg.Backends = copy
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 2 << 30
	}
	return &Manager{cfg: cfg}, nil
}

// ValidID accepts a short path component used for package, revision and backend IDs.
func ValidID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
