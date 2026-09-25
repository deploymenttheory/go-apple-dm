package fault

// Operating system versions, as supplied by an API caller in a blueprint target or a
// software update policy.
var OSVersionMalformed = NewClient(
	"DM-OSVERSION-MALFORMED", InvalidArgument,
	"the operating system version is malformed",
)
