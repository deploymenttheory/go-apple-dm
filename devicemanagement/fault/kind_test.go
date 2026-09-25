package fault_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// TestKindsAlignWithHTTP pins each kind's status, name and server-ness: a transport
// reads them instead of keeping its own table, so a change here is a wire change.
func TestKindsAlignWithHTTP(t *testing.T) {
	want := map[*fault.Kind]struct {
		name   string
		status int
	}{
		fault.NotFound:             {"not_found", http.StatusNotFound},
		fault.InvalidArgument:      {"invalid_argument", http.StatusBadRequest},
		fault.Conflict:             {"conflict", http.StatusConflict},
		fault.Unauthenticated:      {"unauthenticated", http.StatusUnauthorized},
		fault.PermissionDenied:     {"permission_denied", http.StatusForbidden},
		fault.ResourceExhausted:    {"resource_exhausted", http.StatusTooManyRequests},
		fault.Unimplemented:        {"unimplemented", http.StatusNotImplemented},
		fault.Unavailable:          {"unavailable", http.StatusServiceUnavailable},
		fault.DeadlineExceeded:     {"deadline_exceeded", http.StatusGatewayTimeout},
		fault.Upstream:             {"upstream", http.StatusBadGateway},
		fault.Gone:                 {"gone", http.StatusGone},
		fault.PayloadTooLarge:      {"payload_too_large", http.StatusRequestEntityTooLarge},
		fault.UnsupportedMediaType: {"unsupported_media_type", http.StatusUnsupportedMediaType},
		fault.Internal:             {"internal", http.StatusInternalServerError},
		fault.Cancelled:            {"cancelled", 499},
	}
	kinds := fault.Kinds()
	if len(kinds) != len(want) {
		t.Fatalf("Kinds() has %d entries, want %d", len(kinds), len(want))
	}
	for _, k := range kinds {
		w, ok := want[k]
		if !ok {
			t.Fatalf("unexpected kind %v", k)
		}
		if k.String() != w.name || k.HTTPStatus() != w.status {
			t.Fatalf("%v: name %q status %d, want %q %d", k, k.String(), k.HTTPStatus(), w.name, w.status)
		}
		if k.Server() != (w.status >= 500) {
			t.Fatalf("%v: Server() = %v", k, k.Server())
		}
		if k.Error() == "" || k.Kind() != k {
			t.Fatalf("%v: text %q or Kind() changed", k, k.Error())
		}
	}
	// Kinds are errors in their own right, so a bare kind classifies itself.
	if !errors.Is(fault.Gone, fault.Gone) || fault.KindOf(fault.Gone) != fault.Gone {
		t.Fatal("a bare kind did not classify itself")
	}
	// The slice is a copy.
	kinds[0] = nil
	if fault.Kinds()[0] == nil {
		t.Fatal("Kinds() exposed its backing array")
	}
}
