package layout_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	immutableAction = regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`)
	immutableImage  = regexp.MustCompile(`^docker://[^@\s]+@sha256:[0-9a-f]{64}$`)
)

func pinnedUses(value string) bool {
	if strings.HasPrefix(value, "docker://") {
		return immutableImage.MatchString(value)
	}
	return strings.HasPrefix(value, "./") || immutableAction.MatchString(value)
}

func TestWorkflowSecurity(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github/workflows/*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("workflows: %v", err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		var visit func(*yaml.Node)
		visit = func(n *yaml.Node) {
			if n.Kind == yaml.MappingNode {
				for i := 0; i < len(n.Content); i += 2 {
					key, value := n.Content[i], n.Content[i+1]
					if key.Value == "uses" &&
						(value.Kind != yaml.ScalarNode || !pinnedUses(value.Value)) {
						t.Errorf(
							"%s:%d: external actions require full commit SHAs: %q",
							file,
							key.Line,
							value.Value,
						)
					}
				}
			}
			for _, child := range n.Content {
				visit(child)
			}
		}
		visit(&doc)
	}
}

func TestWorkflowSecurityReferences(t *testing.T) {
	for _, value := range []string{"actions/checkout@v7", "org/repo/workflow.yml@main", "${{ inputs.action }}", "docker://image:latest", "actions/checkout@abc", "docker://image@" + strings.Repeat("a", 40)} {
		if pinnedUses(value) {
			t.Errorf("mutable reference accepted: %s", value)
		}
	}
	for _, value := range []string{"./.github/actions/local", "org/repo/workflow.yml@" + strings.Repeat("a", 40), "docker://image@sha256:" + strings.Repeat("a", 64)} {
		if !pinnedUses(value) {
			t.Errorf("immutable/local reference rejected: %s", value)
		}
	}
}
