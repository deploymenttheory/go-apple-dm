package audit_test

import (
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/server/audit"
)

var t0 = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// Every backend defers to Page.Size, so the defaulting and the bound are
// defined once here rather than three times in SQL.
func TestPageSize(t *testing.T) {
	cases := map[string]struct {
		limit int
		want  int
	}{
		"Unset":     {0, audit.DefaultPageSize},
		"Negative":  {-1, audit.DefaultPageSize},
		"Requested": {10, 10},
		"AtMax":     {audit.MaxPageSize, audit.MaxPageSize},
		"AboveMax":  {audit.MaxPageSize + 1, audit.MaxPageSize},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := (audit.Page{Limit: tc.limit}).Size(); got != tc.want {
				t.Fatalf("Size() = %d, want %d", got, tc.want)
			}
		})
	}
}

// Query.Matches is the in-memory filter and the specification the SQL WHERE
// clause has to agree with, so its edges are worth pinning directly.
func TestQueryMatches(t *testing.T) {
	rec := audit.Record{
		At: t0, Type: "admin-action", Actor: "ops",
		Enrollment: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-1"},
	}
	cases := map[string]struct {
		q    audit.Query
		want bool
	}{
		"Zero":            {audit.Query{}, true},
		"Type":            {audit.Query{Type: "admin-action"}, true},
		"WrongType":       {audit.Query{Type: "enrolled"}, false},
		"Actor":           {audit.Query{Actor: "ops"}, true},
		"WrongActor":      {audit.Query{Actor: "break-glass"}, false},
		"Enrollment":      {audit.Query{Enrollment: "UDID-1"}, true},
		"WrongEnrollment": {audit.Query{Enrollment: "UDID-2"}, false},
		"SinceInclusive":  {audit.Query{Since: t0}, true},
		"SinceAfter":      {audit.Query{Since: t0.Add(time.Second)}, false},
		"UntilExclusive":  {audit.Query{Until: t0}, false},
		"UntilAfter":      {audit.Query{Until: t0.Add(time.Second)}, true},
		"Combined":        {audit.Query{Type: "admin-action", Actor: "ops", Since: t0}, true},
		"CombinedMiss":    {audit.Query{Type: "admin-action", Actor: "nobody"}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.q.Matches(rec); got != tc.want {
				t.Fatalf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}
