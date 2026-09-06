package support_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

func TestOSFromProduct(t *testing.T) {
	cases := map[string]support.OS{
		"iPhone17,2": support.IOS, "iPad14,1": support.IOS, "iPod9,1": support.IOS,
		"Mac16,1": support.MacOS, "MacBookPro18,3": support.MacOS, "iMac21,1": support.MacOS, "VirtualMac2,1": support.MacOS,
		"AppleTV14,1": support.TvOS, "RealityDevice14,1": support.VisionOS, "Watch7,1": support.WatchOS,
		"": "", "Toaster1,1": "",
	}
	for product, want := range cases {
		if got := support.OSFromProduct(product); got != want {
			t.Errorf("%q: got %q, want %q", product, got, want)
		}
	}
}
