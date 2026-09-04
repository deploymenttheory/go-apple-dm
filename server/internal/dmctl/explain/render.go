package explain

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/deploymenttheory/go-apple-dm/v3/schema/support"
)

// Verdict is how one key answers for a target.
type Verdict string

// Verdicts. Unknown is deliberately distinct from OK: Check reports
// Supported for an entry it has no data for and for a query with no target
// OS, and rendering either as OK would assert a fact Apple never stated.
const (
	VerdictOK         Verdict = "OK"
	VerdictNo         Verdict = "NO"
	VerdictDeprecated Verdict = "DEPRECATED"
	VerdictUnknown    Verdict = "unknown"
)

// verdictFor grades one support entry against a target.
//
// The two "we do not know" cases are decided from the inputs rather than by
// matching Check's reason text, so a reworded reason cannot silently turn an
// unknown into an OK. Everything else prints Check's reason verbatim, which
// is what makes this agree word for word with the rejection an enqueue
// returns for the same key.
func verdictFor(e *support.Entry, t support.Target) (Verdict, string) {
	if e == nil {
		return VerdictUnknown, "no support data"
	}
	if t.OS == "" {
		return VerdictUnknown, "no target OS"
	}
	res := e.Check(t)
	switch {
	case !res.Supported:
		return VerdictNo, res.Reason
	case res.Deprecated:
		return VerdictDeprecated, res.Reason
	default:
		return VerdictOK, res.Reason
	}
}

// Render writes the answer for one match. With a zero target it prints the
// per-OS support table; with a target it grades the type and every key under
// it, printing Result.Reason verbatim so the wording matches the rejection an
// enqueue would return.
func Render(w io.Writer, m Match, t support.Target) error {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	header(tw, m)
	if t.OS == "" {
		renderTable(tw, m)
	} else {
		renderTarget(tw, m, t)
	}
	return tw.Flush() //nolint:wrapcheck // the caller's writer error, unchanged
}

func header(w io.Writer, m Match) {
	name := m.TypeName
	if m.Key {
		name = m.Path
	}
	fmt.Fprintf(w, "%s\t(%s)\n", name, m.Family)
	if m.ID != "" && m.ID != m.TypeName {
		fmt.Fprintf(w, "Id:\t%s\n", m.ID)
	}
	if m.Kind != "" {
		fmt.Fprintf(w, "Kind:\t%s\n", m.Kind)
	}
	if m.Title != "" {
		fmt.Fprintf(w, "Title:\t%s\n", m.Title)
	}
	// The schema path is a citation, not a paraphrase: the generated packages
	// carry no per-key prose, so nothing here is invented.
	if m.Schema != "" {
		fmt.Fprintf(w, "Schema:\tthird_party/device-management/%s\n", m.Schema)
	}
}

// renderTable prints what Apple states per OS.
func renderTable(w io.Writer, m Match) {
	e := support.Lookup(m.Family, m.Path)
	if e == nil {
		fmt.Fprintf(w, "\nNo support data for %s.\n", m.Path)
		return
	}
	fmt.Fprint(w, "\nSupport\n")
	fmt.Fprint(w, "  OS\tintro\tdeprec\tremoved\tdevice\tuser\tsuperv\tDEP\tUAMDM\tsharediPad\tuserEnrol\n")
	for _, os := range support.AllOS {
		s := e.OS[os]
		if s == nil {
			fmt.Fprintf(w, "  %s\t-\t\t\t\t\t\t\t\t\t\n", os)
			continue
		}
		if s.NotAvailable {
			fmt.Fprintf(w, "  %s\tn/a\t\t\t\t\t\t\t\t\t\n", os)
			continue
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			os, version(s.Introduced), version(s.Deprecated), version(s.Removed),
			tri(s.DeviceChannel), tri(s.UserChannel), tri(s.Supervised),
			tri(s.RequiresDEP), tri(s.UserApprovedMDM),
			mode(s.SharedIPadMode), mode(s.UserEnrollmentMode))
	}
	keys := Keys(m)
	if len(keys) == 0 {
		return
	}
	fmt.Fprintf(w, "\nKeys (%d)\n", len(keys))
	for _, k := range keys {
		fmt.Fprintf(w, "  %s\t%s\n", k, availability(m.Family, k))
	}
}

// renderTarget grades the type and its keys for one target.
func renderTarget(w io.Writer, m Match, t support.Target) {
	fmt.Fprintf(w, "\nTarget\t%s\n\n", describe(t))
	row := func(path string) {
		e := support.Lookup(m.Family, path)
		v, reason := verdictFor(e, t)
		fmt.Fprintf(w, "  %s\t%s\t%s\n", v, path, reason)
	}
	row(m.Path)
	for _, k := range Keys(m) {
		row(k)
	}
}

// availability summarises which OS versions introduce a key, for the untargeted
// listing.
func availability(family, path string) string {
	e := support.Lookup(family, path)
	if e == nil {
		return "no support data"
	}
	var parts []string
	for _, os := range support.AllOS {
		s := e.OS[os]
		if s == nil || s.NotAvailable {
			continue
		}
		switch {
		case !s.Removed.IsZero():
			parts = append(parts, fmt.Sprintf("%s %s to %s", os, s.Introduced, s.Removed))
		case s.Introduced.IsZero():
			parts = append(parts, string(os))
		default:
			parts = append(parts, fmt.Sprintf("%s %s+", os, s.Introduced))
		}
	}
	if len(parts) == 0 {
		return "not available"
	}
	return strings.Join(parts, ", ")
}

// tri renders a tri-state. A nil pointer means Apple did not say, which is
// not the same as "no" and must never be printed as one.
func tri(b *bool) string {
	switch {
	case b == nil:
		return "-"
	case *b:
		return "yes"
	default:
		return "no"
	}
}

func mode(m support.Mode) string {
	if m == "" {
		return "-"
	}
	return string(m)
}

func version(v support.Version) string {
	if v.IsZero() {
		return "-"
	}
	return v.String()
}

func describe(t support.Target) string {
	parts := []string{fmt.Sprintf("%s %s", t.OS, t.Version)}
	if t.Channel != "" {
		parts = append(parts, string(t.Channel)+" channel")
	}
	for _, f := range []struct {
		on   bool
		name string
	}{
		{t.Supervised, "supervised"},
		{t.SharedIPad, "Shared iPad"},
		{t.UserEnrollment, "user enrollment"},
		{t.DEP, "DEP"},
		{t.UserApproved, "user-approved"},
	} {
		if f.on {
			parts = append(parts, f.name)
		}
	}
	return strings.Join(parts, ", ")
}
