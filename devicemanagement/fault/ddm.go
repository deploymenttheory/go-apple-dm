package fault

// Declarative device management, as driven by an API caller: declarations, activations
// and the sets that group them. The conditions a device meets on its own
// DeclarativeManagement channel are in device.go.
var (
	DDMNotFound = NewClient(
		"DM-DDM-NOT-FOUND", NotFound,
		"the declaration, declaration set or enrollment does not exist",
	)
	DDMConflict = NewClient(
		"DM-DDM-CONFLICT", Conflict,
		"the request conflicts with the stored declaration state",
	)
	DDMInvalid = NewClient(
		"DM-DDM-INVALID", InvalidArgument,
		"the declarative management request is not acceptable",
	)
	DDMUnknownType = NewClient(
		"DM-DDM-UNKNOWN-TYPE", InvalidArgument,
		"the declaration type is not one this server serves",
	)
	DDMDeclarationInvalid = NewClient(
		"DM-DDM-DECLARATION-INVALID", InvalidArgument,
		"the declaration failed validation",
	)
)
