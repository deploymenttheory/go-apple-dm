package activationlock_test

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/activationlock"
)

// ExampleGenerate demonstrates generating an activation-lock bypass code and its DEP escrow hash.
func ExampleGenerate() {
	code, hash := activationlock.Generate()
	// Persist code securely against the device before sending this request.
	// Unlocking later requires code; it cannot be reconstructed from hash.
	request := dep.ActivationLockRequest{Device: "DEVICE-SERIAL", EscrowKey: hash}
	fmt.Println(len(code), len(request.EscrowKey))
	// Output: 31 64
}
