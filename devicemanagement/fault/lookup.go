package fault

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"
)

// Classified is an error that knows its own kind. Every value this package declares
// implements it, and a package whose failures come from a remote service — an Apple
// service's HTTP status, an APNs reason — implements it on its own error type so a
// boundary can classify the failure without matching that package's sentinels.
type Classified interface {
	Kind() *Kind
}

// Delayed is an error that knows when a retry may succeed, such as a response that
// carried a Retry-After header.
type Delayed interface {
	RetryDelay() time.Duration
}

// Catalogued is an error that stands for a catalogued condition without being one: a
// transport's own error type that maps its internal code onto an entry. EntryOf
// consults it before descending, so the type's condition wins over the cause's.
type Catalogued interface {
	Entry() *Entry
}

// maxDepth bounds a walk. A chain deeper than this is a bug in the producer, not
// context worth reading, and an error type whose Unwrap returns itself must not hang
// every lookup.
const maxDepth = 100

// walk visits err and everything it wraps, depth first and in operand order, stopping
// when visit returns true. Operand order is what makes the first operand of
// fmt.Errorf("%w: %w", a, b) decide a classification, and what lets an occurrence's
// own condition win over the cause it wraps.
func walk(err error, visit func(error) bool) bool {
	return walkDepth(err, visit, 0)
}

// walkDepth is walk with the depth so far.
func walkDepth(err error, visit func(error) bool, depth int) bool {
	if err == nil || depth >= maxDepth {
		return false
	}
	if visit(err) {
		return true
	}
	switch u := err.(type) { //nolint:errorlint // walk visits every node of the chain itself; errors.As here would skip ahead
	case interface{ Unwrap() error }:
		return walkDepth(u.Unwrap(), visit, depth+1)
	case interface{ Unwrap() []error }:
		for _, inner := range u.Unwrap() {
			if walkDepth(inner, visit, depth+1) {
				return true
			}
		}
	}
	return false
}

// EntryOf returns the first catalogued condition in err's chain, so a transport can
// read the condition's own title separately from the message this occurrence
// assembled.
func EntryOf(err error) (*Entry, bool) {
	var found *Entry
	walk(err, func(e error) bool {
		switch v := e.(type) { //nolint:errorlint // walk visits every node of the chain itself; errors.As here would skip ahead
		case *Entry:
			found = v
		case Catalogued:
			found = v.Entry()
		}
		return found != nil
	})
	return found, found != nil
}

// KindOf returns the classification of the first classified error in err's chain. An
// unclassified failure is classified by its shape when the standard library gives it
// one — a cancelled context is Cancelled, an expired context or a timed-out network
// operation is DeadlineExceeded — and is otherwise Internal, because a condition nobody
// classified is one a caller cannot be told how to handle. A nil error has no kind.
func KindOf(err error) *Kind {
	if err == nil {
		return nil
	}
	var found *Kind
	walk(err, func(e error) bool {
		if c, ok := e.(Classified); ok {
			found = c.Kind()
		}
		return found != nil
	})
	if found != nil {
		return found
	}
	return shapeOf(err)
}

// shapeOf classifies an unclassified failure by what the standard library says about
// it, walking the chain itself so that a malformed chain stays bounded.
func shapeOf(err error) *Kind {
	found := Internal
	walk(err, func(e error) bool {
		switch {
		case e == context.Canceled: //nolint:errorlint // walk visits every node; identity is the comparison errors.Is would make
			found = Cancelled
		case e == context.DeadlineExceeded: //nolint:errorlint // as above
			found = DeadlineExceeded
		default:
			if netErr, ok := e.(net.Error); ok && netErr.Timeout() { //nolint:errorlint // as above
				found = DeadlineExceeded
			} else {
				return false
			}
		}
		return true
	})
	return found
}

// KindOverridden reports whether the classification KindOf returns was chosen by a
// boundary with WithKind rather than declared by a condition: the first classified
// error in the chain is an occurrence carrying an explicit kind. A transport uses it to
// tell a handler's decision from a condition's default.
func KindOverridden(err error) bool {
	var found, overridden bool
	walk(err, func(e error) bool {
		if occ, ok := e.(*Error); ok && occ.kind != nil { //nolint:errorlint // walk visits every node of the chain itself; errors.As here would skip ahead
			found, overridden = true, true
			return true
		}
		if c, ok := e.(Classified); ok && c.Kind() != nil {
			found = true
			return true
		}
		return false
	})
	return found && overridden
}

// IsClassified reports whether something in err's chain declared a kind, as opposed to
// KindOf classifying it by shape or falling back to Internal.
func IsClassified(err error) bool {
	return walk(err, func(e error) bool {
		c, ok := e.(Classified)
		return ok && c.Kind() != nil
	})
}

// CodeOf returns the code of the first catalogued condition in err's chain, or empty
// when the failure was never catalogued. Wrapping preserves it.
func CodeOf(err error) Code {
	if e, ok := EntryOf(err); ok {
		return e.code
	}
	return ""
}

// AudienceOf returns the audience of the first catalogued condition in err's chain. An
// uncatalogued failure is an Operator failure: nobody decided it was safe to publish,
// so it is treated as the deployment's problem and its detail is withheld.
func AudienceOf(err error) Audience {
	if e, ok := EntryOf(err); ok {
		return e.audience
	}
	return Operator
}

// AttrsOf merges the structured context of every occurrence in err's chain. The
// outermost occurrence wins when two set the same key, because it saw the most.
func AttrsOf(err error) []slog.Attr {
	var out []slog.Attr
	seen := map[string]bool{}
	walk(err, func(e error) bool {
		if occ, ok := e.(*Error); ok { //nolint:errorlint // walk visits every node of the chain itself; errors.As here would skip ahead
			for _, a := range occ.attrs {
				if !seen[a.Key] {
					seen[a.Key] = true
					out = append(out, a)
				}
			}
		}
		return false
	})
	return out
}

// RetryAfterOf returns the first retry delay in err's chain, or zero when nothing in
// the chain knows one.
func RetryAfterOf(err error) time.Duration {
	var found time.Duration
	walk(err, func(e error) bool {
		switch v := e.(type) { //nolint:errorlint // walk visits every node of the chain itself; errors.As here would skip ahead
		case *Error:
			found = v.retryAfter
		case Delayed:
			found = v.RetryDelay()
		}
		return found > 0
	})
	return found
}

// StackOf returns the first captured stack in err's chain, or nil.
func StackOf(err error) []string {
	var found []string
	walk(err, func(e error) bool {
		if occ, ok := e.(*Error); ok { //nolint:errorlint // walk visits every node of the chain itself; errors.As here would skip ahead
			found = occ.Stack()
		}
		return found != nil
	})
	return found
}

// Public renders the text a transport may publish for err: the outermost operation,
// then the detail the raising site wrote for the audience, else the catalogued
// condition's message, else the kind's text. A cause that is not one of this package's
// values never appears, so a storage or operating-system error wrapped under a client
// condition is explained to the caller by the condition and kept for the operator by
// Error. A nil error renders as empty.
func Public(err error) string {
	if err == nil {
		return ""
	}
	var op, detail string
	walk(err, func(e error) bool {
		occ, ok := e.(*Error) //nolint:errorlint // walk visits every node of the chain itself
		if !ok {
			return false
		}
		if op == "" {
			op = occ.op
		}
		if detail == "" {
			detail = occ.detail
		}
		return detail != "" && op != ""
	})
	var parts []string
	if op != "" {
		parts = append(parts, op)
	}
	switch {
	case detail != "":
		parts = append(parts, detail)
	default:
		if entry, ok := EntryOf(err); ok {
			parts = append(parts, entry.message)
		} else {
			parts = append(parts, KindOf(err).text)
		}
	}
	return strings.Join(parts, ": ")
}
