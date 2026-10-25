package collection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
)

// Operation selects one independently resumable Apple source stream.
type Operation string

const (
	// Devices enumerates organization devices.
	Devices Operation = "devices"
	// Detail fetches one organization device using its opaque Apple resource ID.
	Detail Operation = "detail"
	// Assignment fetches its assigned management service. A successful JSON null is unassigned.
	Assignment Operation = "assignment"
	// Coverage enumerates all coverage resources for one organization device.
	Coverage Operation = "coverage"
	// Version is the checkpoint wire-format version.
	Version = 1
)

// Stream binds checkpoints and evidence to a caller-owned account and enumeration.
// ResourceID is Apple's returned ID, never an inferred serial. Generation must be
// unique for each new enumeration; it is reused when resuming the same enumeration.
type Stream struct {
	AccountID  string    `json:"account_id"`
	Generation string    `json:"generation"`
	Operation  Operation `json:"operation"`
	ResourceID string    `json:"resource_id,omitempty"`
}

// Request configures a stream. Limit is 0 (Apple's default) or 1 through axm.MaxLimit.
// Sparse field selection is intentionally absent: collection retains full resources.
type Request struct {
	Stream
	Limit int
}

// Checkpoint is durable continuation state. Only a successful atomic sink commit
// advances it. Completed enumeration proves traversal of this stream, not device
// deletion, permission to access another stream, or a consistent snapshot at Apple.
type Checkpoint struct {
	Version  int       `json:"version"`
	Stream   Stream    `json:"stream"`
	Pages    int       `json:"pages"`
	Links    axm.Links `json:"links"`
	Meta     axm.Meta  `json:"meta"`
	Seen     []string  `json:"seen,omitempty"`
	Complete bool      `json:"complete"`
}

// Resource preserves a complete source resource, including relationships, unknown
// fields, explicit nulls and numeric lexemes. It is not an inventory device.
type Resource struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"raw"`
}

// Outcome describes an attempted request separately from successful evidence.
// Failure is a bounded classification; it never includes response bodies or URLs.
// HTTPStatus is zero for non-HTTP errors. Err retains the original error in memory.
type Outcome struct {
	At         time.Time `json:"at"`
	Success    bool      `json:"success"`
	Failure    string    `json:"failure,omitempty"`
	HTTPStatus int       `json:"http_status,omitempty"`
	Err        error     `json:"-"`
}

// Batch is one request and its atomic persistence boundary. Document is the raw
// list document, retaining included resources and document-level extensions.
// EnumerationComplete is emitted only for a successfully exhausted Devices or
// Coverage stream. No failure or partial traversal establishes absence.
type Batch struct {
	Stream              Stream
	Before              Checkpoint
	After               Checkpoint
	Resources           []Resource
	Document            json.RawMessage
	Outcome             Outcome
	EnumerationComplete bool
}

// Sink commits resources, outcome and After together, comparing Before to its
// persisted checkpoint to fence concurrent/stale writers. Reapplying an already
// committed batch must be harmless. If Commit reports an uncertain result, reload
// the authoritative checkpoint before resuming. Apple requests run outside Commit.
// Implementations must not retain mutable batch slices without copying them.
type Sink interface {
	Commit(context.Context, Batch) error
}

// SinkFunc adapts a caller's transaction function to Sink.
type SinkFunc func(context.Context, Batch) error

// Commit persists a batch using f.
func (f SinkFunc) Commit(ctx context.Context, b Batch) error {
	if f == nil {
		return fmt.Errorf("%w: nil sink function", axm.ErrArgument)
	}
	return f(ctx, b)
}

// Collector reuses an independently configured Apple client. It owns no store,
// credentials, scheduler or lease. MaxPages bounds each enumeration across restarts;
// zero uses axm.DefaultPageCap. Use one invocation per caller-fenced stream.
type Collector struct {
	Client   *axm.Client
	MaxPages int
}

// Step collects and commits one request. It returns the last confirmed checkpoint
// on request or commit failure. Request errors are committed as failed outcomes
// without changing the checkpoint, then returned unwrapped for errors.Is/As.
// A complete checkpoint performs no requests or commits.
func (c Collector) Step(ctx context.Context, req Request, before Checkpoint, sink Sink) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return before, err
	}
	if err := c.validate(req, before, sink); err != nil {
		return before, err
	}
	if before.Complete {
		return before, nil
	}
	after := before
	if after.Version == 0 {
		after = Checkpoint{Version: Version, Stream: req.Stream}
	}
	maxPages := c.MaxPages
	if maxPages == 0 {
		maxPages = axm.DefaultPageCap
	}
	if after.Pages >= maxPages {
		return before, axm.ErrPageCap
	}
	batch, err := c.fetch(ctx, req, after)
	batch.Stream = req.Stream
	batch.Before = before
	batch.Outcome = Outcome{At: time.Now().UTC(), Success: err == nil, Err: err}
	if err != nil {
		batch.After = before
		batch.Resources, batch.Document = nil, nil
		batch.EnumerationComplete = false
		batch.Outcome.Failure = failure(err)
		var api *axm.Error
		if errors.As(err, &api) {
			batch.Outcome.HTTPStatus = api.Status
		}
	}
	if saveErr := sink.Commit(ctx, batch); saveErr != nil {
		return before, errors.Join(err, saveErr)
	}
	if err != nil {
		return before, err
	}
	return batch.After, nil
}

// Collect traverses a stream through completion, committing each request before
// making the next. Persisted checkpoints can resume with a new Collector instance.
func (c Collector) Collect(ctx context.Context, req Request, checkpoint Checkpoint, sink Sink) (Checkpoint, error) {
	for {
		next, err := c.Step(ctx, req, checkpoint, sink)
		if err != nil {
			return checkpoint, err
		}
		checkpoint = next
		if checkpoint.Complete {
			return checkpoint, nil
		}
	}
}

// validate rejects invalid scope, continuation state and request bounds before I/O.
func (c Collector) validate(req Request, cp Checkpoint, sink Sink) error {
	if c.Client == nil || sink == nil || c.MaxPages < 0 || req.AccountID == "" || req.Generation == "" || req.Limit < 0 || req.Limit > axm.MaxLimit {
		return fmt.Errorf("%w: client, sink, account, generation and valid bounds required", axm.ErrArgument)
	}
	switch req.Operation {
	case Devices:
		if req.ResourceID != "" {
			return fmt.Errorf("%w: device enumeration has no resource ID", axm.ErrArgument)
		}
	case Detail, Assignment, Coverage:
		if req.ResourceID == "" {
			return fmt.Errorf("%w: opaque resource ID required", axm.ErrArgument)
		}
	default:
		return fmt.Errorf("%w: unknown collection operation", axm.ErrArgument)
	}
	if cp.Version == 0 {
		if cp.Stream != (Stream{}) || cp.Pages != 0 || cp.Links != (axm.Links{}) || cp.Meta != (axm.Meta{}) || len(cp.Seen) != 0 || cp.Complete {
			return fmt.Errorf("%w: malformed initial checkpoint", axm.ErrArgument)
		}
	} else if cp.Version != Version || cp.Stream != req.Stream || cp.Pages < 1 || cp.Pages != len(cp.Seen) {
		return fmt.Errorf("%w: checkpoint version, stream or page history differs", axm.ErrArgument)
	}
	return nil
}

// failure classifies attempts without retaining potentially sensitive error text.
func failure(err error) string {
	var api *axm.Error
	if errors.As(err, &api) {
		return fmt.Sprintf("apple_http_%d", api.Status)
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if axm.IsUnauthorized(err) {
		return "authentication"
	}
	if errors.Is(err, axm.ErrNextLink) {
		return "pagination"
	}
	return "transport_or_decode"
}

// fetch performs one source request outside the caller transaction.
func (c Collector) fetch(ctx context.Context, req Request, cp Checkpoint) (Batch, error) {
	switch req.Operation {
	case Devices:
		var page axm.Page[axm.OrgDevice]
		var err error
		if cp.Pages == 0 {
			page, err = c.Client.ListOrgDevices(ctx, axm.ListOptions{Limit: req.Limit})
		} else {
			page, err = axm.NextPage(ctx, c.Client, axm.Page[axm.OrgDevice]{Links: cp.Links, Meta: cp.Meta})
		}
		if err != nil {
			return Batch{}, err
		}
		resources := make([]Resource, 0, len(page.Items))
		for _, item := range page.Items {
			resources = append(resources, Resource{ID: item.ID, Type: item.Type, Raw: item.Raw})
		}
		return pageBatch(cp, page, resources)
	case Coverage:
		var page axm.Page[axm.AppleCareCoverage]
		var err error
		if cp.Pages == 0 {
			page, err = c.Client.ListAppleCareCoverage(ctx, req.ResourceID, axm.ListOptions{Limit: req.Limit})
		} else {
			page, err = axm.NextPage(ctx, c.Client, axm.Page[axm.AppleCareCoverage]{Links: cp.Links, Meta: cp.Meta})
		}
		if err != nil {
			return Batch{}, err
		}
		resources := make([]Resource, 0, len(page.Items))
		for _, item := range page.Items {
			resources = append(resources, Resource{ID: item.ID, Type: item.Type, Raw: item.Raw})
		}
		return pageBatch(cp, page, resources)
	case Detail:
		item, err := c.Client.GetOrgDevice(ctx, req.ResourceID, axm.GetOptions{})
		if err != nil {
			return Batch{}, err
		}
		if item.ID == "" || item.Type == "" {
			return Batch{}, axm.ErrDecode
		}
		return singleBatch(cp, Resource{ID: item.ID, Type: item.Type, Raw: item.Raw}), nil
	default:
		item, err := c.Client.GetAssignedServer(ctx, req.ResourceID, axm.GetOptions{})
		if err != nil {
			return Batch{}, err
		}
		if !bytes.Equal(bytes.TrimSpace(item.Raw), []byte("null")) && (item.ID == "" || item.Type == "") {
			return Batch{}, axm.ErrDecode
		}
		return singleBatch(cp, Resource{ID: item.ID, Type: item.Type, Raw: item.Raw}), nil
	}
}

// pageBatch validates a source document and advances a copied pagination history.
func pageBatch[T any](cp Checkpoint, page axm.Page[T], resources []Resource) (Batch, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(page.Raw, &envelope); err != nil {
		return Batch{}, axm.ErrDecode
	}
	data := bytes.TrimSpace(envelope.Data)
	if len(data) == 0 || data[0] != '[' {
		return Batch{}, axm.ErrDecode
	}
	for _, resource := range resources {
		if resource.ID == "" || resource.Type == "" {
			return Batch{}, axm.ErrDecode
		}
	}

	for _, seen := range cp.Seen {
		if page.Links.Self == seen {
			return Batch{}, axm.ErrNextLink
		}
	}
	cp.Seen = append(append([]string(nil), cp.Seen...), page.Links.Self)
	cp.Pages++
	cp.Links, cp.Meta, cp.Complete = page.Links, page.Meta, !page.HasNext()
	return Batch{After: cp, Resources: resources, Document: page.Raw, EnumerationComplete: cp.Complete}, nil
}

// singleBatch completes a successful singular-resource stream without an enumeration marker.
func singleBatch(cp Checkpoint, resource Resource) Batch {
	cp.Pages, cp.Complete = 1, true
	cp.Seen = []string{string(cp.Stream.Operation)}
	return Batch{After: cp, Resources: []Resource{resource}}
}
