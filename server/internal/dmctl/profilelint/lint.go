// Package profilelint preserves the CLI's internal aliases to the shared inspector.
package profilelint

import "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile/inspect"

type (
	Options = inspect.Options
	Issue   = inspect.Issue
	Report  = inspect.Report
)

var Inspect = inspect.Inspect
