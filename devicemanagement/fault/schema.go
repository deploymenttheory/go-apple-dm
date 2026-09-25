package fault

// Schema validation of content an API caller authored: a command, a declaration, a
// profile payload or a blueprint checked against Apple's device management schema.
var SchemaInvalid = NewClient(
	"DM-SCHEMA-INVALID", InvalidArgument,
	"the content failed schema validation",
)
