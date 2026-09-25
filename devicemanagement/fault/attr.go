package fault

import "log/slog"

// Attribute keys shared by errors, log records and spans. They follow OpenTelemetry's
// dotted form so a record from this library and a span from the consumer's tracer name
// the same thing the same way. Values are Apple's identifiers verbatim: an enrollment
// ID is the UDID or EnrollmentID the device sent, a request type is the RequestType
// string from the command.
const (
	// KeyErrorType is OpenTelemetry's error.type: the catalogued code when there is
	// one, otherwise the kind's name, so a metric can count failures by a bounded set.
	KeyErrorType = "error.type"
	// KeyEnrollmentID is the enrollment the request or command concerns.
	KeyEnrollmentID = "enrollment.id"
	// KeyRequestID is the identifier a transport assigned to one inbound request.
	KeyRequestID = "request.id"
	// KeyCorrelationID groups the records of one exchange across services.
	KeyCorrelationID = "correlation.id"
	// KeyPrincipalID is the authenticated administrative principal.
	KeyPrincipalID = "principal.id"
	// KeyHTTPRoute is the registered route pattern, never the request path.
	KeyHTTPRoute = "http.route"
	// KeyDeclarationIdentifier is a declaration's Identifier.
	KeyDeclarationIdentifier = "declaration.identifier"
	// KeyCommandUUID is a command's CommandUUID.
	KeyCommandUUID = "command.uuid"
	// KeyCommandRequestType is a command's RequestType.
	KeyCommandRequestType = "command.request_type"
	// KeyMessageType is a check-in message's MessageType.
	KeyMessageType = "checkin.message_type"
	// KeyComponent names the library component that wrote a record, such as "dep" or
	// "ddm"; a logger handed to a component carries it on every record.
	KeyComponent = "component"
	// KeyWorker names a background worker.
	KeyWorker = "worker.name"
	// KeyDestination names an event delivery destination.
	KeyDestination = "outbox.destination"
)

// EnrollmentID returns the enrollment attribute.
func EnrollmentID(id string) slog.Attr { return slog.String(KeyEnrollmentID, id) }

// RequestID returns the request attribute.
func RequestID(id string) slog.Attr { return slog.String(KeyRequestID, id) }

// CorrelationID returns the correlation attribute.
func CorrelationID(id string) slog.Attr { return slog.String(KeyCorrelationID, id) }

// PrincipalID returns the principal attribute.
func PrincipalID(id string) slog.Attr { return slog.String(KeyPrincipalID, id) }

// HTTPRoute returns the route attribute.
func HTTPRoute(pattern string) slog.Attr { return slog.String(KeyHTTPRoute, pattern) }

// DeclarationIdentifier returns the declaration attribute.
func DeclarationIdentifier(id string) slog.Attr { return slog.String(KeyDeclarationIdentifier, id) }

// CommandUUID returns the command attribute.
func CommandUUID(id string) slog.Attr { return slog.String(KeyCommandUUID, id) }

// CommandRequestType returns the request type attribute.
func CommandRequestType(t string) slog.Attr { return slog.String(KeyCommandRequestType, t) }

// MessageType returns the check-in message type attribute.
func MessageType(t string) slog.Attr { return slog.String(KeyMessageType, t) }

// Component returns the component attribute.
func Component(name string) slog.Attr { return slog.String(KeyComponent, name) }

// Worker returns the worker attribute.
func Worker(name string) slog.Attr { return slog.String(KeyWorker, name) }

// Destination returns the destination attribute.
func Destination(name string) slog.Attr { return slog.String(KeyDestination, name) }

// ErrorType returns the value for error.type: the catalogued code when there is one,
// otherwise the kind's name. It is empty for nil.
func ErrorType(err error) string {
	if err == nil {
		return ""
	}
	if code := CodeOf(err); code != "" {
		return string(code)
	}
	return KindOf(err).String()
}

// ErrorTypeAttr returns error.type as a flat attribute, for a record or a metric that
// needs the classification beside the error group.
func ErrorTypeAttr(err error) slog.Attr { return slog.String(KeyErrorType, ErrorType(err)) }

// Attr renders any error as the "error" group a boundary logs. It works on a plain
// error, a wrapped occurrence and a bare condition alike, because slog only consults
// LogValue on the outermost value and a boundary usually holds a wrapped one. A nil
// error yields an empty attribute, which handlers ignore.
func Attr(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	return slog.Attr{Key: "error", Value: valueOf(err)}
}

// reservedGroupKeys are the keys valueOf writes itself.
var reservedGroupKeys = map[string]bool{
	"message": true, "kind": true, "code": true, "audience": true,
	"operation": true, "detail": true, "retry_after": true, "stack": true,
}

// valueOf builds the group: message, kind, code, audience, operation, detail, the
// merged attributes, retry_after and stack when present. A merged attribute whose key
// is one of the group's own is dropped rather than allowed to shadow it.
func valueOf(err error) slog.Value {
	attrs := []slog.Attr{slog.String("message", err.Error())}
	if k := KindOf(err); k != nil {
		attrs = append(attrs, slog.String("kind", k.String()))
	}
	if code := CodeOf(err); code != "" {
		attrs = append(attrs, slog.String("code", string(code)))
	}
	attrs = append(attrs, slog.String("audience", AudienceOf(err).String()))
	if occ, ok := err.(*Error); ok { //nolint:errorlint // the operation and detail belong to the outermost occurrence only
		if occ.op != "" {
			attrs = append(attrs, slog.String("operation", occ.op))
		}
		if occ.detail != "" {
			attrs = append(attrs, slog.String("detail", occ.detail))
		}
	}
	for _, a := range AttrsOf(err) {
		if !reservedGroupKeys[a.Key] {
			attrs = append(attrs, a)
		}
	}
	if d := RetryAfterOf(err); d > 0 {
		attrs = append(attrs, slog.Duration("retry_after", d))
	}
	if stack := StackOf(err); stack != nil {
		attrs = append(attrs, slog.Any("stack", stack))
	}
	return slog.GroupValue(attrs...)
}
