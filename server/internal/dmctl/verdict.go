package dmctl

import (
	json "encoding/json/v2"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/adminclient"
)

// keyAvailability is one key's availability issue in an admin API response.
type keyAvailability struct {
	Path, OS, Rule, Severity        string
	Introduced, Deprecated, Removed string
}

// scopeResponse is an assignment or enqueue response that may carry an
// availability verdict.
type scopeResponse struct {
	Changed      *bool
	CommandUUID  string
	Queued       *int
	Skipped      string
	Tier         string
	Availability []keyAvailability
	Verdict      *struct {
		Tier     string
		Eligible []struct{ Identifier string }
		Issues   []struct {
			Identifier, Reason string
			Withheld           bool
			Availability       []keyAvailability
		}
	}
}

// emitVerdict writes an assignment or enqueue response. Human output lists
// the target's support tier, eligible declarations, each declaration issue and
// each affected key with its rule and version window; json modes print the
// server's bytes unchanged.
func (e *env) emitVerdict(resp *adminclient.Response) error {
	var body scopeResponse
	if json.Unmarshal(resp.Body, &body) != nil {
		return e.emit(resp, nil)
	}
	return e.emit(resp, func(w *tabwriter.Writer) {
		if body.Changed != nil {
			_, _ = fmt.Fprintf(w, "changed\t%t\n", *body.Changed)
		}
		if body.CommandUUID != "" {
			_, _ = fmt.Fprintf(w, "command\t%s\n", body.CommandUUID)
		}
		if body.Queued != nil {
			_, _ = fmt.Fprintf(w, "queued\t%d\n", *body.Queued)
		}
		if body.Skipped != "" {
			_, _ = fmt.Fprintf(w, "skipped\t%s\n", body.Skipped)
		}
		writeTier(w, body.Tier)
		writeKeys(w, body.Availability)
		if body.Verdict == nil {
			return
		}
		writeTier(w, body.Verdict.Tier)
		for _, ref := range body.Verdict.Eligible {
			_, _ = fmt.Fprintf(w, "eligible\t%s\n", ref.Identifier)
		}
		for _, issue := range body.Verdict.Issues {
			outcome := "delivered"
			if issue.Withheld {
				outcome = "withheld"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", outcome, issue.Identifier, issue.Reason)
			writeKeys(w, issue.Availability)
		}
	})
}

// writeTier writes the target's support tier when the server reports one.
func writeTier(w *tabwriter.Writer, tier string) {
	if tier != "" {
		_, _ = fmt.Fprintf(w, "tier\t%s\n", tier)
	}
}

// writeKeys writes one line for each key's availability issue, indented under
// the declaration identifiers.
func writeKeys(w *tabwriter.Writer, keys []keyAvailability) {
	for _, k := range keys {
		window := []string{}
		for _, bound := range []struct{ name, version string }{
			{"introduced", k.Introduced}, {"deprecated", k.Deprecated}, {"removed", k.Removed},
		} {
			if bound.version != "" {
				window = append(window, bound.name+" "+bound.version)
			}
		}
		_, _ = fmt.Fprintf(w, "\t%s\t%s\t%s %s\t%s\n", k.Path, k.OS, k.Severity, k.Rule, strings.Join(window, ", "))
	}
}
