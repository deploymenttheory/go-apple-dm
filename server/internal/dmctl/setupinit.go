package dmctl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// Custody outcomes for generated key material. A secret the deployment can
// replace by itself is regenerable; one that cannot be replaced without losing
// data must be preserved outside the machine that holds it.
const (
	custodyPreserve    = "preserve"
	custodyRegenerable = "regenerable"
)

// generatedSecret describes one piece of key material that setup init created.
//
// The file names are short because they are also identifiers, so on their own they
// say nothing about what they protect. Reporting the purpose and the custody
// requirement alongside each one is what stops an operator having to read the
// server source to find out whether a file matters.
type generatedSecret struct {
	// Name is the file name inside the secrets directory.
	Name string `json:"name"`
	// Path is the absolute file, as the server will read it.
	Path string `json:"path"`
	// Variable is the DM_* setting that references the file, where one does. The
	// storage keys are resolved by name from the secrets directory instead.
	Variable string `json:"variable,omitempty"`
	// Purpose is what the secret is, in one phrase.
	Purpose string `json:"purpose"`
	// Custody is custodyPreserve or custodyRegenerable.
	Custody string `json:"custody"`
	// Consequence states what losing it costs.
	Consequence string `json:"consequence"`
}

// setupInitResult is the outcome of setup init.
type setupInitResult struct {
	SetupFile  string            `json:"setupFile"`
	SecretsDir string            `json:"secretsDir,omitempty"`
	Secrets    []generatedSecret `json:"secrets,omitempty"`
	NextAction string            `json:"nextAction"`
}

// secretPurposes describes the referenced secrets by the setting that names them.
var secretPurposes = map[string]struct{ purpose, consequence string }{
	app.EnvBootstrapToken: {
		"bootstrap administrator credential",
		"a replacement can be generated and the setup file updated",
	},
	app.EnvSCEPHMACKey: {
		"SCEP enrollment challenge key",
		"rotating it invalidates enrollment profiles that carry the old challenge",
	},
	app.EnvACMEHMACKey: {
		"ACME external account binding key",
		"rotating it invalidates enrollment profiles that carry the old binding",
	},
}

// describeGeneratedSecrets reads the generated document and reports what its key
// material is for. Deriving this from the document rather than from a fixed list
// keeps it correct for a custom storage key name, extra key aliases and an
// imported ACME key.
func describeGeneratedSecrets(path string) (setupInitResult, error) {
	result := setupInitResult{
		SetupFile:  path,
		NextAction: "create or import HTTPS and enrollment identities; request the Apple certificates",
	}
	// #nosec G304 -- the document this command just wrote, at an operator-selected path.
	b, err := os.ReadFile(path)
	if err != nil {
		return result, wrapError(err)
	}
	var f app.SetupFile
	if err := json.Unmarshal(b, &f); err != nil {
		return result, wrapError(err)
	}
	result.SecretsDir = f.Environment[app.EnvSecretsDir]
	// Storage keys are resolved by name from the secrets directory, so the file name
	// is the key name and cannot be changed independently of it.
	for i, name := range strings.Split(f.Environment[app.EnvStorageKeys], ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		purpose := "database encryption key, active"
		if i > 0 {
			purpose = "database encryption key, retained for values sealed under it"
		}
		result.Secrets = append(result.Secrets, generatedSecret{
			Name:        name,
			Path:        filepath.Join(result.SecretsDir, name),
			Purpose:     purpose,
			Custody:     custodyPreserve,
			Consequence: "the sealed columns cannot be read and every certificate in the database is lost",
		})
	}
	referenced := make([]string, 0, len(f.SecretFiles))
	for variable := range f.SecretFiles {
		referenced = append(referenced, variable)
	}
	sort.Strings(referenced)
	for _, variable := range referenced {
		file := f.SecretFiles[variable]
		described, ok := secretPurposes[variable]
		if !ok {
			described.purpose, described.consequence = "referenced secret", "unknown to this version"
		}
		result.Secrets = append(result.Secrets, generatedSecret{
			Name:        filepath.Base(file),
			Path:        file,
			Variable:    variable,
			Purpose:     described.purpose,
			Custody:     custodyRegenerable,
			Consequence: described.consequence,
		})
	}
	return result, nil
}

// renderSetupInit writes the outcome of setup init for a person.
//
// It names every generated secret, what it protects and whether losing it is
// recoverable, because the command has just written key material whose file names
// are identifiers rather than descriptions.
func renderSetupInit(w *tabwriter.Writer, result setupInitResult) {
	_, _ = fmt.Fprintf(w, "Wrote %s\n", result.SetupFile)
	if len(result.Secrets) == 0 {
		_, _ = fmt.Fprintf(w, "\nNext: %s.\n", result.NextAction)
		return
	}
	_, _ = fmt.Fprintf(w, "\nGenerated key material in %s:\n\n", result.SecretsDir)
	_, _ = fmt.Fprintln(w, "  FILE\tSETTING\tWHAT IT IS\tIF LOST")
	preserve := make([]string, 0, len(result.Secrets))
	for _, s := range result.Secrets {
		variable := s.Variable
		if variable == "" {
			variable = "(by name)"
		}
		outcome := "recoverable"
		if s.Custody == custodyPreserve {
			outcome = "UNRECOVERABLE"
			preserve = append(preserve, s.Name)
		}
		_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", s.Name, variable, s.Purpose, outcome)
	}
	for _, s := range result.Secrets {
		if s.Custody == custodyPreserve {
			_, _ = fmt.Fprintf(w, "\n  %s: %s.\n", s.Name, s.Consequence)
		}
	}
	if len(preserve) > 0 {
		_, _ = fmt.Fprintf(w,
			"\nCopy %s into a secrets manager now, before this deployment holds anything\n"+
				"that cannot be recreated. Keep the file name as well as the bytes: the name is\n"+
				"recorded inside every value sealed with it and selects the key that opens it.\n",
			strings.Join(preserve, " and "),
		)
	}
	_, _ = fmt.Fprintf(w, "\nNext: %s.\n", result.NextAction)
}

// emitSetupInit reports the outcome in the selected output mode.
func (e *env) emitSetupInit(path string) error {
	result, err := describeGeneratedSecrets(path)
	if err != nil {
		return err
	}
	if e.opts.output != outputHuman {
		return wrapError(json.NewEncoder(e.stdout).Encode(result))
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 8, 2, ' ', 0)
	renderSetupInit(tw, result)
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("dmctl: write: %w", err)
	}
	return nil
}
