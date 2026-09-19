package appleappidentity_test

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appleappidentity"
)

func ExampleSearch() {
	for _, app := range appleappidentity.Search("Safari") {
		fmt.Println(app.Name, app.BundleID)
	}
	// Output: Safari com.apple.mobilesafari
}

func ExampleLookup() {
	app, found := appleappidentity.Lookup("com.apple.MobileSMS")
	fmt.Println(app.Name, found)
	// Output: Messages true
}
