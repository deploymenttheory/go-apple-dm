package audit

import (
	"context"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
)

// Sentinel errors. They mirror the shapes storage and adminauth use so a
// handler can map them to a status without knowing which store answered.
var (
	// ErrNotFound is a record that does not exist.
	ErrNotFound = errors.New("audit: not found")
	// ErrInvalid is a malformed argument: a bad cursor, an empty record, a
	// nil database.
	ErrInvalid = errors.New("audit: invalid argument")
)

// DefaultPageSize applies when Page.Limit is not positive.
const DefaultPageSize = 100

// MaxPageSize bounds one page. A caller asking for more gets this many, so a
// cursor is always cheap to serve.
const MaxPageSize = 1000

// Page requests one page of records. An empty Cursor starts from the newest;
// Limit <= 0 uses DefaultPageSize.
type Page struct {
	Cursor string
	Limit  int
}

// Result is one page of records with the cursor for the next page ("" at the
// end).
type Result[T any] struct {
	Items      []T
	NextCursor string
}

// Query filters a listing. The zero Query matches everything.
type Query struct {
	// Type restricts to one event type.
	Type string
	// Actor restricts to one actor, which is how "what did this admin do"
	// and "what did break-glass do" are asked.
	Actor string
	// Enrollment restricts to one enrollment id.
	Enrollment string
	// Since and Until bound the record time. Zero means unbounded.
	Since, Until time.Time
}

// Record is one persisted event. It is the projection from event/sink after
// it has been reduced to what may leave the process, never the raw payload:
// a TokenUpdate's payload carries the device unlock token, so the trail
// stores what the projection allowed and nothing else.
type Record struct {
	// ID orders the trail and is the pagination cursor. It is assigned by
	// the store, ascending, so the newest record has the highest id.
	ID int64
	// At is when the event happened, from the publisher's clock.
	At time.Time
	// Type is the event type, for example "command-queued".
	Type string
	// Actor is who caused it: "device", "admin", a principal name, or
	// "break-glass".
	Actor string
	// Enrollment is the enrollment the event concerned, zero for events with
	// no enrollment such as an admin action.
	Enrollment mdm.EnrollmentID
	// Fields is the projected payload, stored as JSON.
	Fields map[string]any
}

// Store persists the audit trail.
//
// It is deliberately append-and-prune: there is no update and no delete by
// id. A trail whose rows can be edited answers no question worth asking, and
// the only removal is by age, so retention is a policy rather than a way to
// lose one inconvenient record.
type Store interface {
	// Append writes one record and returns it with its assigned ID.
	// ErrInvalid for a record with no type.
	Append(ctx context.Context, rec Record) (Record, error)
	// List pages the trail newest first, filtered by q. An unparsable
	// cursor is ErrInvalid.
	List(ctx context.Context, q Query, p Page) (Result[Record], error)
	// Get returns one record. ErrNotFound when it does not exist.
	Get(ctx context.Context, id int64) (Record, error)
	// Prune removes records older than before and returns how many went.
	// Retention is the only way a record leaves the trail.
	Prune(ctx context.Context, before time.Time) (int, error)
}

// Size returns the page size to use: the requested limit, defaulted and
// bounded, so every backend agrees without repeating the rule.
func (p Page) Size() int {
	switch {
	case p.Limit <= 0:
		return DefaultPageSize
	case p.Limit > MaxPageSize:
		return MaxPageSize
	default:
		return p.Limit
	}
}

// Matches reports whether rec satisfies q. The in-memory store filters with
// it; the SQL stores build the equivalent WHERE clause, and the contract
// suite is what keeps the two agreeing.
func (q Query) Matches(rec Record) bool {
	switch {
	case q.Type != "" && rec.Type != q.Type:
		return false
	case q.Actor != "" && rec.Actor != q.Actor:
		return false
	case q.Enrollment != "" && rec.Enrollment.ID != q.Enrollment:
		return false
	case !q.Since.IsZero() && rec.At.Before(q.Since):
		return false
	case !q.Until.IsZero() && !rec.At.Before(q.Until):
		return false
	default:
		return true
	}
}
