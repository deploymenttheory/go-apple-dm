package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	acmesql "github.com/deploymenttheory/go-apple-dm/server/acmestore/sqlstore"
	adminsql "github.com/deploymenttheory/go-apple-dm/server/adminauth/sqlstore"
	auditsql "github.com/deploymenttheory/go-apple-dm/server/audit/sqlstore"
	ddmsql "github.com/deploymenttheory/go-apple-dm/server/ddmstore/sqlstore"
	depsql "github.com/deploymenttheory/go-apple-dm/server/depstore/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
	"github.com/deploymenttheory/go-apple-dm/server/webhook"
)

// ServerSchema lists every persistent reference-server schema. A database using
// additional application tables must explicitly add their migration sets; an
// unrecognized table fails backup instead of silently losing application state.
func ServerSchema(d sqlcommon.Dialect) ([]sqlcommon.MigrationSet, error) {
	sets := []sqlcommon.MigrationSet{{Table: sqlcommon.DefaultMigrationsTable, FS: d.Migrations}}
	for _, get := range []func(sqlcommon.Dialect) (sqlcommon.MigrationSet, error){
		acmesql.MigrationSet, adminsql.MigrationSet, auditsql.MigrationSet,
		ddmsql.MigrationSet, depsql.MigrationSet, statestore.MigrationSet, eventstore.MigrationSet, webhook.MigrationSet, maintenance.MigrationSet,
	} {
		set, err := get(d)
		if err != nil {
			return nil, wrap(err)
		}
		sets = append(sets, set)
	}
	return sets, nil
}

var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

type schemaDescription struct {
	Name      string   `json:"name"`
	Signature string   `json:"signature"`
	Tables    []string `json:"tables"`
	Versions  []int    `json:"versions"`
}

func describe(set sqlcommon.MigrationSet) (schemaDescription, error) {
	out := schemaDescription{Name: set.Table, Tables: []string{set.Table}}
	if set.FS == nil || !identifier.MatchString(set.Table) {
		return out, ErrInvalid
	}
	migrations, err := sqlcommon.LoadMigrations(set.FS)
	if err != nil {
		return out, wrap(err)
	}
	for _, migration := range migrations {
		out.Versions = append(out.Versions, migration.Version)
		for _, statement := range migration.Up {
			words := strings.Fields(statement)
			if len(words) >= 3 && words[0] == "CREATE" && words[1] == "TABLE" {
				name := words[2]
				if !identifier.MatchString(name) {
					return out, ErrInvalid
				}
				out.Tables = append(out.Tables, name)
			}
		}
	}
	raw, err := json.Marshal(migrations)
	if err != nil {
		return out, fmt.Errorf("recovery: schema signature: %w", err)
	}
	sum := sha256.Sum256(raw)
	out.Signature = hex.EncodeToString(sum[:])
	return out, nil
}

func quoted(d sqlcommon.Dialect, name string) (string, error) {
	if !identifier.MatchString(name) {
		return "", ErrInvalid
	}
	if d.Name == "mysql" {
		return "`" + name + "`", nil
	}
	return `"` + name + `"`, nil
}
