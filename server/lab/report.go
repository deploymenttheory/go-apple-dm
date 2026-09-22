package lab

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Report file names written into a run directory.
const (
	ResultsFile = "results.json"
	JUnitFile   = "junit.xml"
	HTMLFile    = "report.html"
)

// WriteReports writes private JSON evidence, a JUnit projection and a self-contained HTML
// report. Non-passes never become successes merely because the selected execution mode
// lacks a device.
func WriteReports(dir string, results []Result) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return wrapError(err)
	}
	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return wrapError(err)
	}
	if err = os.WriteFile(filepath.Join(dir, ResultsFile), append(b, '\n'), 0o600); err != nil {
		return wrapError(err)
	}
	if err = writeJUnit(dir, results); err != nil {
		return err
	}
	return WriteHTML(dir, results)
}

// ReadResults loads the results.json written by WriteReports.
func ReadResults(dir string) ([]Result, error) {
	b, err := os.ReadFile(filepath.Join(dir, ResultsFile)) // #nosec G304 -- operator-selected run directory
	if err != nil {
		return nil, wrapError(err)
	}
	var results []Result
	if err = json.Unmarshal(b, &results); err != nil {
		return nil, wrapError(err)
	}
	return results, nil
}

// writeJUnit writes the JUnit projection: blocked and unsupported results are skipped.
func writeJUnit(dir string, results []Result) error {
	type issue struct {
		Message string `xml:"message,attr"`
	}
	type testcase struct {
		Name    string `xml:"name,attr"`
		Class   string `xml:"classname,attr"`
		Time    string `xml:"time,attr"`
		Failure *issue `xml:"failure,omitempty"`
		Skipped *issue `xml:"skipped,omitempty"`
	}
	suite := struct {
		XMLName  xml.Name   `xml:"testsuite"`
		Tests    int        `xml:"tests,attr"`
		Failures int        `xml:"failures,attr"`
		Skipped  int        `xml:"skipped,attr"`
		Cases    []testcase `xml:"testcase"`
	}{Tests: len(results)}
	for _, r := range results {
		c := testcase{
			Name:  r.ID + " " + r.Name,
			Class: r.Adapter + "." + r.Mode,
			Time:  fmt.Sprintf("%.3f", r.Duration.Seconds()),
		}
		switch r.Status {
		case StatusFailed:
			c.Failure = &issue{r.Detail}
			suite.Failures++
		case StatusBlocked, StatusUnsupported:
			c.Skipped = &issue{r.Detail}
			suite.Skipped++
		}
		suite.Cases = append(suite.Cases, c)
	}
	b, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return wrapError(err)
	}
	return wrapError(os.WriteFile(filepath.Join(dir, JUnitFile), b, 0o600))
}

//go:embed report.html.tmpl
var reportSource string

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"duration":  func(d time.Duration) string { return d.Round(time.Millisecond).String() },
	"stageName": stageName,
}).Parse(reportSource))

// stageName names a lifecycle stage for the report.
func stageName(stage int) string {
	names := map[int]string{
		StagePreflight: "Preflight", StageProvision: "Provision", StageEnroll: "Enrollment",
		StageReadiness: "Readiness", StageInventory: "Inventory", StageConfiguration: "Configuration",
		StageApps: "Apps", StageSecurity: "Security and update", StageEvents: "Events and audit",
		StageReplacement: "Replacement", StageDestructive: "Destructive", StageUnenroll: "Unenrollment",
	}
	if name, ok := names[stage]; ok {
		return fmt.Sprintf("%02d %s", stage, name)
	}
	return fmt.Sprintf("%02d", stage)
}

type reportView struct {
	Generated string
	Revisions []string
	Adapters  []string
	Modes     []string
	Counts    map[string]int
	Total     int
	Duration  time.Duration
	Targets   []targetView
}

type targetView struct {
	Result Result // first result, for the target description
	Stages []stageView
	Counts map[string]int
	stages map[int]int // stage to index in Stages
}

type stageView struct {
	Stage   int
	Results []Result
}

// WriteHTML renders the results as a self-contained report.html in dir.
func WriteHTML(dir string, results []Result) error {
	view := reportView{
		Generated: time.Now().UTC().Format(time.RFC3339),
		Counts:    map[string]int{},
		Total:     len(results),
	}
	byTarget := map[string]*targetView{}
	var order []string
	for _, r := range results {
		view.Counts[r.Status]++
		view.Duration += r.Duration
		view.Revisions = appendUnique(view.Revisions, r.Revision)
		view.Adapters = appendUnique(view.Adapters, r.Adapter)
		view.Modes = appendUnique(view.Modes, r.Mode)
		key := r.Target.ID
		tv, ok := byTarget[key]
		if !ok {
			tv = &targetView{Result: r, Counts: map[string]int{}, stages: map[int]int{}}
			byTarget[key] = tv
			order = append(order, key)
		}
		tv.Counts[r.Status]++
		// Group by stage whatever order results arrive in, so each stage appears once.
		i, seen := tv.stages[r.Stage]
		if !seen {
			i = len(tv.Stages)
			tv.stages[r.Stage] = i
			tv.Stages = append(tv.Stages, stageView{Stage: r.Stage})
		}
		tv.Stages[i].Results = append(tv.Stages[i].Results, r)
	}
	for _, key := range order {
		tv := byTarget[key]
		slices.SortStableFunc(tv.Stages, func(a, b stageView) int { return a.Stage - b.Stage })
		view.Targets = append(view.Targets, *tv)
	}
	var buf bytes.Buffer
	if err := reportTemplate.Execute(&buf, view); err != nil {
		return wrapError(err)
	}
	return wrapError(os.WriteFile(filepath.Join(dir, HTMLFile), buf.Bytes(), 0o600))
}

// appendUnique appends s when it is non-empty and absent.
func appendUnique(list []string, s string) []string {
	if s == "" || slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// Markdown is generated directly from executable metadata.
func Markdown() string {
	var b strings.Builder
	b.WriteString(
		"# Lab module catalogue\n\nGenerated by `dmctl lab list -format markdown`. Reserved IDs E2E-015 and E2E-022 are not implemented coverage. Names describe the shared workflow assertions; linked regressions retain their detailed component checks. Modes name the server environment a module needs: `simulated` local fixtures or `live` Apple services with a real device.\n\n| ID | Theme | Stage | Modes | Module | Retained regression |\n|---|---|---|---|---|---|\n",
	)
	for _, m := range Catalogue() {
		fmt.Fprintf(
			&b,
			"| %s | %s | %s | %s | %s | %s |\n",
			m.ID,
			m.Theme,
			stageName(m.Stage),
			strings.Join(m.Modes, ", "),
			m.Name,
			m.Regression,
		)
	}
	return b.String()
}
