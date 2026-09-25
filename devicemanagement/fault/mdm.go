package fault

// MDM commands and the content an administrator supplies alongside them.
var (
	MDMCommandInvalid = NewClient(
		"DM-MDM-COMMAND-INVALID", InvalidArgument,
		"the command is not acceptable",
	)
	ActivationLockBypassCodeInvalid = NewClient(
		"DM-ACTIVATIONLOCK-BYPASS-CODE-INVALID", InvalidArgument,
		"the server bypass code is not valid",
	)
	ManifestInvalid = NewClient(
		"DM-MANIFEST-INVALID", InvalidArgument,
		"the manifest input is not acceptable",
	)
	ProfileInvalid = NewClient(
		"DM-PROFILE-INVALID", InvalidArgument,
		"the configuration profile is not acceptable",
	)
	ProfileMalformed = NewClient(
		"DM-PROFILE-MALFORMED", InvalidArgument,
		"the configuration profile could not be parsed",
	)
)
