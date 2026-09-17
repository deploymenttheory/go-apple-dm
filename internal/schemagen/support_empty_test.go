package schemagen

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func TestExplicitEmptySupportLists(t *testing.T) {
	t.Parallel()
	parent := supportOS{
		AllowedScopes:      []string{"system"},
		AllowedEnrollments: []string{"supervised"},
		SharedIPadScopes:   []string{"user"},
	}
	if err := overlay(&parent, &OSSupport{
		AllowedScopes:      []string{},
		AllowedEnrollments: []string{},
		SharedIPad:         &SharedIPad{AllowedScopes: []string{}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, list := range [][]string{parent.AllowedScopes, parent.AllowedEnrollments, parent.SharedIPadScopes} {
		if list == nil || len(list) != 0 {
			t.Fatalf("explicit empty list lost: %#v", list)
		}
	}
	// Omitted child metadata must inherit the explicit prohibition.
	if err := overlay(&parent, &OSSupport{}); err != nil {
		t.Fatal(err)
	}
	got := osLiteral(&parent, support.MacOS)
	for _, name := range []string{"AllowedScopes", "AllowedEnrollments", "SharedIPadScopes"} {
		if !strings.Contains(got, name+": []string{}") {
			t.Errorf("generated support loses %s: %s", name, got)
		}
	}
}
