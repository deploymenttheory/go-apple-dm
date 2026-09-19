package appidentity_test

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
)

func ExampleInspect() {
	identity, err := appidentity.Inspect(context.Background(), "/Applications/Example.app")
	if err != nil {
		fmt.Println(err)
		return
	}
	// This author chooses exact signed code. Each architecture has its own
	// CDHash; one slice's hash does not identify the complete universal binary.
	payload := &ddm.AppSettings{Allowed: &ddm.AppSettingsAllowed{}}
	for _, arch := range identity.Architectures {
		if arch.Signature.Status != appidentity.Valid || arch.CDHash == "" {
			fmt.Println("review signature before authoring rules:", arch.Name)
			return
		}
		payload.Allowed.AllowedBinaries = append(payload.Allowed.AllowedBinaries,
			ddm.AppSettingsAllowedAllowedBinaries{CDHash: new(arch.CDHash)})
	}
	// Supply the actual target in an application. This example targets the
	// device channel of a supervised Mac running macOS 27.
	target := support.Target{
		OS: support.MacOS, Version: osversion.New(27, 0, 0),
		Channel: support.ChannelDevice, Supervised: true,
	}
	if err := payload.Validate(target); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("binary rules:", len(payload.Allowed.AllowedBinaries))
	// No Output directive: execution requires macOS and the caller's local app.
}
