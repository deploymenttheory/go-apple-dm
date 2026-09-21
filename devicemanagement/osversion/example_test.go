package osversion_test

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
)

// ExampleVersion_Compare demonstrates comparing a device OS version with a minimum release and the
// next major release.
func ExampleVersion_Compare() {
	device := osversion.MustParse("26.4.1")
	floor := osversion.New(osversion.MacOS26, 4, 0)
	fmt.Println(device.Compare(floor) >= 0)
	fmt.Println(device.Compare(osversion.New(osversion.MacOS27, 0, 0)) >= 0)
	// Output:
	// true
	// false
}
