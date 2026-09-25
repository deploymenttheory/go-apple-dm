package fault

// Property list parsing. A plist arrives from a device as a protocol message and from
// an API caller as a profile or a command, so the conditions are catalogued for the
// caller; a device meets them as a bare status.
var (
	PlistTooLarge = NewClient(
		"DM-PLIST-TOO-LARGE", PayloadTooLarge,
		"the plist exceeds the size limit",
	)
	PlistTooDeep = NewClient(
		"DM-PLIST-TOO-DEEP", InvalidArgument,
		"the plist nesting exceeds the depth limit",
	)
	PlistUnknownFormat = NewClient(
		"DM-PLIST-UNKNOWN-FORMAT", InvalidArgument,
		"the input is neither an XML nor a binary plist",
	)
)
