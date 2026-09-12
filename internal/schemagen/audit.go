package schemagen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Evidence identifies a source location and the observation requiring attention.
type Evidence struct {
	Path    string `json:"path"`
	Detail  string `json:"detail"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	Context string `json:"context,omitempty"`
}

// Finding is a grouped failure or engineering review, independent of run time.
type Finding struct {
	Key         string     `json:"key"`
	Category    string     `json:"category"`
	Kind        string     `json:"kind"`
	Stage       string     `json:"stage"`
	Title       string     `json:"title"`
	Action      string     `json:"action"`
	Evidence    []Evidence `json:"evidence"`
	Fingerprint string     `json:"fingerprint"`
}

// SchemaChange compares one wire object, including moves that preserve its ID.
type SchemaChange struct {
	Path         string     `json:"path"`
	PreviousPath string     `json:"previousPath,omitempty"`
	Identifier   string     `json:"identifier"`
	Kind         string     `json:"kind"`
	Fields       []Evidence `json:"fields"`
}

// AuditReport is the versioned interchange between the scanner and publisher.
// Stage completion, runtime logs and immutable run identities are added by the
// workflow runner. Findings never imply that blocked downstream tests passed.
type AuditReport struct {
	SchemaVersion   int            `json:"schemaVersion"`
	BaselineCommit  string         `json:"baselineCommit"`
	CandidateCommit string         `json:"candidateCommit"`
	Ref             string         `json:"ref"`
	BaselineCount   int            `json:"baselineCount"`
	CandidateCount  int            `json:"candidateCount"`
	ParsePassed     bool           `json:"parsePassed"`
	Changes         []SchemaChange `json:"changes"`
	Findings        []Finding      `json:"findings"`
}

type auditDocument struct {
	path   string
	id     string
	fields map[string]string
	prose  map[string]string
}

type auditTree struct {
	docs        map[string]auditDocument
	findings    map[string]*Finding
	areas       map[string]bool
	inputs      map[string][]string
	parsePassed bool
}

// Audit compares raw schema structure even when the strict generator rejects
// new metadata. It reports every file's parse error without changing Parse.
func Audit(baseline, candidate, ref string) (*AuditReport, error) {
	before, err := readAuditTree(baseline, false)
	if err != nil {
		return nil, err
	}
	after, err := readAuditTree(candidate, true)
	if err != nil {
		return nil, err
	}
	report := &AuditReport{
		SchemaVersion: 1, BaselineCommit: gitHEAD(baseline),
		CandidateCommit: gitHEAD(candidate), Ref: ref, BaselineCount: len(before.docs),
		CandidateCount: len(after.docs), ParsePassed: after.parsePassed,
		Changes: []SchemaChange{}, Findings: []Finding{},
	}
	for area := range after.areas {
		if !before.areas[area] {
			files := after.inputs[area]
			if len(files) == 0 {
				files = []string{area}
			}
			for _, file := range files {
				after.add(
					"upstream-input",
					"review",
					"audit",
					"area:"+area,
					"Assess new Apple input area: "+area,
					"Decide whether this input area belongs in the library or server and record the support decision.",
					file,
					"New upstream input area; no handler or generator support is assumed.",
				)
			}
		}
	}
	for _, pair := range matchAuditDocuments(before.docs, after.docs) {
		old, current, existed, exists := pair.before, pair.after, pair.existed, pair.exists
		id := current.id
		if !exists {
			id = old.id
		}
		change := SchemaChange{Identifier: id, Path: current.path, Fields: []Evidence{}}
		switch {
		case !exists:
			change.Path, change.Kind = old.path, "removed"
		case !existed:
			change.Kind = "added"
		default:
			change.Kind = "changed"
			change.Fields = compareFields(old.fields, current.fields)
			if old.path != current.path {
				change.PreviousPath = old.path
				change.Kind = "renamed"
			}
			if len(change.Fields) == 0 && old.path == current.path {
				if sameFields(old.prose, current.prose) {
					continue
				}
				change.Kind = "documentation"
				change.Fields = compareFields(old.prose, current.prose)
			}
		}
		report.Changes = append(report.Changes, change)
		after.reviewChange(change, old, current)
	}
	for _, f := range after.findings {
		sort.Slice(f.Evidence, func(i, j int) bool {
			if f.Evidence[i].Path == f.Evidence[j].Path {
				return f.Evidence[i].Detail < f.Evidence[j].Detail
			}
			return f.Evidence[i].Path < f.Evidence[j].Path
		})
		// Presentation context is not part of incident identity. Preserve the
		// original path/detail fingerprint contract when adding richer evidence.
		identity := make([]Evidence, len(f.Evidence))
		for i, evidence := range f.Evidence {
			identity[i] = Evidence{Path: evidence.Path, Detail: evidence.Detail}
		}
		data, _ := json.Marshal(identity)
		sum := sha256.Sum256(data)
		f.Fingerprint = hex.EncodeToString(sum[:])
		report.Findings = append(report.Findings, *f)
	}
	sort.Slice(
		report.Findings,
		func(i, j int) bool { return report.Findings[i].Key < report.Findings[j].Key },
	)
	sort.Slice(
		report.Changes,
		func(i, j int) bool { return report.Changes[i].Path < report.Changes[j].Path },
	)
	return report, nil
}

// Apple deliberately defines multiple schemas with the same wire ID. Match
// paths first; recognize a move by wire ID only when the unmatched pair is
// unambiguous. The registry's ByID API has the same one-to-many semantics.
type auditPair struct {
	before, after   auditDocument
	existed, exists bool
}

func matchAuditDocuments(before, after map[string]auditDocument) []auditPair {
	pairs := []auditPair{}
	used := map[string]bool{}
	oldIDs, newIDs := map[string][]string{}, map[string][]string{}
	for file, doc := range before {
		if current, ok := after[file]; ok {
			pairs = append(pairs, auditPair{doc, current, true, true})
			used[file] = true
		} else {
			oldIDs[doc.id] = append(oldIDs[doc.id], file)
		}
	}
	for file, doc := range after {
		if _, ok := before[file]; !ok {
			newIDs[doc.id] = append(newIDs[doc.id], file)
		}
	}
	for id, files := range oldIDs {
		for _, file := range files {
			if len(files) == 1 && len(newIDs[id]) == 1 {
				target := newIDs[id][0]
				used[target] = true
				pairs = append(pairs, auditPair{before[file], after[target], true, true})
			} else {
				pairs = append(pairs, auditPair{before: before[file], existed: true})
			}
		}
	}
	for file, doc := range after {
		if !used[file] {
			pairs = append(pairs, auditPair{after: doc, exists: true})
		}
	}
	return pairs
}

func (t *auditTree) add(category, kind, stage, key, title, action, file, detail string) {
	key = category + ":" + key
	f := t.findings[key]
	if f == nil {
		f = &Finding{
			Key:      key,
			Category: category,
			Kind:     kind,
			Stage:    stage,
			Title:    title,
			Action:   action,
			Evidence: []Evidence{},
		}
		t.findings[key] = f
	}
	evidence := Evidence{Path: file, Detail: detail}
	for _, existing := range f.Evidence {
		if existing == evidence {
			return
		}
	}
	f.Evidence = append(f.Evidence, evidence)
}

var unknownAuditField = regexp.MustCompile(`field ([^ ]+) not found in type ([^\s]+)`)

func (t *auditTree) parseError(file string, err error) {
	t.parsePassed = false
	messages := unknownAuditField.FindAllStringSubmatch(err.Error(), -1)
	if len(messages) == 0 {
		t.add(
			"schema-format",
			"failure",
			"parse",
			"parse:"+file,
			"schemagen cannot parse "+file,
			"Reproduce strict parsing against the recorded commit; correct the generator or investigate the upstream schema.",
			file,
			err.Error(),
		)
		return
	}
	for _, m := range messages {
		t.add(
			"schema-format",
			"failure",
			"parse",
			"field:"+m[2]+":"+m[1],
			"schemagen rejects "+m[1]+" metadata in "+m[2],
			"Compare the field with Apple's schema definition; add deliberate support or document an upstream inconsistency. Keep strict decoding enabled.",
			file,
			m[0],
		)
	}
}

func readAuditTree(directory string, strict bool) (*auditTree, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	defer root.Close()
	tree := &auditTree{
		docs:        map[string]auditDocument{},
		findings:    map[string]*Finding{},
		areas:       map[string]bool{},
		inputs:      map[string][]string{},
		parsePassed: true,
	}
	err = fs.WalkDir(root.FS(), ".", func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if file != "." && strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			if !strings.Contains(file, "/") && file != "." {
				switch file {
				case "docs", "examples", "mdm", "declarative", "other":
				default:
					tree.areas[file] = true
				}
			}
			return nil
		}
		area := strings.Split(file, "/")[0]
		if tree.areas[area] {
			tree.inputs[area] = append(tree.inputs[area], file)
		}
		if strings.HasPrefix(file, "docs/") || strings.HasPrefix(file, "examples/") ||
			!strings.HasSuffix(file, ".yaml") {
			return nil
		}
		data, readErr := root.ReadFile(file)
		if readErr != nil {
			return fmt.Errorf("read audit input: %w", readErr)
		}
		var node yaml.Node
		if parseErr := yaml.Unmarshal(data, &node); parseErr != nil {
			if strict {
				tree.parseError(file, parseErr)
			} else {
				return fmt.Errorf("baseline %s: %w", file, parseErr)
			}
			return nil
		}
		if strict {
			if _, parseErr := Parse(data); parseErr != nil {
				tree.parseError(file, parseErr)
			}
			if _, _, classErr := Classify(file); classErr != nil {
				tree.parseError(file, classErr)
			}
			tree.checkExamples(root, file, &node)
		}
		fields, prose := map[string]string{}, map[string]string{}
		flattenAudit(&node, "", fields, prose, map[*yaml.Node]bool{})
		id := auditID(file, fields)
		tree.docs[file] = auditDocument{path: file, id: id, fields: fields, prose: prose}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	if strict && len(tree.docs) == 0 {
		tree.parseError(".", fmt.Errorf("%w: candidate contains no schema documents", ErrNaming))
	}
	return tree, nil
}

func auditID(file string, fields map[string]string) string {
	for _, key := range []string{"requesttype", "declarationtype", "statusitemtype", "credentialtype", "payloadtype"} {
		if value := fields["payload."+key]; value != "" {
			return strings.Split(file, "/")[0] + ":" + key + ":" + value
		}
	}
	return file
}

func auditScalar(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return strings.Join(strings.Fields(n.Value), " ")
}

func auditChild(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return auditChild(n.Content[0], key)
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func flattenAudit(
	n *yaml.Node,
	prefix string,
	fields, prose map[string]string,
	seen map[*yaml.Node]bool,
) {
	if n == nil {
		return
	}
	if seen[n] {
		fields[prefix] = "<recursive>"
		return
	}
	seen[n] = true
	defer delete(seen, n)
	switch n.Kind {
	case yaml.DocumentNode:
		for _, child := range n.Content {
			flattenAudit(child, prefix, fields, prose, seen)
		}
	case yaml.AliasNode:
		flattenAudit(n.Alias, prefix, fields, prose, seen)
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			key, value := n.Content[i].Value, n.Content[i+1]
			name := key
			if prefix != "" {
				name = prefix + "." + key
			}
			switch key {
			case "examples", "title":
			case "description", "content", "notes":
				if value.Kind == yaml.ScalarNode {
					prose[name] = strings.TrimSpace(value.Value)
				} else {
					flattenAudit(value, name, prose, prose, seen)
				}
			default:
				flattenAudit(value, name, fields, prose, seen)
			}
		}
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			fields[prefix] = "[]"
		}
		for i, child := range n.Content {
			key := auditScalar(auditChild(child, "key"))
			if key == "" {
				key = auditScalar(auditChild(child, "value"))
			}
			if key == "" {
				key = fmt.Sprint(i)
			}
			flattenAudit(child, prefix+"["+key+"]", fields, prose, seen)
		}
	default:
		fields[prefix] = auditScalar(n)
	}
}

func compareFields(before, after map[string]string) []Evidence {
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	changes := []Evidence{}
	for key := range keys {
		old, oldOK := before[key]
		current, currentOK := after[key]
		if oldOK == currentOK && old == current {
			continue
		}
		if !oldOK {
			old = "<absent>"
		}
		if !currentOK {
			current = "<absent>"
		}
		changes = append(
			changes,
			Evidence{Path: key, Detail: old + " → " + current, Before: old, After: current},
		)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func sameFields(
	before, after map[string]string,
) bool {
	return len(compareFields(before, after)) == 0
}

func (t *auditTree) reviewChange(change SchemaChange, old, current auditDocument) {
	file := change.Path
	protocol := strings.HasPrefix(file, "mdm/checkin/") ||
		strings.HasPrefix(file, "declarative/protocol/")
	if protocol {
		for _, field := range substantiveProse(old, current) {
			if change.Kind != "documentation" && old.prose[field.Path] == "" {
				continue // New-field descriptions accompany the structural evidence.
			}
			t.add(
				"behavior-review",
				"review",
				"audit",
				"protocol-wording",
				"Review changed Apple protocol descriptions",
				"Assess whether these wording changes alter server obligations. Record the assessment; wording alone is not a demonstrated incompatibility.",
				file+"#"+field.Path,
				field.Detail,
			)
			t.enrichEvidence("behavior-review:protocol-wording", file+"#"+field.Path, field, "")
		}
		if change.Kind != "documentation" {
			details := change.Fields
			if len(details) == 0 {
				details = []Evidence{{Path: "schema", Detail: change.Kind}}
			}
			for _, field := range details {
				t.add(
					"behavior-review",
					"review",
					"audit",
					"protocol:"+change.Identifier,
					"Review Apple protocol changes in "+path.Base(file),
					"Assess request/response behavior and server responsibilities; link a regression test or record why existing handling is sufficient.",
					file+"#"+field.Path,
					field.Detail,
				)
				t.enrichEvidence(
					"behavior-review:protocol:"+change.Identifier,
					file+"#"+field.Path,
					field,
					fieldContext(current.prose, field.Path),
				)
			}
		}
	}
	if change.Kind == "removed" {
		t.add(
			"behavior-review",
			"review",
			"audit",
			"removed-schemas",
			"Review removed Apple schema objects",
			"Assess migration and support for older devices; do not authorize public API removals automatically.",
			file,
			old.id,
		)
	}
	for _, field := range change.Fields {
		if strings.Contains(field.Path, "supportedOS") {
			t.add(
				"behavior-review",
				"review",
				"audit",
				"availability",
				"Review changed Apple platform and enrollment support",
				"Check supported OS boundaries, channels and enrollment restrictions; confirm older devices retain intended support.",
				file,
				field.Path+": "+field.Detail,
			)
			t.enrichEvidence("behavior-review:availability", file, field, "")
		}
	}
	// New commands can imply work beyond generic plist generation (for example,
	// a server destination for uploaded logs). Keep that assessment explicit.
	if change.Kind == "added" && strings.HasPrefix(file, "mdm/commands/") {
		t.add(
			"behavior-review",
			"review",
			"audit",
			"new-commands",
			"Assess server responsibilities for new Apple commands",
			"Confirm delivery, result handling and any additional service endpoints or storage required by the new commands.",
			file,
			current.id,
		)
	}
}

func (t *auditTree) checkExamples(root *os.Root, file string, node *yaml.Node) {
	examples := auditChild(node, "examples")
	if examples == nil {
		return
	}
	for _, example := range examples.Content {
		files := auditChild(example, "files")
		if files == nil {
			continue
		}
		for _, item := range files.Content {
			for _, key := range []string{"file", "request-file", "response-file"} {
				reference := auditChild(item, key)
				if reference == nil {
					continue
				}
				name := reference.Value
				data, err := root.ReadFile(name)
				if err == nil && strings.HasSuffix(name, ".json") && !json.Valid(data) {
					err = fmt.Errorf("%w: invalid JSON example", ErrNaming)
				}
				if err != nil {
					t.add(
						"upstream-input",
						"failure",
						"audit",
						"examples",
						"Apple schema examples are missing or invalid",
						"Check referenced example paths and data against the pinned Apple commit; record an upstream issue or add a reviewed correction.",
						file,
						name+": "+err.Error(),
					)
				}
			}
		}
	}
}

// Markdown renders an audit without claiming runtime compatibility.
func (r *AuditReport) Markdown() string {
	var out strings.Builder
	fmt.Fprintf(
		&out,
		"# Apple schema assessment: %s\n\nBaseline: `%s`\n\nCandidate: `%s`\n\nSchema objects: %d → %d.\n\n",
		r.Ref,
		r.BaselineCommit,
		r.CandidateCommit,
		r.BaselineCount,
		r.CandidateCount,
	)
	fmt.Fprintf(
		&out,
		"Strict parsing passed: **%t**. Runtime checks are reported separately.\n\n",
		r.ParsePassed,
	)
	for _, finding := range r.Findings {
		fmt.Fprintf(&out, "## %s\n\nKind: %s. %s\n\n", finding.Title, finding.Kind, finding.Action)
		for _, e := range finding.Evidence {
			fmt.Fprintf(&out, "- `%s`: %s\n", e.Path, e.Detail)
		}
		out.WriteString("\n")
	}
	return out.String()
}
