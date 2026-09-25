package telemetry_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/codes"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/telemetry"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/telemetry/telemetrytest"
)

var catalogued = fault.NewClient("DM-TEST-TELEMETRY", fault.NotFound, "the thing is missing")

// TestErrorTypeIsTheCatalogueOne checks that a span's error.type is the same value a
// log record and a metric carry for the failure: the code, else the kind, with a
// cancelled or timed-out request classified by its shape rather than as internal.
func TestErrorTypeIsTheCatalogueOne(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"CataloguedCode":   {fmt.Errorf("x: %w", catalogued), "DM-TEST-TELEMETRY"},
		"KindOnly":         {fault.Wrap(errors.New("x"), fault.WithKind(fault.Upstream)), "upstream"},
		"Cancelled":        {fmt.Errorf("x: %w", context.Canceled), "cancelled"},
		"Timeout":          {context.DeadlineExceeded, "deadline_exceeded"},
		"Plain":            {errors.New("dial tcp: refused"), "internal"},
		"ClassifiedCancel": {fault.Wrap(context.Canceled, fault.WithKind(fault.Unavailable)), "unavailable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := telemetrytest.NewSpanRecorder()
			_, span := rec.Tracer("test").Start(t.Context(), "op")
			telemetry.RecordError(span, tc.err)
			span.End()
			if v, _ := rec.Spans()[0].Attr(telemetry.AttrErrorType); v != tc.want {
				t.Fatalf("error.type = %q, want %q", v, tc.want)
			}
		})
	}
	if telemetry.AttrErrorType != fault.KeyErrorType {
		t.Fatal("telemetry and fault disagree on the error.type key")
	}
}

// TestRecordErrorMarksOnlyServerFailures checks that a span always gains error.type and
// gains Error status only for a 5xx kind, so a rejected device request is not an outage.
func TestRecordErrorMarksOnlyServerFailures(t *testing.T) {
	cases := map[string]struct {
		err        error
		wantType   string
		wantStatus codes.Code
	}{
		"ClientRejection": {fault.New(catalogued), "DM-TEST-TELEMETRY", codes.Unset},
		"ServerFailure":   {fault.Wrap(errors.New("db down"), fault.WithKind(fault.Unavailable)), "unavailable", codes.Error},
		"Unclassified":    {errors.New("boom"), "internal", codes.Error},
		"CallerWentAway":  {fmt.Errorf("read body: %w", context.Canceled), "cancelled", codes.Unset},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := telemetrytest.NewSpanRecorder()
			_, span := rec.Tracer("test").Start(t.Context(), "op")
			telemetry.RecordError(span, tc.err)
			span.End()
			spans := rec.Spans()
			if len(spans) != 1 {
				t.Fatalf("recorded %d spans", len(spans))
			}
			got := spans[0]
			if v, _ := got.Attr(telemetry.AttrErrorType); v != tc.wantType {
				t.Fatalf("error.type = %q, want %q", v, tc.wantType)
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %v, want %v", got.Status, tc.wantStatus)
			}
			if got.StatusMessage != "" && got.StatusMessage != tc.wantType {
				t.Fatal("status description carried more than the type", got.StatusMessage)
			}
		})
	}
	// Nil is a no-op on both sides.
	telemetry.RecordError(nil, errors.New("x"))
	rec := telemetrytest.NewSpanRecorder()
	_, span := rec.Tracer("test").Start(t.Context(), "op")
	telemetry.RecordError(span, nil)
	span.End()
	if s := rec.Spans()[0]; s.Status != codes.Unset || len(s.Attrs) != 0 {
		t.Fatal("nil error marked the span")
	}
}
