package fault

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Error is one occurrence of a failure: the catalogued condition it is an instance of,
// the cause it wraps, the operation that was under way, and the structured context a
// log record or a problem document can carry as fields rather than as prose.
//
// An occurrence is built with New or Wrap and options. It satisfies errors.Is for its
// condition and its kind, unwraps to both the condition and the cause, and renders as
// a slog group through LogValue. Wrapping it further with fmt.Errorf and %w keeps every
// lookup in this package working, because they walk the whole chain.
type Error struct {
	entry      *Entry
	kind       *Kind
	cause      error
	op         string
	detail     string
	attrs      []slog.Attr
	retryAfter time.Duration
	stack      []uintptr
	wantStack  bool
}

// Option configures an occurrence.
type Option func(*Error)

// WithEntry sets the catalogued condition the occurrence is an instance of. Wrap uses
// it to classify a cause that was raised without one, such as a storage error that a
// device-facing service reports as an unrecognized enrollment.
func WithEntry(e *Entry) Option { return func(err *Error) { err.entry = e } }

// WithKind classifies an occurrence without cataloguing it. It is for a boundary that
// knows how a caller should treat a failure — a parse error is the caller's request
// being wrong — but has no condition worth a published code. When both an entry and a
// kind are set, the kind wins for KindOf and for errors.Is against the occurrence; the
// condition itself, reachable through the chain, keeps matching its own kind.
func WithKind(k *Kind) Option { return func(err *Error) { err.kind = k } }

// WithOperation names what was under way, in the form a person reads: "read setup
// file". It leads the rendered message and is never a package name, because the
// binary that renders the error names itself and a label per layer stacks into a
// chain that states the call path instead of the problem.
func WithOperation(op string) Option { return func(err *Error) { err.op = op } }

// WithDetail sets prose about this occurrence that is safe for its audience to read:
// "the limit must be between 1 and 1000", "DeviceID is required". It is what Public
// renders in place of the condition's general message, and it never carries a value
// the audience did not supply. The cause a transport must not publish goes in Wrap's
// first argument instead.
func WithDetail(format string, args ...any) Option {
	return func(err *Error) { err.detail = fmt.Sprintf(format, args...) }
}

// WithAttrs attaches structured context: the enrollment, the declaration, the request.
// Keys are the vocabulary in attr.go and values are Apple's identifiers verbatim.
func WithAttrs(attrs ...slog.Attr) Option {
	return func(err *Error) { err.attrs = append(err.attrs, attrs...) }
}

// WithRetryAfter records when a retry may succeed, for a transport to publish as a
// Retry-After header and for a worker to schedule by.
func WithRetryAfter(d time.Duration) Option { return func(err *Error) { err.retryAfter = d } }

// WithStack captures the call stack at the point of construction. It is off by default:
// most failures are classified well enough to act on without one, and capture costs a
// walk of the goroutine's frames. There is no process-wide switch, so a library can
// never be made to capture on a consumer's behalf.
func WithStack() Option { return func(err *Error) { err.wantStack = true } }

// New returns an occurrence of a catalogued condition. The entry is required: an
// occurrence without a condition would be an error with nothing to classify it, which
// is what Wrap with WithKind is for.
func New(entry *Entry, opts ...Option) *Error {
	if entry == nil {
		panic("fault: New needs a catalogued condition")
	}
	e := &Error{entry: entry}
	return e.apply(opts)
}

// Wrap returns an occurrence around a cause, or nil when the cause is nil so a call
// site can wrap unconditionally. Without options it adds nothing but a place to hang
// context; with WithEntry or WithKind it classifies a cause that carried no
// classification of its own.
func Wrap(cause error, opts ...Option) error {
	if cause == nil {
		return nil
	}
	e := &Error{cause: cause}
	return e.apply(opts)
}

// apply runs the options and captures the stack when one asked for it.
func (e *Error) apply(opts []Option) *Error {
	for _, opt := range opts {
		opt(e)
	}
	if e.wantStack {
		pcs := make([]uintptr, 32)
		// Skip runtime.Callers, apply and its caller (New or Wrap), so the first frame
		// is the site that raised the failure.
		n := runtime.Callers(3, pcs)
		e.stack = pcs[:n]
	}
	return e
}

// Error renders the operation, the condition, the detail and the cause, joined by
// ": ", without repeating a condition the cause already states. This is the text for
// the operator's record; a transport publishes Public instead.
func (e *Error) Error() string {
	var parts []string
	if e.op != "" {
		parts = append(parts, e.op)
	}
	switch {
	case e.cause == nil:
		if e.entry != nil {
			parts = append(parts, e.entry.message)
		} else if e.kind != nil {
			parts = append(parts, e.kind.text)
		}
		if e.detail != "" {
			parts = append(parts, e.detail)
		}
	case e.entry != nil && !errors.Is(e.cause, e.entry):
		parts = append(parts, e.entry.message)
		if e.detail != "" {
			parts = append(parts, e.detail)
		}
		parts = append(parts, e.cause.Error())
	default:
		if e.detail != "" {
			parts = append(parts, e.detail)
		}
		parts = append(parts, e.cause.Error())
	}
	return strings.Join(parts, ": ")
}

// Unwrap returns the condition, then the cause, so errors.Is and errors.As reach both
// and a lookup that walks depth-first meets the occurrence's own classification before
// anything the cause carries.
func (e *Error) Unwrap() []error {
	out := make([]error, 0, 2)
	if e.entry != nil {
		out = append(out, e.entry)
	}
	if e.cause != nil {
		out = append(out, e.cause)
	}
	return out
}

// Is matches the occurrence's condition and its effective kind.
func (e *Error) Is(target error) bool {
	if e.entry != nil && target == e.entry {
		return true
	}
	k := e.Kind()
	return k != nil && target == k
}

// Kind returns the effective classification: an explicit kind, else the condition's,
// else nil for an occurrence that only carries context and leaves classification to
// its cause.
func (e *Error) Kind() *Kind {
	if e.kind != nil {
		return e.kind
	}
	if e.entry != nil {
		return e.entry.kind
	}
	return nil
}

// Entry returns the catalogued condition, or nil.
func (e *Error) Entry() *Entry { return e.entry }

// Operation returns what was under way, or empty.
func (e *Error) Operation() string { return e.op }

// Detail returns the audience-safe prose WithDetail attached, or empty.
func (e *Error) Detail() string { return e.detail }

// Attrs returns the structured context attached to this occurrence alone. AttrsOf
// merges the whole chain.
func (e *Error) Attrs() []slog.Attr { return append([]slog.Attr(nil), e.attrs...) }

// RetryAfter returns when a retry may succeed, or zero.
func (e *Error) RetryAfter() time.Duration { return e.retryAfter }

// Stack returns the captured frames as "function file:line" lines, innermost first, or
// nil when WithStack was not given.
func (e *Error) Stack() []string {
	if len(e.stack) == 0 {
		return nil
	}
	frames := runtime.CallersFrames(e.stack)
	var out []string
	for {
		f, more := frames.Next()
		if f.Function != "" {
			out = append(out, f.Function+" "+f.File+":"+strconv.Itoa(f.Line))
		}
		if !more {
			return out
		}
	}
}

// LogValue renders the occurrence as a group: message, kind, code, audience, operation,
// detail, the attached attributes, retry_after and stack when present.
func (e *Error) LogValue() slog.Value { return valueOf(e) }
