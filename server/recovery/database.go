package recovery

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// SQL copies rows without decrypting secret columns. Schema comes from the
// caller's compiled migrations, never from executable SQL in an archive.
// Snapshot requires a drained maintenance fence. Restore requires an empty,
// isolated database and keeps inserted rows in one transaction.
type SQL struct {
	DB      *sql.DB
	Dialect sqlcommon.Dialect
	Schema  []sqlcommon.MigrationSet
}

type tableSnapshot struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    int64    `json:"rows"`
}

type sequenceSnapshot struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Value  int64  `json:"value"`
}

type databaseSnapshot struct {
	Version   int                 `json:"version"`
	Backend   string              `json:"backend"`
	Schemas   []schemaDescription `json:"schemas"`
	Tables    []tableSnapshot     `json:"tables"`
	Sequences []sequenceSnapshot  `json:"sequences"`
}

type cell struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

func (s SQL) validate() error {
	if s.DB == nil || len(s.Schema) == 0 {
		return ErrInvalid
	}
	if s.Dialect.Name != "sqlite" && s.Dialect.Name != "postgres" && s.Dialect.Name != "mysql" {
		return ErrInvalid
	}
	return nil
}

func tableNames(ctx context.Context, q sqlcommon.Queryer, backend string) ([]string, error) {
	query := "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
	switch backend {
	case "postgres":
		query = "SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY tablename"
	case "mysql":
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name"
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, wrap(err)
		}
		if !identifier.MatchString(name) {
			return nil, ErrInvalid
		}
		names = append(names, name)
	}
	return names, wrap(rows.Err())
}

// Snapshot streams tables into a new private directory. It rejects unknown
// tables and partial migration sets, instead of producing an incomplete backup.
func (s SQL) Snapshot(ctx context.Context, destination string) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return wrap(err)
	}
	opts := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	if s.Dialect.Name == "sqlite" {
		opts = nil
	}
	tx, err := s.DB.BeginTx(ctx, opts)
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	names, err := tableNames(ctx, tx, s.Dialect.Name)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, name := range names {
		present[name] = true
	}
	info := databaseSnapshot{
		Version: 1,
		Backend: s.Dialect.Name,
		Schemas: []schemaDescription{},
		Tables:  []tableSnapshot{},
	}
	for _, set := range s.Schema {
		desc, err := describe(set)
		if err != nil {
			return err
		}
		if !present[set.Table] {
			continue
		}
		if err := s.checkVersions(ctx, tx, desc); err != nil {
			return err
		}
		info.Schemas = append(info.Schemas, desc)
		for _, table := range desc.Tables {
			if !present[table] {
				return fmt.Errorf("%w: missing table %s", ErrInvalid, table)
			}
			delete(present, table)
			row, err := s.snapshotTable(ctx, tx, destination, table)
			if err != nil {
				return err
			}
			info.Tables = append(info.Tables, row)
		}
	}
	if len(present) != 0 || len(info.Schemas) == 0 {
		return fmt.Errorf("%w: database has unregistered or missing schemas", ErrInvalid)
	}
	info.Sequences, err = s.snapshotSequences(ctx, tx, info.Tables)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return wrap(err)
	}
	return writeJSONFile(filepath.Join(destination, "database.json"), info)
}

func (s SQL) checkVersions(ctx context.Context, q sqlcommon.Queryer, desc schemaDescription) error {
	table, err := quoted(s.Dialect, desc.Name)
	if err != nil {
		return err
	}
	rows, err := q.QueryContext(
		ctx,
		"SELECT version FROM "+table+" ORDER BY version",
	) // #nosec G202 -- identifier is validated against compiled schema.
	if err != nil {
		return wrap(err)
	}
	defer rows.Close()
	versions := []int{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return wrap(err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return wrap(err)
	}
	if !slices.Equal(versions, desc.Versions) {
		return fmt.Errorf("%w: schema version mismatch for %s", ErrInvalid, desc.Name)
	}
	return nil
}

func (s SQL) snapshotTable(
	ctx context.Context,
	q sqlcommon.Queryer,
	directory, table string,
) (tableSnapshot, error) {
	result := tableSnapshot{Name: table}
	name, err := quoted(s.Dialect, table)
	if err != nil {
		return result, err
	}
	rows, err := q.QueryContext(
		ctx,
		"SELECT * FROM "+name,
	) // #nosec G202 -- table belongs to the compiled migration set.
	if err != nil {
		return result, wrap(err)
	}
	defer rows.Close()
	result.Columns, err = rows.Columns()
	if err != nil {
		return result, wrap(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return result, wrap(err)
	}
	f, err := openLocal(
		filepath.Join(directory, table+".jsonl"),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return result, wrap(err)
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	for rows.Next() {
		values := make([]any, len(types))
		pointers := make([]any, len(types))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return result, wrap(err)
		}
		cells := make([]cell, len(values))
		for i, value := range values {
			cells[i], err = encodeCell(value, types[i].DatabaseTypeName())
			if err != nil {
				return result, err
			}
		}
		if err := encoder.Encode(cells); err != nil {
			return result, wrap(err)
		}
		result.Rows++
	}
	if err := rows.Err(); err != nil {
		return result, wrap(err)
	}
	return result, wrap(f.Sync())
}

func encodeCell(value any, databaseType string) (cell, error) {
	switch v := value.(type) {
	case nil:
		return cell{Kind: "null"}, nil
	case bool:
		return cell{Kind: "bool", Value: strconv.FormatBool(v)}, nil
	case int64:
		return cell{Kind: "int", Value: strconv.FormatInt(v, 10)}, nil
	case float64:
		return cell{Kind: "float", Value: strconv.FormatFloat(v, 'g', -1, 64)}, nil
	case time.Time:
		return cell{Kind: "time", Value: v.UTC().Format(time.RFC3339Nano)}, nil
	case string:
		return cell{Kind: "text", Value: v}, nil
	case []byte:
		databaseType = strings.ToUpper(databaseType)
		if strings.Contains(databaseType, "BLOB") || databaseType == "BYTEA" ||
			strings.Contains(databaseType, "BINARY") {
			return cell{Kind: "bytes", Value: base64.StdEncoding.EncodeToString(v)}, nil
		}
		return cell{Kind: "text", Value: string(v)}, nil
	default:
		return cell{}, fmt.Errorf("%w: unsupported SQL value %T", ErrInvalid, value)
	}
}

func decodeCell(c cell) (any, error) {
	switch c.Kind {
	case "null":
		if c.Value != "" {
			return nil, ErrInvalid
		}
		return nil, nil
	case "text":
		return c.Value, nil
	case "bytes":
		value, err := base64.StdEncoding.DecodeString(c.Value)
		return value, wrap(err)
	case "bool":
		value, err := strconv.ParseBool(c.Value)
		return value, wrap(err)
	case "int":
		value, err := strconv.ParseInt(c.Value, 10, 64)
		return value, wrap(err)
	case "float":
		value, err := strconv.ParseFloat(c.Value, 64)
		return value, wrap(err)
	case "time":
		value, err := time.Parse(time.RFC3339Nano, c.Value)
		return value, wrap(err)
	default:
		return nil, ErrInvalid
	}
}

// Restore initializes only the checkpoint's compiled schema sets in an empty
// database, restores every row, and retains sequence high-water marks. A failed
// operation leaves the target isolated; it must never be activated automatically.
func (s SQL) Restore(ctx context.Context, directory string) error {
	if err := s.validate(); err != nil {
		return err
	}
	var info databaseSnapshot
	if err := readJSONFile(filepath.Join(directory, "database.json"), &info); err != nil {
		return err
	}
	sets, err := s.validateSnapshot(info)
	if err != nil {
		return err
	}
	names, err := tableNames(ctx, s.DB, s.Dialect.Name)
	if err != nil {
		return err
	}
	if len(names) != 0 {
		return ErrOccupied
	}
	for _, set := range sets {
		if _, err := sqlcommon.MigrateSet(ctx, s.DB, s.Dialect, set); err != nil {
			return wrap(err)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	// Only rows produced by the just-completed initialization exist here.
	// Delete seeds and migration timestamps before restoring their exact values.
	for i := len(info.Tables) - 1; i >= 0; i-- {
		name, err := quoted(s.Dialect, info.Tables[i].Name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+name); err != nil {
			return wrap(err)
		} // #nosec G202 -- compiled schema allowlist.
	}
	for _, table := range info.Tables {
		if err := s.restoreTable(ctx, tx, directory, table); err != nil {
			return err
		}
	}
	if err := s.checkSequenceHighWater(ctx, tx, info.Sequences); err != nil {
		return err
	}
	if s.Dialect.Name != "mysql" {
		if err := s.restoreSequences(ctx, tx, info.Sequences); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return wrap(err)
	}
	if s.Dialect.Name == "mysql" {
		return s.restoreSequences(ctx, s.DB, info.Sequences)
	}
	return nil
}

func (s SQL) validateSnapshot(info databaseSnapshot) ([]sqlcommon.MigrationSet, error) {
	if info.Version != 1 || info.Backend != s.Dialect.Name || len(info.Schemas) == 0 {
		return nil, ErrInvalid
	}
	known := map[string]sqlcommon.MigrationSet{}
	for _, set := range s.Schema {
		known[set.Table] = set
	}
	sets := []sqlcommon.MigrationSet{}
	expectedTables := []string{}
	for _, schema := range info.Schemas {
		set, ok := known[schema.Name]
		if !ok {
			return nil, ErrInvalid
		}
		delete(known, schema.Name)
		desc, err := describe(set)
		if err != nil {
			return nil, err
		}
		if desc.Signature != schema.Signature || !slices.Equal(desc.Tables, schema.Tables) ||
			!slices.Equal(desc.Versions, schema.Versions) {
			return nil, fmt.Errorf("%w: checkpoint schema does not match this binary", ErrInvalid)
		}
		sets = append(sets, set)
		expectedTables = append(expectedTables, desc.Tables...)
	}
	if len(expectedTables) != len(info.Tables) {
		return nil, ErrInvalid
	}
	for i, table := range info.Tables {
		if table.Name != expectedTables[i] || len(table.Columns) == 0 || table.Rows < 0 {
			return nil, ErrInvalid
		}
		seen := map[string]bool{}
		for _, column := range table.Columns {
			if !identifier.MatchString(column) || seen[column] {
				return nil, ErrInvalid
			}
			seen[column] = true
		}
	}
	if err := validateSequences(info); err != nil {
		return nil, err
	}
	return sets, nil
}

func (s SQL) restoreTable(
	ctx context.Context,
	tx *sql.Tx,
	directory string,
	table tableSnapshot,
) error {
	name, err := quoted(s.Dialect, table.Name)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(
		ctx,
		"SELECT * FROM "+name+" LIMIT 0",
	) // #nosec G202 -- compiled schema allowlist.
	if err != nil {
		return wrap(err)
	}
	columns, err := rows.Columns()
	closeErr := rows.Close()
	if err != nil || closeErr != nil {
		return wrap(errors.Join(err, closeErr))
	}
	if !slices.Equal(columns, table.Columns) {
		return fmt.Errorf("%w: columns differ for %s", ErrInvalid, table.Name)
	}
	quotedColumns := make([]string, len(columns))
	for i, column := range columns {
		quotedColumns[i], err = quoted(s.Dialect, column)
		if err != nil {
			return err
		}
	}
	query := "INSERT INTO " + name + " (" + strings.Join(quotedColumns, ",") + ") "
	if s.Dialect.Name == "postgres" {
		query += "OVERRIDING SYSTEM VALUE "
	}
	query += "VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + ")"
	statement, err := tx.PrepareContext(ctx, s.Dialect.Rebind(query))
	if err != nil {
		return wrap(err)
	}
	defer statement.Close()
	f, err := openLocal(filepath.Join(directory, table.Name+".jsonl"), os.O_RDONLY, 0)
	if err != nil {
		return wrap(err)
	}
	defer f.Close()
	decoder := json.NewDecoder(contextReader{ctx, f})
	decoder.DisallowUnknownFields()
	for i := int64(0); i < table.Rows; i++ {
		var cells []cell
		if err := decoder.Decode(&cells); err != nil {
			return wrap(err)
		}
		if len(cells) != len(columns) {
			return ErrInvalid
		}
		values := make([]any, len(cells))
		for i, c := range cells {
			values[i], err = decodeCell(c)
			if err != nil {
				return err
			}
		}
		if _, err := statement.ExecContext(ctx, values...); err != nil {
			return wrap(err)
		}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func writeJSONFile(name string, value any) error {
	f, err := openLocal(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return wrap(err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(value); err != nil {
		return wrap(err)
	}
	return wrap(f.Sync())
}

func readJSONFile(name string, value any) error {
	f, err := openLocal(name, os.O_RDONLY, 0)
	if err != nil {
		return wrap(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return wrap(err)
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return ErrLimit
	}
	decoder := json.NewDecoder(io.LimitReader(f, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return wrap(err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}
