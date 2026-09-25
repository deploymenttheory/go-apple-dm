package fault

// Declarative management predicates, authored by an administrator in an activation.
var (
	PredicateMalformed = NewClient(
		"DM-PREDICATE-MALFORMED", InvalidArgument,
		"the predicate could not be parsed",
	)
	PredicateUnsupported = NewClient(
		"DM-PREDICATE-UNSUPPORTED", InvalidArgument,
		"the predicate uses a construct this server does not support",
	)
	PredicateTypeMismatch = NewClient(
		"DM-PREDICATE-TYPE-MISMATCH", InvalidArgument,
		"the predicate compares values of different types",
	)
)
