package source_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/source"
)

// TestReferencePreservesStorageContract fixes the existing persisted identity encoding.
func TestReferencePreservesStorageContract(t *testing.T) {
	ref := source.Reference{Kind: "axm.device", AccountID: "account", ResourceID: "opaque/id"}
	hash := sha256.Sum256([]byte("axm.device\x00account\x00opaque/id"))
	if ref.Key() != hex.EncodeToString(hash[:]) {
		t.Fatal(ref.Key())
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"kind":"axm.device","account_id":"account","resource_id":"opaque/id"}` {
		t.Fatal(string(raw))
	}
	if ref.Key() == (source.Reference{Kind: ref.Kind, AccountID: "other", ResourceID: ref.ResourceID}).Key() {
		t.Fatal("account collision")
	}
}
