// Package profilelint preserves the CLI's internal aliases to the shared inspector.
package profilelint

import "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile/inspect"

type (
	// Options is the shared profile-inspection configuration exposed by dmctl.
	Options = inspect.Options
	// Issue is one shared profile-inspection diagnostic.
	Issue = inspect.Issue
	// Report is the shared profile-inspection result returned by dmctl.
	Report = inspect.Report
)

var Inspect = inspect.Inspect
