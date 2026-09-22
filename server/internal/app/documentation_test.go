package app_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

var documentedCode = regexp.MustCompile("`([^`]+)`")

// documentedPermissions reads the deliberately marked operation tables. Each
// data row names exact HTTP patterns in its first cell and actions in its last.
func documentedPermissions(text string, actions map[string]bool, routes map[string]string) (int, []string) {
	const start = "<!-- docs-check: permissions -->"
	const end = "<!-- /docs-check: permissions -->"
	var problems []string
	count := 0
	for {
		_, rest, ok := strings.Cut(text, start)
		if !ok {
			break
		}
		section, after, closed := strings.Cut(rest, end)
		if !closed {
			return count, append(problems, "unclosed permission table marker")
		}
		text = after
		for _, line := range strings.Split(section, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			if len(cells) < 2 || !strings.HasPrefix(line, "|") {
				problems = append(problems, "malformed permission table row: "+line)
				continue
			}
			first := strings.TrimSpace(cells[0])
			if first == "Method and route" || first == "Method and path" || strings.Trim(first, "-: ") == "" {
				continue
			}
			count++
			permissions := documentedCode.FindAllStringSubmatch(cells[len(cells)-1], -1)
			if len(permissions) == 0 {
				problems = append(problems, "permission row has no documented action: "+line)
				continue
			}
			for _, permission := range permissions {
				if !actions[permission[1]] {
					problems = append(problems, "unknown documented permission "+permission[1])
				}
			}
			patterns := documentedCode.FindAllStringSubmatch(cells[0], -1)
			if len(patterns) == 0 {
				problems = append(problems, "permission row has no documented HTTP pattern")
			}
			var sharedPath string
			for _, pattern := range patterns {
				if _, path, ok := strings.Cut(pattern[1], " "); ok && strings.HasPrefix(path, "/") {
					sharedPath = path
				}
			}
			for _, pattern := range patterns {
				value := pattern[1]
				if !strings.Contains(value, " ") && sharedPath != "" {
					value += " " + sharedPath
				}
				actual, ok := routes[value]
				if !ok {
					problems = append(problems, "documented route is not mounted: "+value)
					continue
				}
				if len(permissions) != 1 || actual != permissions[0][1] {
					problems = append(problems, fmt.Sprintf("%s requires %s; documented action differs", value, actual))
				}
			}
		}
	}
	return count, problems
}

// TestDocumentationPermissions compares the maintained operation tables with
// the catalogue and actual mounted routes, catching valid but incorrect actions.
func TestDocumentationPermissions(t *testing.T) {
	a := newEnrollFixture(t, "", nil).app
	actions := make(map[string]bool)
	for _, action := range app.AdminActions() {
		actions[action.ID] = true
	}
	routes := make(map[string]string)
	for _, route := range a.AdminRoutes() {
		routes[route.RoutePattern()] = route.RouteAction()
	}
	for _, name := range []string{"blueprints.md", "reference-lab.md"} {
		t.Run(name, func(t *testing.T) {
			// #nosec G304 -- The names above are fixed repository documentation paths.
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "operations", name))
			if err != nil {
				t.Fatal(err)
			}
			count, problems := documentedPermissions(string(data), actions, routes)
			if count == 0 {
				t.Fatal("no marked permission rows checked")
			}
			for _, problem := range problems {
				t.Error(problem)
			}
		})
	}
}

// TestDocumentationPermissionFailures proves unknown actions, wrong mappings,
// removed routes and truncated markers cannot silently pass the documentation gate.
func TestDocumentationPermissionFailures(t *testing.T) {
	actions := map[string]bool{"read": true, "write": true}
	routes := map[string]string{"GET /items": "read"}
	for _, tt := range []struct {
		name, row string
		fail      bool
	}{
		{"correct", "| `GET /items` | `read` |", false},
		{"unknown", "| `GET /items` | `retired` |", true},
		{"wrong-existing-action", "| `GET /items` | `write` |", true},
		{"removed-route", "| `GET /retired` | `read` |", true},
		{"missing-route", "| GET /items | `read` |", true},
		{"missing-action", "| `GET /retired` | read |", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text := "<!-- docs-check: permissions -->\n" + tt.row + "\n<!-- /docs-check: permissions -->"
			count, problems := documentedPermissions(text, actions, routes)
			if count != 1 || (len(problems) > 0) != tt.fail {
				t.Fatalf("count = %d, problems = %v", count, problems)
			}
		})
	}
	if _, problems := documentedPermissions("<!-- docs-check: permissions -->", actions, routes); len(problems) == 0 {
		t.Fatal("unclosed marker accepted")
	}
	text := "<!-- docs-check: permissions -->\n| `GET /items` | `read` |\n| `GET /retired` | read |\n<!-- /docs-check: permissions -->"
	if count, problems := documentedPermissions(text, actions, routes); count != 2 || len(problems) == 0 {
		t.Fatal("a valid row concealed a malformed permission row")
	}
}
