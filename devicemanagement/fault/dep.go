package fault

// Automated Device Enrollment, driven through Apple's device enrollment service. Only
// the conditions an API caller creates are catalogued; an expired token, unsigned terms
// or a missing account are the deployment's to fix and are declared by the dep package
// as operator conditions.
var (
	ADENotFound = NewClient(
		"DM-ADE-NOT-FOUND", NotFound,
		"the Automated Device Enrollment record does not exist",
	)
	ADEInvalid = NewClient(
		"DM-ADE-INVALID", InvalidArgument,
		"the Automated Device Enrollment request is not acceptable",
	)
	ADEConflict = NewClient(
		"DM-ADE-CONFLICT", Conflict,
		"the request conflicts with the stored Automated Device Enrollment state",
	)
	ADEProfileInvalid = NewClient(
		"DM-ADE-PROFILE-INVALID", InvalidArgument,
		"the Automated Device Enrollment profile is not acceptable",
	)
	ADEConsumerKeyMismatch = NewClient(
		"DM-ADE-CONSUMER-KEY-MISMATCH", Conflict,
		"the consumer key differs from the stored token and replacement was not forced",
	)
)
