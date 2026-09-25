package fault

// Requests an API caller makes through this server to Apple's services: the Apple
// School and Business Manager API (package axm, whose domain AXM abbreviates that name)
// and Apps and Books (package appsbooks). Only the conditions the caller's own request
// creates are catalogued.
//
// A failure of an Apple service is not catalogued, and every client of one classifies
// it the same way through its own error type: a 400, 404 or 409 answer is the caller's
// (InvalidArgument, NotFound, Conflict), because this server relays the caller's
// request and Apple's verdict on it; a 429 is ResourceExhausted with Apple's
// Retry-After; a 408 or 504 is DeadlineExceeded; a 401 or 403 is the deployment's
// credential and is Unavailable, because the caller cannot correct it and a retry will
// not help until an operator acts; anything else is Upstream.
var (
	AXMInvalid = NewClient(
		"DM-AXM-INVALID", InvalidArgument,
		"the Apple School and Business Manager API request is not acceptable",
	)
	AXMLimitInvalid = NewClient(
		"DM-AXM-LIMIT-INVALID", InvalidArgument,
		"the page limit is out of range",
	)
	AXMActivityInvalid = NewClient(
		"DM-AXM-ACTIVITY-INVALID", InvalidArgument,
		"the activity violates the Apple School and Business Manager API rules",
	)
	AppsBooksInvalid = NewClient(
		"DM-APPSBOOKS-INVALID", InvalidArgument,
		"the Apps and Books request is not acceptable",
	)
	AppsBooksLimitExceeded = NewClient(
		"DM-APPSBOOKS-LIMIT-EXCEEDED", ResourceExhausted,
		"the request exceeds the current Apps and Books service limits",
	)
)
