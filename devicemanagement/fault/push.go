package fault

// Push notifications an API caller asks this server to send.
var APNSRequestInvalid = NewClient(
	"DM-APNS-REQUEST-INVALID", InvalidArgument,
	"the app notification request is not acceptable",
)
