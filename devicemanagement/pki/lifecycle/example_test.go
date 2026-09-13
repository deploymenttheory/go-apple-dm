package lifecycle_test

import (
	"context"
	"crypto/x509/pkix"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func ExampleManager_Begin() {
	// Memory is appropriate for this example. Deployments supply an encrypted
	// persistent state adapter and retain its external encryption keys.
	manager := &lifecycle.Manager{Store: state.NewMemory()}
	ctx := lifecycle.WithAudit(context.Background(), "setup-operator", "push/request")
	request := lifecycle.Request{ID: "customer", Kind: lifecycle.Push, Subject: pkix.Name{CommonName: "Example customer"}}
	first, err := manager.Begin(ctx, request)
	if err != nil {
		panic(err)
	}
	resumed, err := manager.Begin(ctx, request)
	if err != nil {
		panic(err)
	}
	csr, err := manager.Export(ctx, request.ID, resumed.Pending, "csr")
	if err != nil {
		panic(err)
	}
	// Send the public CSR to a vendor signer, attach the verified response,
	// export the signed request for Apple's portal, then Import and Activate
	// the returned certificate using this same pending revision.
	fmt.Println(first.Pending == resumed.Pending, len(csr) > 0)
	// Output: true true
}
