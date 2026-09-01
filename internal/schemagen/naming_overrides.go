package schemagen

// typeNameOverrides pins top-level type names for schema files whose derived
// name would collide or read badly. Keys are schema paths relative to the
// Apple repository root. Adding an entry here is the only way to change an
// existing generated name; see schema/RENAMES.md.
var typeNameOverrides = map[string]string{}

// fieldNameOverrides pins field names by "SchemaPath#KeyPath" where KeyPath is
// the dotted wire key path within the schema (response keys are prefixed
// with "response:").
var fieldNameOverrides = map[string]string{}
