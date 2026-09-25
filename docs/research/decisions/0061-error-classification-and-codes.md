# 0061: Error classification, audiences and published codes

## Context

A failure is read by more than one reader, and they need different things from it.

The person running the server needs to know which setting is wrong and what to change. A
client calling the administration API needs to know whether to retry, correct its request
or give up, and needs that answer to survive rewording of the message. A device follows a
protocol whose failure format Apple and the IETF already specify.

Nothing separated those readers. One value served all of them: an error was a message,
and callers reacted to it by matching a sentinel belonging to whichever package happened
to raise it. Three consequences followed.

Classification was per package. Sixteen packages export `ErrNotFound`, twenty export
`ErrInvalid`, nine `ErrConflict`. A boundary that maps failures onto HTTP statuses had to
name each package's sentinels, so it named one — declarative management — and every other
package's missing record answered 500. Packages with nothing to do with declarations
returned `ddm.ErrInvalid` to be classified correctly, which is how a vocabulary spreads
without ever being designed.

The message was the contract. The administration API answered `{"Error": "<text>"}` with
no other machine-readable field, so a client with different behavior per condition had to
match on prose. Prose is written for a person: it gets clarified, reworded and
translated, and each improvement is a silent breaking change.

Audience was not represented. One sentinel described both a startup misconfiguration an
operator fixes and a runtime failure a client sees, so nothing decided which detail was
safe to publish beyond the blanket rule that 5xx bodies say "internal error".

## Decision

A failure carries three separable parts. The **message** says what happened, for a
person. The **kind** says how any caller should treat it. The **code** names the specific
condition and is stable across releases.

`devicemanagement/fault` holds the classification and the catalogue of the conditions the
library raises. It is in the library because the library raises most conditions and the
server must not own vocabulary its own consumers need. It depends on nothing else in the
module (a layout test enforces it), only on the standard library, and knows nothing of
HTTP, because the library is embedded in other people's servers and must not dictate their
wire format. Conditions only the reference server raises — administrative principals,
audit records, events, webhook destinations — are catalogued by the server, in
`server/problem`, with the same constructors; the registry is one, so their codes are
checked for uniqueness against the library's.

### Audience

Every catalogued condition declares who acts on it, and the constructor enforces the
consequence rather than a review or a test doing it later.

| Audience | Who acts | Reported through | Published code |
|---|---|---|---|
| `Operator` | the person running the server | terminal, logs; the catalogue message alone when met through the API | never — `NewOperator` takes no code |
| `Client` | the API caller, whether a person's tool or another service | HTTP problem document | always — `NewClient` requires a `DM-` code |
| `Device` | an Apple device on a protocol route | the status, or Apple's error document | Apple's own identifier or none — `NewDevice` refuses a `DM-` code |

A device is a third audience because the transport must decide what a device may be
told, and Apple decides the vocabulary: the five error documents under `mdm/errors`
(`com.apple.unrecognized.device`, `com.apple.softwareupdate.required`,
`com.apple.psso.required`, `com.apple.watch.pairing.token.missing`,
`com.apple.well-known.failed`) carry `code`, `description` and `message` as Apple
defines them, and everything else a device meets is a bare status. A device condition
without an Apple document is declared by the package that raises it, like an operator
condition, since it has no code to catalogue. `ErrorChain` is not a server-to-device
format: it is what a device sends in a command response, and it is stored verbatim.

Service-to-service failures are not a fourth audience. An outbound call that failed is
consumed first by this server, which decides whether to retry, and then reported to the
operator or relayed to the API caller as 502, 503 or 504 with `Retry-After`. That is a
matter of kind and retry metadata, so the kinds `Upstream`, `Unavailable`,
`DeadlineExceeded` and `ResourceExhausted` exist, an occurrence may carry
`WithRetryAfter`, and an upstream client's own error type classifies itself through the
`Classified` and `Delayed` interfaces while keeping Apple's fields verbatim.

An unclassified failure is treated as `Operator`, so nothing becomes publishable by
omission.

An operator condition is often met by the operator through the administration API, since
the person running `dmctl` is the operator: an expired enrollment token, unsigned terms in
Apple Business Manager, a push certificate that has lapsed. Such a failure is answered with
its 5xx status and the catalogue message as `title`, because that message is written by
hand and names no runtime value, and never with the occurrence's chain. The caller learns
what to fix without learning how the deployment is built.

Operator conditions are declared by the package that raises them: they carry no code, so
there is no registry to maintain and no reason to separate them from the contract they
belong to. Client conditions are declared in this package's catalogue files, one per
domain, because their codes are published and must be unique, enumerable and reviewable
as a set before they are promised to anyone.

### Kinds

The kinds take their names from the canonical error codes gRPC and Google's API design
guide share, because API clients already understand that vocabulary, but the set is
HTTP's: each kind is one status a transport answers with. So `Conflict`, `Gone`,
`PayloadTooLarge`, `UnsupportedMediaType` and `Upstream` exist where gRPC has
`AlreadyExists`, `Aborted` and `FailedPrecondition`, and gRPC's `DataLoss` and
`OutOfRange` do not. The set is `NotFound`, `InvalidArgument`, `Conflict`,
`Unauthenticated`, `PermissionDenied`, `ResourceExhausted`, `Unimplemented`,
`Unavailable`, `DeadlineExceeded`, `Upstream`, `Gone`, `PayloadTooLarge`,
`UnsupportedMediaType`, `Internal` and `Cancelled`. Each kind names the status it aligns
with as a plain integer, so every transport agrees and the package still knows nothing of
`net/http`. `Gone` exists because Apple's protocol relies on 410 to end a user channel.
`Cancelled` carries 499, the number nginx logs for a caller that went away: it is not a
failure of the server, so it is never counted as one, and a transport that has no client
left to answer closes the connection.

A failure nobody classified is classified by its shape when the standard library gives
it one — a cancelled context is `Cancelled`, an expired context or a network timeout is
`DeadlineExceeded` — and is `Internal` otherwise. `error.type` is therefore one value
wherever the same failure is recorded: `fault.ErrorType` feeds the log record, the
counter and the span alike.

An answer from an Apple service is classified by one rule every client shares,
`KindForUpstreamStatus`: a 400, 404 or 409 is the caller's, because this server relayed
the caller's request and Apple's verdict on it; a 429 is `ResourceExhausted` with Apple's
`Retry-After`; a 408 or 504 is `DeadlineExceeded`; a 401 or 403 is the deployment's
credential and is `Unavailable`, since the caller cannot correct it and a retry will not
help until an operator acts; anything else is `Upstream`. Misconfiguration a library
package detects at construction is `Internal`, whatever its shape, because it is the
deployment's and is never published.

### Occurrences carry structure, and two texts

A sentinel says what condition occurred; an occurrence (`fault.New`, `fault.Wrap`) adds
the operation under way, the cause, the detail written for the audience, structured
attributes with a shared key vocabulary (`enrollment.id`, `declaration.identifier`,
`command.uuid`, `request.id`, …), when a retry may succeed, and optionally the stack.

An occurrence has two renderings. `Error()` is for the operator's record: the operation,
the condition, the detail and the whole cause chain, so a storage driver's message is
never lost. `fault.Public` is for a transport: the outermost operation, then the detail
the raising site wrote with `WithDetail`, else the catalogued condition's message, else
the kind's text, and never a cause that is not one of this package's values. A transport
publishes `Public` and logs `Error()`, so publication is decided by what a site chose to
say to its audience, not by what happened to be in the chain. Every value implements `slog.LogValuer` and
`fault.Attr` renders any error, wrapped or not, as the `error` group a boundary logs.
Lookups walk the chain depth-first in operand order, so the first operand of
`fmt.Errorf("%w: %w", a, b)` classifies, and an occurrence's own condition wins over the
cause it wraps. A boundary may re-classify with `WithKind`; `KindOverridden` tells a
transport that it did.

### Sentinels stay with their producer

A package keeps its exported error and points it at its catalogue entry:

```go
var ErrNotFound = fault.DDMNotFound
```

`errors.Is(err, ddm.ErrNotFound)` is unchanged for every existing caller, while the same
error now also matches `fault.NotFound` and yields `DM-DDM-NOT-FOUND` anywhere in the
tree. Moving the sentinels instead would have forced sixteen `ErrNotFound` into one
namespace as `DDMNotFound`, `ACMENotFound`, `InventoryNotFound` — re-encoding the package
name into identifiers that Go's package qualifier already distinguishes — and would have
broken every consumer of a published module for no gain in what anyone reads.

`CodeOf`, `KindOf` and `AudienceOf` walk the chain, so context added with `%w` on the way
out never costs the classification.

### Codes

A code is `DM-<DOMAIN>-<NOUN>-<CONDITION>`, upper case, hyphen separated, and spells the
Go identifier that declares it: `DDMDeclarationInvalid` is `DM-DDM-DECLARATION-INVALID`.
A layout test derives one from the other. The domain is Apple's current name for the
thing, abbreviated where the repository already does: `ADE` for Automated Device
Enrollment (the package keeps its historical name `dep`), `AXM` for the Apple School and
Business Manager API, `APPSBOOKS` for Apps and Books. The condition word has one meaning:
`INVALID` is understood and rejected, `MALFORMED` could not be parsed, `TOO-LARGE` is a
size bound, `LIMIT-EXCEEDED` a rate or quota bound. Once published a code is never reused
for a different condition and never renamed; a condition that splits gains new codes and
retires the old one.

### The administration API answers with RFC 9457

Problem documents, `application/problem+json`:

```json
{
  "type":   "urn:deploymenttheory:apple-dm:error:ddm-not-found",
  "title":  "the declaration, declaration set or enrollment does not exist",
  "status": 404,
  "detail": "resolve declaration \"com.acme.settings\": the declaration, declaration set or enrollment does not exist"
}
```

`type` is the stable identifier a client matches. `title` describes the condition and does
not vary between occurrences. `detail` is `fault.Public` of this occurrence: the operation
and the prose written for the caller, never the cause.

A URN is used rather than an HTTP URL because RFC 9457 does not require the type to be
dereferenceable, and a URL would promise a page at a domain no deployment controls. It
follows the form ACME already uses in this repository,
`urn:ietf:params:acme:error:<code>`.

Publication follows the audience and the status:

- **5xx** publishes the status and nothing else. The deployment is at fault; the client
  can neither diagnose nor act on the cause and must not be led to depend on it.
- **4xx** always publishes `detail`. The caller sent something this server rejected and
  cannot correct it without being told what. Whether the condition is catalogued decides
  only whether a machine-matchable `type` accompanies it: an uncatalogued rejection is
  still explained, it simply has nothing stable to match on yet.

That distinction matters during migration. Treating "not yet catalogued" as "unfit to
publish" would silently remove the explanation from every rejection not yet in the
catalogue.

### Sentinels that stay plain

`pki/pushcert` keeps `errors.New`: the layout test forbids it any in-module import so it
stays usable on its own. Test scaffolding packages are not migrated.

### One program prefix

The binary that renders an error names itself, and nothing below it does. The
per-package `wrapError`/`wrap` helpers are gone, and a layout test refuses an error text
or a log message in either module that leads with a package label. A label per layer stacked into a chain that stated the
call path rather than the problem:

```text
dmctl: dmctl: app: app: app: private file: <syscall error>
dmctl: cannot create /data/secrets/admin: permission denied
```

Wrap sites add the operation and its operand — `fmt.Errorf("read setup file %q: %w", path,
err)` — never their own identity.

## Rationale

Kinds are few and carry no domain, so one boundary maps them onto statuses without knowing
which package failed. That is what makes classification composable: the alternative, a
handler naming each package's sentinels, is what produced a 500 for every unlisted
package.

Audience is a property of the condition, not of the moment it is reported, so it is
declared where the condition is declared. Making it a constructor argument would have
allowed an operator condition to carry a code; making it two constructors means the
compiler refuses.

No third-party error library was adopted. `github.com/pkg/errors` has been archived since
2020. `github.com/cockroachdb/errors` brings fifteen direct dependencies against this
module's eleven, for wire encoding and redaction aimed at distributed databases.
`github.com/joomcode/errorx` offers the closest match in namespaces and types, but its
types would appear throughout a published library's API. The standard library already
provides wrapping, `Is`, `As`, `Join` and `AsType`; what was missing was a catalogue and
a small classification layer.

## Constraints

The catalogue's codes are a published interface. Adding one is routine; changing what one
means is not, and requires a new code.

Most conditions are not catalogued. That is intended: only conditions a client reacts to
need a code, and an uncatalogued rejection still explains itself. The set grows as
conditions prove worth matching.

`title` is the catalogue message and describes the condition. `DM-DDM-NOT-FOUND` still
covers a declaration, a declaration set and an enrollment; splitting it needs every
declarative store backend to say which it failed to find, and is deferred.

Kinds and audience are not a security boundary. They decide what is published by default;
redaction of event and audit payloads remains with `server/eventsink` and decision record
0037.

## Verification

- `devicemanagement/fault` tests cover identity and kind matching, survival of wrapping,
  the operand-order rule, kind overrides, attribute merging, retry delay, stack capture,
  rendering through `slog`, and the defaults for an unclassified failure.
- Every migrated package pins each sentinel to its kind, audience and code in an
  `errors_test.go`; `internal/layout` checks that the documented catalogue equals the
  code's and that every catalogued condition is raised.
- `server/problem` tests cover the wire shapes: a catalogued client condition with a
  stable type, an uncatalogued rejection that keeps its detail, a server error that
  publishes nothing beyond its status, an operator condition with a request kind that
  is still a 500, and a device condition that is a bare status.
- `server/internal/dmctl/errortext_test.go` asserts that no error returned by the CLI
  names the binary or repeats a label, and pins the rendered text of the failures an
  operator meets most often.
- `statusFor` classifying a condition from any package was confirmed by a not-found raised
  outside declarative management answering 404 where it previously answered 500.
- `make test` across both modules; `make test-quickstart`, which parses CLI output.

## References

- [RFC 9457, Problem Details for HTTP APIs](https://www.rfc-editor.org/rfc/rfc9457.html)
  (obsoletes [RFC 7807](https://www.rfc-editor.org/rfc/rfc7807.html))
- [RFC 8555 section 6.7](https://www.rfc-editor.org/rfc/rfc8555.html#section-6.7), the
  ACME problem documents this repository already implements in
  `devicemanagement/pki/acme/problem.go`
- [Go 1.13 error values](https://go.dev/blog/go1.13-errors) and
  [Go 1.20 `errors.Join`](https://go.dev/doc/go1.20#errors)
- Decision record 0035 for `dmctl` output and exit codes, 0037 for event redaction
