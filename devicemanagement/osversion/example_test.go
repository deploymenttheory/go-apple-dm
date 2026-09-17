package osversion_test

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
)

func ExampleVersion_Compare() {
	device := osversion.MustParse("26.4.1")
	floor := osversion.New(osversion.MacOS26, 4, 0)
	fmt.Println(device.Compare(floor) >= 0)
	fmt.Println(device.Compare(osversion.New(osversion.MacOS27, 0, 0)) >= 0)
	// Output:
	// true
	// false
}
