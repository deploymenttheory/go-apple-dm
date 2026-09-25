// Package fault carries the classification, the audience and the stable code that
// travel with a failure, so a caller can react to what went wrong without reading the
// message, and a transport can decide what to publish without knowing which package
// failed.
//
// # Design
//
// A failure has separable parts. The message says what happened and is written for a
// person. The kind says how any caller should treat it — missing, rejected,
// conflicting, unavailable — and names the HTTP status it aligns with, so every
// transport agrees. The audience says who must act: the operator running the server,
// the client calling an API, or the device following a protocol. The code names the
// specific condition, is stable across releases, and is what a client may branch on.
// Prose is not an interface: it is translated, reworded and reformatted, and a client
// matching on it breaks when it is improved.
//
// Sentinels stay with the package that produces them; this package holds only the
// catalogue they point at. A producing package keeps its exported error, so
// errors.Is against it continues to work, while the same error also matches its kind
// and yields its code anywhere in the tree:
//
//	var ErrNotFound = fault.DDMNotFound   // in mdmprotocol/ddm
//
//	errors.Is(err, ddm.ErrNotFound)      // unchanged for existing callers
//	errors.Is(err, fault.NotFound)       // true for every package's not-found
//	fault.CodeOf(err)                    // "DM-DDM-NOT-FOUND"
//	fault.KindOf(err).HTTPStatus()       // 404
//
// An occurrence adds what a sentinel cannot: the operation under way, the cause, and
// structured context a log record or a problem document carries as fields:
//
//	return fault.Wrap(err,
//		fault.WithEntry(fault.DDMNotFound),
//		fault.WithOperation("resolve declaration"),
//		fault.WithAttrs(fault.DeclarationIdentifier(id)))
//
// Wrapping with fmt.Errorf and %w preserves everything: every lookup walks the chain,
// depth first and in operand order, so the first operand of "%w: %w" decides the
// classification and context added on the way out costs nothing.
//
// # Two texts
//
// Error renders everything for the operator's record: the operation, the condition, the
// detail and the whole cause chain. Public renders what a transport may publish: the
// outermost operation, then the detail the raising site wrote with WithDetail, else the
// condition's message, else the kind's text — never a cause that is not one of this
// package's values. A storage driver's message therefore reaches the log and not the
// caller, and what a caller reads is decided by the site that raised the failure:
//
//	return fault.New(fault.AXMLimitInvalid,
//		fault.WithDetail("the page limit must be between 1 and %d", max))
//
// # Audiences
//
// Operator conditions are declared by the package that raises them with NewOperator.
// They carry no code, because they are never published: the constructor refuses one.
// Misconfiguration a package detects at construction is an operator condition of kind
// Internal. Client conditions are declared in a catalogue file of the module that
// publishes them, one file per domain, with NewClient, because their codes are promised
// to API callers and must be unique, enumerable and reviewable as a set; this package
// holds the library's, and the reference server holds its own. Device conditions are
// declared with NewDevice and carry Apple's own error-document identifier or none,
// because a device acts on a status and on Apple's documents, never on this server's
// vocabulary; the ones a transport raises are gathered in device.go, and the ones a
// protocol package raises sit beside it. An unclassified failure is treated as an
// operator failure, so nothing becomes publishable by omission; its kind is taken from
// its shape when the standard library gives it one (a cancelled context is Cancelled, a
// timeout is DeadlineExceeded) and is Internal otherwise.
//
// An answer from an Apple service is classified by KindForUpstreamStatus, the one rule
// every client of one shares.
//
// # Logging
//
// Every value here implements slog.LogValuer, and Attr renders any error, wrapped or
// not, as the "error" group a boundary logs: message, kind, code, audience, operation
// and the merged attributes. ErrorType gives the bounded error.type value a metric or
// a span records. The package installs no logger and captures no stack unless asked.
//
// # Codes
//
// A client code is DM-<DOMAIN>-<NOUN>-<CONDITION>, upper case, hyphen separated, and
// spells the identifier that declares it: DDMDeclarationInvalid is
// DM-DDM-DECLARATION-INVALID, which a layout test checks. The domain is Apple's current
// name for the thing (ADE, AXM, APPSBOOKS, …) and the condition word has one meaning:
// INVALID is understood and rejected, MALFORMED could not be parsed, TOO-LARGE is a size
// bound, LIMIT-EXCEEDED a rate or quota bound. Once published a code is never reused for
// a different condition and never renamed; a condition that splits gains new codes and
// retires the old one. A device code is Apple's identifier verbatim. Catalogue lists
// every coded condition and Lookup turns a code back into its condition.
//
// # References
//
//   - Decision record 0061: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0061-error-classification-and-codes.md
//   - Google API design guide, errors: https://cloud.google.com/apis/design/errors
//   - OpenTelemetry semantic conventions, recording errors: https://opentelemetry.io/docs/specs/semconv/general/recording-errors/
//   - Apple, MDM error responses: https://developer.apple.com/documentation/devicemanagement/errorcodeunrecognizeddevice
package fault
