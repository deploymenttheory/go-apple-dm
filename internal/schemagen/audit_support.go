package schemagen

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// BoundaryCase compares source-derived support with the candidate's compiled
// tables. Existing support package tests independently cover the Check rules.
type BoundaryCase struct {
	Family     string `json:"family"`
	Path       string `json:"path"`
	OS         string `json:"os"`
	Version    string `json:"version"`
	Context    string `json:"context"`
	Supported  bool   `json:"supported"`
	Deprecated bool   `json:"deprecated"`
}

func sourceSupport(
	root string,
	histories ...string,
) (map[string]map[string]*support.Entry, *Tree, error) {
	tree, err := Load(root)
	if err != nil {
		return nil, nil, err
	}
	for _, historyRoot := range histories {
		if historyRoot == "" {
			continue
		}
		history, err := Load(historyRoot)
		if err != nil {
			return nil, nil, err
		}
		tree, err = MergeHistory(tree, history)
		if err != nil {
			return nil, nil, err
		}
	}
	pkgs, err := Build(tree)
	if err != nil {
		return nil, nil, err
	}
	tables := map[string]map[string]*support.Entry{}
	for _, pkg := range pkgs {
		table := map[string]*support.Entry{}
		for _, schema := range pkg.Schemas {
			entries, entryErr := effective(schema)
			if entryErr != nil {
				return nil, nil, entryErr
			}
			for key, entry := range entries {
				table[key] = entry
			}
		}
		tables[pkg.Name] = table
	}
	return tables, tree, nil
}

// BoundaryProbes includes changed source entries on stable and candidate OS
// boundaries, and device/user enrollment contexts. No generated table is used
// to calculate the expected result.
func BoundaryProbes(baseline, candidate string, histories ...string) ([]BoundaryCase, error) {
	before, oldTree, err := sourceSupport(baseline)
	if err != nil {
		return nil, err
	}
	after, _, err := sourceSupport(candidate, histories...)
	if err != nil {
		return nil, err
	}
	stableVersions := NewestIntroduced(oldTree)
	cases := []BoundaryCase{}
	for family, table := range after {
		for name, entry := range table {
			old := before[family][name]
			platforms := map[support.OS]bool{}
			for os := range entry.OS {
				platforms[os] = true
			}
			if old != nil {
				for os := range old.OS {
					platforms[os] = true
				}
			}
			for os := range platforms {
				current := entry.OS[os]
				if old != nil && reflect.DeepEqual(old.OS[os], current) {
					continue
				}
				versions := map[string]bool{}
				if stable := stableVersions[string(os)]; stable != "" {
					versions[stable] = true
				}
				bounds := []*support.OSSupport{current}
				if old != nil {
					bounds = append(bounds, old.OS[os])
				}
				for _, bound := range bounds {
					if bound == nil {
						continue
					}
					for _, v := range []support.Version{bound.Introduced, bound.Removed, bound.Deprecated} {
						if !v.IsZero() {
							versions[v.String()] = true
						}
					}
				}
				if len(versions) == 0 {
					versions["1.0"] = true
				}
				for version := range versions {
					v, versionErr := support.ParseVersion(version)
					if versionErr != nil {
						return nil, fmt.Errorf("boundary: %w", versionErr)
					}
					for _, context := range []string{"device", "user", "unsupervised", "non-dep", "not-user-approved", "shared-device", "shared-user", "user-enrollment"} {
						target := boundaryTarget(os, v, context)
						expected := entry.Check(target)
						cases = append(
							cases,
							BoundaryCase{
								Family:     family,
								Path:       name,
								OS:         string(os),
								Version:    version,
								Context:    context,
								Supported:  expected.Supported,
								Deprecated: expected.Deprecated,
							},
						)
					}
				}
			}
		}
	}
	sort.Slice(cases, func(i, j int) bool {
		a, b := cases[i], cases[j]
		return a.Family+"/"+a.Path+"/"+a.OS+"/"+a.Version+"/"+a.Context < b.Family+"/"+b.Path+"/"+b.OS+"/"+b.Version+"/"+b.Context
	})
	return cases, nil
}

func boundaryTarget(os support.OS, v support.Version, context string) support.Target {
	target := support.Target{
		OS:           os,
		Version:      v,
		Channel:      support.ChannelDevice,
		Supervised:   true,
		DEP:          true,
		UserApproved: true,
	}
	switch context {
	case "user":
		target.Channel = support.ChannelUser
	case "unsupervised":
		target.Supervised = false
	case "non-dep":
		target.DEP = false
	case "not-user-approved":
		target.UserApproved = false
	case "shared-device":
		target.SharedIPad = true
	case "shared-user":
		target.SharedIPad = true
		target.Channel = support.ChannelUser
	case "user-enrollment":
		target.UserEnrollment = true
	}
	return target
}
