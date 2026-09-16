package ade_test

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
)

func ExamplePasswordHash() {
	// Supply the password from your secret input and calibrate iterations for
	// your deployment. This iteration count is illustrative, not a recommendation.
	data, err := ade.PasswordHash([]byte("example password"), 40000)
	if err != nil {
		panic(err)
	}
	create := &commands.AccountConfiguration{AutoSetupAdminAccounts: []commands.AccountConfigurationAutoSetupAdminAccounts{{ShortName: "managedadmin", PasswordHash: data}}}
	// GUID must come from the actual administrator created during ADE.
	change := &commands.SetAutoAdminPassword{GUID: "GUID-recorded-from-device", PasswordHash: data}
	fmt.Println(create.AutoSetupAdminAccounts[0].ShortName, change.GUID)
	// Output: managedadmin GUID-recorded-from-device
}
