package fault_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// render logs one record with the given attribute through the JSON handler and decodes
// it, which is what a log pipeline sees.
func render(t *testing.T, attr slog.Attr) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.LogAttrs(t.Context(), slog.LevelInfo, "request failed", attr)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("decode record: %v: %s", err, buf.String())
	}
	return rec
}

// TestErrorGroupRendersFieldsNotProse checks the shape a boundary logs: a group under
// "error" whose fields a query can match, for an occurrence, a wrapped occurrence and
// a bare condition alike.
func TestErrorGroupRendersFieldsNotProse(t *testing.T) {
	occ := fault.Wrap(errors.New("row missing"),
		fault.WithEntry(entry),
		fault.WithOperation("read declaration"),
		fault.WithAttrs(fault.DeclarationIdentifier("com.acme.x")),
		fault.WithRetryAfter(time.Second),
		fault.WithStack(),
	)
	t.Run("OccurrenceViaLogValue", func(t *testing.T) {
		rec := render(t, slog.Any("error", occ))
		group, _ := rec["error"].(map[string]any)
		if group["message"] != "read declaration: the thing is missing: row missing" {
			t.Fatal("message", group["message"])
		}
		if group["kind"] != "not_found" || group["code"] != "DM-TEST-MISSING" || group["audience"] != "client" {
			t.Fatal("classification fields", group)
		}
		if group["operation"] != "read declaration" || group[fault.KeyDeclarationIdentifier] != "com.acme.x" {
			t.Fatal("context fields", group)
		}
		if group["retry_after"] == nil || group["stack"] == nil {
			t.Fatal("retry_after or stack missing", group)
		}
	})
	t.Run("WrappedViaAttr", func(t *testing.T) {
		// slog consults LogValue only on the outermost value; Attr does the walk.
		wrapped := fmt.Errorf("serve: %w", occ)
		plain := render(t, slog.Any("error", wrapped))
		if _, isGroup := plain["error"].(map[string]any); isGroup {
			t.Fatal("expected slog to render a wrapped error as a string")
		}
		rec := render(t, fault.Attr(wrapped))
		group, _ := rec["error"].(map[string]any)
		if group["code"] != "DM-TEST-MISSING" || group[fault.KeyDeclarationIdentifier] != "com.acme.x" {
			t.Fatal("Attr lost fields through wrapping", group)
		}
		if group["operation"] != nil {
			t.Fatal("operation belongs to the outermost occurrence only", group)
		}
	})
	t.Run("BareEntry", func(t *testing.T) {
		rec := render(t, slog.Any("error", entry))
		group, _ := rec["error"].(map[string]any)
		if group["message"] != "the thing is missing" || group["kind"] != "not_found" {
			t.Fatal("entry group", group)
		}
	})
	t.Run("PlainError", func(t *testing.T) {
		rec := render(t, fault.Attr(errors.New("boom")))
		group, _ := rec["error"].(map[string]any)
		if group["message"] != "boom" || group["kind"] != "internal" || group["audience"] != "operator" || group["code"] != nil {
			t.Fatal("plain error group", group)
		}
	})
	t.Run("Nil", func(t *testing.T) {
		rec := render(t, fault.Attr(nil))
		if _, present := rec["error"]; present {
			t.Fatal("nil rendered a field")
		}
	})
}

// TestErrorTypeIsBounded checks the value a metric counts by: the code when there is
// one, otherwise the kind, never the message.
func TestErrorTypeIsBounded(t *testing.T) {
	if got := fault.ErrorType(fmt.Errorf("x: %w", entry)); got != "DM-TEST-MISSING" {
		t.Fatal(got)
	}
	if got := fault.ErrorType(fault.Wrap(errors.New("x"), fault.WithKind(fault.Gone))); got != "gone" {
		t.Fatal(got)
	}
	if got := fault.ErrorType(errors.New("anything at all")); got != "internal" {
		t.Fatal(got)
	}
	if a := fault.ErrorTypeAttr(entry); a.Key != fault.KeyErrorType || a.Value.String() != "DM-TEST-MISSING" {
		t.Fatal(a)
	}
}

// TestAttributeHelpersUseTheVocabulary pins each helper to its key so a dashboard built
// on one name never drifts from the code.
func TestAttributeHelpersUseTheVocabulary(t *testing.T) {
	for key, attr := range map[string]slog.Attr{
		fault.KeyEnrollmentID:          fault.EnrollmentID("v"),
		fault.KeyRequestID:             fault.RequestID("v"),
		fault.KeyCorrelationID:         fault.CorrelationID("v"),
		fault.KeyPrincipalID:           fault.PrincipalID("v"),
		fault.KeyHTTPRoute:             fault.HTTPRoute("v"),
		fault.KeyDeclarationIdentifier: fault.DeclarationIdentifier("v"),
		fault.KeyCommandUUID:           fault.CommandUUID("v"),
		fault.KeyCommandRequestType:    fault.CommandRequestType("v"),
		fault.KeyMessageType:           fault.MessageType("v"),
		fault.KeyWorker:                fault.Worker("v"),
		fault.KeyDestination:           fault.Destination("v"),
	} {
		if attr.Key != key || attr.Value.String() != "v" {
			t.Fatalf("%s: %v", key, attr)
		}
	}
}
