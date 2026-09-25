package fault

// Conditions a device meets on a protocol route. A device never receives this server's
// prose: it acts on the status and, for the five conditions Apple documents, on an
// error document whose code is Apple's identifier, carried here verbatim and checked
// against the generated schema by the transport that sends it. Apple's
// com.apple.watch.pairing.token.missing document is not catalogued until a watch
// enrollment flow sends it.
//
// The status a device receives is decided by the transport, not by the kind alone. A
// device answered 401 outside account-driven re-authentication may unenroll, so a
// transport renders Unauthenticated as 400 unless it is issuing a WWW-Authenticate
// challenge; a device answered 403 with an unrecognized-device document unenrolls on
// purpose; a device answered 410 to UserAuthenticate stops managing that user channel.
var (
	// DeviceUnrecognized is an enrollment this server does not know. With Apple's
	// error document the device unenrolls; without it the device receives a bare
	// status.
	DeviceUnrecognized = NewDevice(
		"com.apple.unrecognized.device", PermissionDenied,
		"the enrollment is not recognized by this server",
	)
	// DeviceSoftwareUpdateRequired is a device below the OS version enrollment
	// requires. The transport attaches the required version in the document's details.
	DeviceSoftwareUpdateRequired = NewDevice(
		"com.apple.softwareupdate.required", PermissionDenied,
		"the device must update its operating system before enrolling",
	)
	// DevicePlatformSSORequired is a device that must complete Platform SSO first.
	DevicePlatformSSORequired = NewDevice(
		"com.apple.psso.required", PermissionDenied,
		"the device must complete Platform SSO before enrolling",
	)
	// DeviceWellKnownFailed is a service discovery request this server refused.
	DeviceWellKnownFailed = NewDevice(
		"com.apple.well-known.failed", PermissionDenied,
		"service discovery failed for this device",
	)

	// DeviceIdentityRequired is a request without the device identity certificate the
	// route requires.
	DeviceIdentityRequired = NewDevice("", PermissionDenied, "the device identity certificate is missing")
	// DeviceIdentityMismatch is a request whose identity certificate does not belong to
	// the enrollment it names.
	DeviceIdentityMismatch = NewDevice("", PermissionDenied, "the device identity does not match the enrollment")
	// DeviceReenrollDenied is an Authenticate for an enrollment that already exists
	// where policy forbids re-enrollment.
	DeviceReenrollDenied = NewDevice("", PermissionDenied, "re-enrollment is not permitted for this enrollment")
	// DeviceEnrollmentDisabled is a request from an enrollment this server has
	// disabled.
	DeviceEnrollmentDisabled = NewDevice("", PermissionDenied, "the enrollment is disabled")
	// DeviceMessageInvalid is a check-in or command response this server could not
	// accept.
	DeviceMessageInvalid = NewDevice("", InvalidArgument, "the message is not acceptable")
	// DeviceUserNotManaged is a UserAuthenticate for a user this server does not manage;
	// Apple's protocol expects 410 and the device stops managing the user channel.
	DeviceUserNotManaged = NewDevice("", Gone, "the user channel is not managed")
	// DeviceUserAuthRequired is a user-channel request before UserAuthenticate.
	DeviceUserAuthRequired = NewDevice("", PermissionDenied, "the user channel is not authenticated")
	// DeviceHandlerUnavailable is a message type this server does not serve.
	DeviceHandlerUnavailable = NewDevice("", Unimplemented, "the message type is not served")
	// DeviceReauthenticationRequired is an account-driven enrollment whose access
	// token must be renewed; the transport answers with a WWW-Authenticate challenge.
	DeviceReauthenticationRequired = NewDevice("", Unauthenticated, "the enrollment must re-authenticate")

	// DeviceDDMEndpointMalformed is a DeclarativeManagement check-in naming an
	// endpoint the protocol does not define.
	DeviceDDMEndpointMalformed = NewDevice("", InvalidArgument, "the declarative management endpoint is malformed")
	// DeviceDDMStatusTooLarge is a status report over the size this server accepts.
	DeviceDDMStatusTooLarge = NewDevice("", PayloadTooLarge, "the status report exceeds the accepted size")
	// DeviceDDMStatusMalformed is a status report this server could not parse.
	DeviceDDMStatusMalformed = NewDevice("", InvalidArgument, "the status report is malformed")
)
