package audittest

import (
	"context"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/audit"
)

// ErrFailing is what a Failing store returns for the method it is set to
// fail.
var ErrFailing = errors.New("audittest: injected failure")

// Failing wraps a store and fails one named method, so a caller's error path
// can be reached without a broken database.
type Failing struct {
	audit.Store
	// Fail names the method to fail: "Append", "List", "Get", or "Prune".
	// Empty passes everything through.
	Fail string
}

func (f *Failing) fails(name string) bool { return f.Fail == name }

// Append implements audit.Store.
func (f *Failing) Append(ctx context.Context, rec audit.Record) (audit.Record, error) {
	if f.fails("Append") {
		return audit.Record{}, ErrFailing
	}
	return f.Store.Append(ctx, rec)
}

// List implements audit.Store.
func (f *Failing) List(ctx context.Context, q audit.Query, p audit.Page) (audit.Result[audit.Record], error) {
	if f.fails("List") {
		return audit.Result[audit.Record]{}, ErrFailing
	}
	return f.Store.List(ctx, q, p)
}

// Get implements audit.Store.
func (f *Failing) Get(ctx context.Context, id int64) (audit.Record, error) {
	if f.fails("Get") {
		return audit.Record{}, ErrFailing
	}
	return f.Store.Get(ctx, id)
}

// Prune implements audit.Store.
func (f *Failing) Prune(ctx context.Context, before time.Time) (int, error) {
	if f.fails("Prune") {
		return 0, ErrFailing
	}
	return f.Store.Prune(ctx, before)
}
