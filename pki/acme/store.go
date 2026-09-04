package acme

import (
	"context"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/paging"
)

// Storage errors. A backend reports these; the server turns them into
// problem documents.
var (
	// ErrNotFound is a record that does not exist.
	ErrNotFound = errors.New("acme: not found")
	// ErrConflict is a write that lost a race for a unique value: a second
	// account for one key, or a second order for one client identifier.
	// The server relies on the backend to detect this rather than reading
	// first and writing after, because a read-then-write cannot be correct
	// under concurrency.
	ErrConflict = errors.New("acme: conflict")
	// ErrInvalid is a record a backend will not store.
	ErrInvalid = errors.New("acme: invalid record")
)

// Reader is the read half of the store, available both directly and inside
// a transaction.
type Reader interface {
	GetAccount(ctx context.Context, id string) (*Account, error)
	// AccountByThumbprint finds the account for a key. RFC 8555 makes a key
	// the identity of an account, so this is how a returning client is
	// recognised.
	AccountByThumbprint(ctx context.Context, thumbprint string) (*Account, error)
	GetOrder(ctx context.Context, id string) (*Order, error)
	GetAuthorization(ctx context.Context, id string) (*Authorization, error)
	GetChallenge(ctx context.Context, id string) (*Challenge, error)
	GetCertificate(ctx context.Context, id string) (*Certificate, error)
	ListOrders(
		ctx context.Context,
		accountID string,
		page paging.Page,
	) (paging.Result[Order], error)
	ListCertificates(
		ctx context.Context,
		q CertificateQuery,
		page paging.Page,
	) (paging.Result[Certificate], error)
}

// Writer is the write half, available only inside a transaction. Every put
// replaces the whole record.
type Writer interface {
	PutAccount(ctx context.Context, a *Account) error
	PutOrder(ctx context.Context, o *Order) error
	PutAuthorization(ctx context.Context, a *Authorization) error
	PutChallenge(ctx context.Context, c *Challenge) error
	PutCertificate(ctx context.Context, c *Certificate) error
	// ClaimIdentifier records that a client identifier has been used, and
	// returns ErrConflict if it already was. Apple describes the
	// ClientIdentifier as an anti-replay code and a one-time code, so a
	// second order for the same value is refused. The claim is taken in the
	// same transaction that creates the order, so two concurrent orders
	// cannot both succeed.
	//
	// A repeat claim conflicts even when the order is the same: reaching
	// the claim means the order was created, so a client that retries after
	// a timeout has an order already and should read it rather than make
	// another. Claims are never released, including by Prune, because
	// releasing a one-time code reopens the replay it exists to prevent.
	ClaimIdentifier(ctx context.Context, identifier, orderID string) error
}

// Tx is a transaction. Everything a request changes happens inside one, so
// an order, its authorization, its challenge, and the claim on its
// identifier either all exist or none do.
type Tx interface {
	Reader
	Writer
}

// Store is the ACME state: accounts, orders, authorizations, challenges,
// issued certificates, and nonces.
//
// Nonces are outside the transaction deliberately. Every signed request
// takes one, they carry no relationship to anything else, and taking one
// has to be atomic on its own, so they are their own two methods rather
// than a transaction each.
type Store interface {
	Reader
	// Update runs fn in a transaction, retrying nothing: a caller that
	// loses a race sees ErrConflict and answers the client. The callback
	// must do all its work through the Tx it is given; reaching back into
	// the Store from inside it is not supported and a backend may deadlock.
	Update(ctx context.Context, fn func(Tx) error) error
	// PutNonce stores a freshly minted nonce. Values are random and
	// therefore unique in practice; a backend may either overwrite a
	// duplicate or report ErrConflict, and no caller relies on which.
	PutNonce(ctx context.Context, n Nonce) error
	// TakeNonce removes a nonce and returns it, atomically. A nonce that is
	// not there is ErrNotFound, which is how a replay is detected: the
	// first use removed it. Expiry is judged by the caller against
	// IssuedAt, so a backend needs no clock of its own.
	TakeNonce(ctx context.Context, value string) (*Nonce, error)
	// Prune removes nonces issued before the given time, and orders and
	// authorizations that expired before it. A challenge goes with the
	// authorization it belongs to, having no lifetime of its own. A record
	// with a zero expiry never expires and is kept. Issued certificates,
	// accounts, and identifier claims are never pruned: the first two are
	// the record of what was given out, and the third is what stops a
	// client identifier being used twice.
	//
	// The count is every record removed, cascaded ones included.
	Prune(ctx context.Context, before time.Time) (int, error)
}
