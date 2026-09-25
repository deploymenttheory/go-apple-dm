package fault

// RecordNotFound is the fallback condition for an addressed record that does not
// exist in a store without a domain of its own. The state store raises it for every
// namespace, so it is catalogued once; a domain with its own not-found condition
// (declarations, enrollments, certificates) raises that instead.
var RecordNotFound = NewClient(
	"DM-RECORD-NOT-FOUND", NotFound,
	"the record does not exist",
)
