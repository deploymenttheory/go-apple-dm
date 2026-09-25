package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// RecordError marks a span with a failure the way the semantic conventions ask: the
// bounded error.type attribute always, and Error status only when the failure is the
// server's — a 5xx kind. A request the server rejected, or one its caller abandoned, is
// a successful span from the server's point of view, and marking it as an error would
// make every malformed request from a device look like an outage. The message is not
// recorded on the span, following the rule the rest of this package keeps; it belongs
// in the log record that carries the same trace identifiers.
//
// The value is fault.ErrorType, so a span, a log record and a metric that describe the
// same failure agree on it.
func RecordError(span trace.Span, err error) {
	if err == nil || span == nil {
		return
	}
	t := fault.ErrorType(err)
	span.SetAttributes(attribute.String(AttrErrorType, t))
	if fault.KindOf(err).Server() {
		span.SetStatus(codes.Error, t)
	}
}
