package fault

// Enrollment storage: the records the check-in and command channels read and write.
// These conditions are raised by every storage backend and read by both the
// administration API and the device transport, which projects them onto the device
// conditions in device.go.
var (
	EnrollmentNotFound = NewClient(
		"DM-ENROLLMENT-NOT-FOUND", NotFound,
		"the enrollment does not exist",
	)
	EnrollmentDisabled = NewClient(
		"DM-ENROLLMENT-DISABLED", Conflict,
		"the enrollment is disabled",
	)
	EnrollmentConflict = NewClient(
		"DM-ENROLLMENT-CONFLICT", Conflict,
		"the request conflicts with the stored enrollment",
	)
	EnrollmentInvalid = NewClient(
		"DM-ENROLLMENT-INVALID", InvalidArgument,
		"the enrollment request is not acceptable",
	)
	EnrollmentUserChannelRequired = NewClient(
		"DM-ENROLLMENT-USER-CHANNEL-REQUIRED", InvalidArgument,
		"the operation requires a user channel",
	)
)

// Single-use enrollment tokens that authorize an account-driven enrollment. An
// administrator issues and inspects them; a device presenting one meets the condition
// as a bare status.
var (
	EnrollmentTokenNotFound = NewClient(
		"DM-ENROLLMENT-TOKEN-NOT-FOUND", NotFound,
		"the enrollment token does not exist",
	)
	EnrollmentTokenExpired = NewClient(
		"DM-ENROLLMENT-TOKEN-EXPIRED", Gone,
		"the enrollment token has expired",
	)
	EnrollmentTokenUsed = NewClient(
		"DM-ENROLLMENT-TOKEN-USED", Conflict,
		"the enrollment token has already been used",
	)
)
