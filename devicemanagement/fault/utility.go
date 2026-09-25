package fault

// Application artifacts and identities an administrator submits for inspection, and
// public App Store listings they look up.
var (
	AppArtifactInvalid = NewClient(
		"DM-APPARTIFACT-INVALID", InvalidArgument,
		"the application artifact is not valid",
	)
	AppArtifactUnsupported = NewClient(
		"DM-APPARTIFACT-UNSUPPORTED", InvalidArgument,
		"the application artifact type is not supported",
	)
	AppArtifactTooLarge = NewClient(
		"DM-APPARTIFACT-TOO-LARGE", PayloadTooLarge,
		"the application artifact exceeds the inspection limit",
	)
	AppIdentityInvalid = NewClient(
		"DM-APPIDENTITY-INVALID", InvalidArgument,
		"the bundle or Mach-O executable is not valid",
	)
	AppIdentityTooLarge = NewClient(
		"DM-APPIDENTITY-TOO-LARGE", PayloadTooLarge,
		"the application metadata exceeds the size limit",
	)
	AppStoreQueryInvalid = NewClient(
		"DM-APPSTORE-QUERY-INVALID", InvalidArgument,
		"the App Store query is not valid",
	)
	AppStoreListingNotFound = NewClient(
		"DM-APPSTORE-LISTING-NOT-FOUND", NotFound,
		"the App Store listing does not exist",
	)
)
