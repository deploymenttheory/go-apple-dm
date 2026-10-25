package lab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/axmassignment"
)

type faultObservation struct {
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Body   []byte      `json:"body"`
}

type faultGroup struct {
	Signature       string `json:"signature"`
	Observations    []int  `json:"observations"`
	Representatives []int  `json:"representatives"`
}

type faultSelection struct {
	Trace    []faultObservation `json:"trace"`
	Groups   []faultGroup       `json:"groups"`
	Selected []int              `json:"selected"`
	Fallback string             `json:"fallback,omitempty"`
}

// assignmentPoll recognizes only the documented account-scoped workflow read route.
func assignmentPoll(o faultObservation) bool {
	parts := strings.Split(strings.Trim(o.Path, "/"), "/")
	return o.Method == http.MethodGet && len(parts) == 7 && parts[0] == "admin" && parts[1] == "v1" && parts[2] == "axm" && parts[3] == "accounts" && parts[4] != "" && parts[5] == "assignments" && parts[6] != ""
}

// normalizeObservationTime masks only valid, documented observation timestamps while preserving presence and nulls.
func normalizeObservationTime(fields map[string]any, names ...string) error {
	for _, name := range names {
		raw, exists := fields[name]
		if !exists || raw == nil {
			continue
		}
		text, ok := raw.(string)
		if !ok {
			return errors.New("unrecognized observation timestamp type")
		}
		if _, e := time.Parse(time.RFC3339Nano, text); e == nil {
			fields[name] = map[string]any{"observation_time": true}
		}
	}
	return nil
}

// pollSignature preserves every non-time response field, unknown member, number lexeme and response header.
// Invalid, duplicate-key or unrecognized workflow shapes cannot establish equivalent observations.
func pollSignature(o faultObservation) (string, error) {
	if o.Status != http.StatusOK {
		return "", errors.New("unexpected workflow status")
	}
	tokens := jsontext.NewDecoder(bytes.NewReader(o.Body))
	for {
		_, e := tokens.ReadToken()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return "", e
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(o.Body))
	decoder.UseNumber()
	var fields map[string]any
	if e := decoder.Decode(&fields); e != nil {
		return "", e
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", errors.New("multiple workflow values")
	}
	operation, _ := fields["operation"].(string)
	state, _ := fields["state"].(string)
	id, _ := fields["id"].(string)
	targets, ok := fields["targets"].([]any)
	if (operation != "assign" && operation != "unassign") || state == "" || id == "" || !ok || len(targets) == 0 {
		return "", errors.New("unrecognized workflow shape")
	}
	for _, value := range targets {
		target, ok := value.(map[string]any)
		if !ok {
			return "", errors.New("unrecognized target shape")
		}
		serial, _ := target["serial"].(string)
		if _, ok = target["converged"].(bool); !ok || serial == "" {
			return "", errors.New("unrecognized target outcome")
		}
		if e := normalizeObservationTime(target, "observed_at"); e != nil {
			return "", e
		}
	}
	if e := normalizeObservationTime(fields, "created_at", "updated_at", "next_attempt", "submission_started_at", "accepted_at"); e != nil {
		return "", e
	}
	header := map[string]any{}
	for name, values := range o.Header {
		header[name] = append([]string(nil), values...)
	}
	if dates := o.Header["Date"]; len(dates) == 1 {
		if _, e := http.ParseTime(dates[0]); e == nil {
			header["Date"] = map[string]any{"observation_time": true}
		}
	}
	body, e := json.Marshal(struct {
		Fields map[string]any `json:"fields"`
		Header map[string]any `json:"header"`
	}{fields, header})
	if e != nil {
		return "", e
	}
	digest := sha256.Sum256(body)
	return o.Method + " " + o.Path + " " + hex.EncodeToString(digest[:]), nil
}

// selectABMFaults retains every write/non-poll exchange and the first two plus last of each complete response class.
// If any poll cannot be classified, the entire original trace is replayed rather than sampled.
func selectABMFaults(trace []faultObservation) faultSelection {
	out := faultSelection{Trace: trace}
	groups := map[string]int{}
	selected := map[int]bool{}
	for i, o := range trace {
		if !assignmentPoll(o) {
			selected[i] = true
			continue
		}
		signature, e := pollSignature(o)
		if e != nil {
			out.Fallback = fmt.Sprintf("observation %d: %v", i+1, e)
			out.Selected = make([]int, len(trace))
			for j := range trace {
				out.Selected[j] = j
			}
			out.Groups = nil
			return out
		}
		group, ok := groups[signature]
		if !ok {
			group = len(out.Groups)
			groups[signature] = group
			out.Groups = append(out.Groups, faultGroup{Signature: signature})
		}
		out.Groups[group].Observations = append(out.Groups[group].Observations, i)
	}
	for i := range out.Groups {
		group := &out.Groups[i]
		for _, index := range []int{group.Observations[0], group.Observations[min(1, len(group.Observations)-1)], group.Observations[len(group.Observations)-1]} {
			if len(group.Representatives) == 0 || group.Representatives[len(group.Representatives)-1] != index {
				group.Representatives = append(group.Representatives, index)
			}
			selected[index] = true
		}
	}
	for index := range selected {
		out.Selected = append(out.Selected, index)
	}
	sort.Ints(out.Selected)
	return out
}

// TestABMFaultSelectionPreservesResponseBoundaries independently checks complete response equivalence and entry/repeat/exit coverage.
func TestABMFaultSelectionPreservesResponseBoundaries(t *testing.T) {
	base := `{"id":"job","operation":"assign","state":"queued","updated_at":"2000-01-01T00:00:00Z","targets":[{"serial":"SER","converged":false}]}`
	variants := []string{
		base,
		strings.Replace(base, `"queued"`, `"accepted"`, 1),
		strings.Replace(base, `"queued"`, `"terminal"`, 1),
		strings.Replace(base, `"queued"`, `"complete"`, 1),
		strings.Replace(base, `"assign"`, `"unassign"`, 1),
		strings.Replace(base, `"converged":false`, `"converged":true`, 1),
		strings.Replace(base, `"converged":false`, `"converged":false,"outcome":"FAILED"`, 1),
		strings.Replace(base, `"converged":false`, `"converged":false,"outcome":"VENDOR_UNKNOWN"`, 1),
		strings.Replace(base, `"state":"queued"`, `"state":"queued","terminal_status":"FAILED"`, 1),
		strings.Replace(base, `"state":"queued"`, `"state":"queued","log_complete":true,"log_succeeded":false`, 1),
		strings.Replace(base, `"state":"queued"`, `"state":"queued","future":{"enabled":false,"large":9007199254740993,"values":[],"value":null}`, 1),
		strings.Replace(base, `"state":"queued"`, `"state":"queued","future":{"enabled":false,"large":9007199254740992,"values":[],"value":null}`, 1),
		strings.Replace(base, `"state":"queued"`, `"state":"queued","future_at":"2000-01-01T00:00:01Z"`, 1),
		strings.Replace(base, `"state":"queued"`, `"state":"queued","future_at":"2000-01-01T00:00:02Z"`, 1),
	}
	var trace []faultObservation
	// Duplicate non-polls must remain distinct, regardless of identical bodies.
	for range 2 {
		trace = append(trace, faultObservation{Method: "POST", Path: "/admin/v1/axm/accounts/default/assignments", Status: 202, Body: []byte(`{}`)})
	}
	for _, body := range variants {
		for i := range 4 {
			copy := strings.Replace(body, "2000-01-01T00:00:00Z", fmt.Sprintf("2000-01-01T00:00:0%dZ", i), 1)
			trace = append(trace, faultObservation{Method: "GET", Path: "/admin/v1/axm/accounts/default/assignments/job", Status: 200, Header: http.Header{"Date": {time.Unix(int64(i), 0).UTC().Format(http.TimeFormat)}}, Body: []byte(copy)})
		}
	}
	out := selectABMFaults(trace)
	if out.Fallback != "" || len(out.Groups) != len(variants) || len(out.Selected) != 2+3*len(variants) {
		t.Fatalf("coverage classes lost: %+v", out)
	}
	if out.Selected[0] != 0 || out.Selected[1] != 1 {
		t.Fatal("write exchanges were coalesced")
	}
	for i, g := range out.Groups {
		start := 2 + 4*i
		if len(g.Observations) != 4 || !reflect.DeepEqual(g.Representatives, []int{start, start + 1, start + 3}) {
			t.Fatalf("entry/repeat/exit lost: %+v", g)
		}
	}
	// Unknown response headers and invalid timestamps remain exact semantic evidence.
	for _, mutate := range []func(*faultObservation){
		func(o *faultObservation) { o.Header.Set("X-Future", "changed") },
		func(o *faultObservation) {
			o.Body = bytes.Replace(o.Body, []byte("2000-01-01T00:00:00Z"), []byte("future-invalid-time"), 1)
		},
	} {
		a := faultObservation{Method: "GET", Path: "/admin/v1/axm/accounts/default/assignments/job", Status: 200, Header: http.Header{}, Body: []byte(base)}
		b := a
		b.Header = a.Header.Clone()
		mutate(&b)
		if selection := selectABMFaults([]faultObservation{a, b}); selection.Fallback != "" || len(selection.Groups) != 2 {
			t.Fatal("unknown evidence coalesced", selection)
		}
	}
}

// TestABMFaultHeaderTimeMarkerCannotCollide preserves invalid Date literals separately from valid timestamps.
func TestABMFaultHeaderTimeMarkerCannotCollide(t *testing.T) {
	body := []byte(`{"id":"job","operation":"assign","state":"queued","targets":[{"serial":"SER","converged":false}]}`)
	a := faultObservation{Method: "GET", Path: "/admin/v1/axm/accounts/default/assignments/job", Status: 200, Header: http.Header{"Date": {time.Unix(0, 0).UTC().Format(http.TimeFormat)}}, Body: body}
	b := a
	b.Header = http.Header{"Date": {"observation-time"}}
	c := a
	c.Header = a.Header.Clone()
	c.Header.Add("Date", "future-value")
	selected := selectABMFaults([]faultObservation{a, b, c})
	if selected.Fallback != "" || len(selected.Groups) != 3 {
		t.Fatal("invalid header merged with timestamp", selected)
	}
	for range 2 {
		a.Path = "/admin/v1/inventory/diagnostics"
		selected.Trace = append(selected.Trace, a)
	}
	selected = selectABMFaults(selected.Trace)
	if !reflect.DeepEqual(selected.Selected, []int{0, 1, 2, 3, 4}) {
		t.Fatal("non-poll exchanges coalesced", selected)
	}
}

// TestABMFaultSelectionFallsBackToAll refuses to infer equivalence from malformed or unrecognized responses.
func TestABMFaultSelectionFallsBackToAll(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `{"id":"a","id":"b"}`, `{"operation":"assign","state":"queued","id":"job","updated_at":{"observation_time":true},"targets":[{"serial":"S","converged":false}]}`, `{"operation":"assign","state":"queued","id":"job","targets":[]}`, `{"operation":"assign","state":"queued","id":"job","targets":[{"serial":"S","converged":"unknown"}]}`} {
		o := faultObservation{Method: "GET", Path: "/admin/v1/axm/accounts/default/assignments/job", Status: 200, Body: []byte(body)}
		selected := selectABMFaults([]faultObservation{o, o, o, o})
		if selected.Fallback == "" || !reflect.DeepEqual(selected.Selected, []int{0, 1, 2, 3}) {
			t.Fatal("unproven equivalence sampled", selected)
		}
	}
}

// assertABMTraceBoundaries requires actual queued, accepted, terminal-log and independently converged fixture evidence.
func assertABMTraceBoundaries(t *testing.T, trace []faultObservation) {
	t.Helper()
	seen := map[string]map[string]bool{}
	for _, o := range trace {
		if !assignmentPoll(o) {
			continue
		}
		var w axmassignment.Workflow
		if e := json.Unmarshal(o.Body, &w); e != nil {
			t.Fatal(e)
		}
		if seen[w.Operation] == nil {
			seen[w.Operation] = map[string]bool{}
		}
		seen[w.Operation][w.State] = true
		if w.State == "terminal" && w.Operation == "assign" {
			if !w.LogComplete || !w.LogSucceeded || w.TerminalStatus != "COMPLETED" {
				t.Fatal("terminal log evidence missing", w)
			}
			for _, target := range w.Targets {
				if target.Converged || target.Outcome != "SUCCESS" {
					t.Fatal("delayed convergence boundary missing", w)
				}
			}
		}
		if w.State == "complete" {
			for _, target := range w.Targets {
				if !target.Converged {
					t.Fatal("complete evidence lacked convergence", w)
				}
			}
		}
	}
	for operation, states := range map[string][]string{"assign": {"queued", "accepted", "terminal", "complete"}, "unassign": {"queued", "accepted", "complete"}} {
		for _, state := range states {
			if !seen[operation][state] {
				t.Fatalf("actual %s/%s boundary was not observed: %v", operation, state, seen)
			}
		}
	}
}
