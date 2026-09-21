package schemagen

import (
	"reflect"
	"testing"
)

// TestReferencesRetainContextOfSharedGoTypes checks references retain context of shared go types.
func TestReferencesRetainContextOfSharedGoTypes(t *testing.T) {
	// A reused Go representation must retain each schema field's asset types.
	credential := func(name, typ string) Key {
		return Key{Key: name, Type: "<array>", Subkeys: []Key{
			{Key: "Item", Type: "<dictionary>", Subkeys: []Key{
				{Key: "AssetReference", Type: "<string>", AssetTypes: []string{typ}},
			}},
		}}
	}
	fields := []Key{{Key: "ExtensionConfigs", Type: "<dictionary>", Subkeys: []Key{
		{Key: "ANY", Type: "<dictionary>", Subkeys: []Key{
			credential("Passwords", "password"),
			credential("Certificates", "certificate"),
		}},
	}}}
	refs := referencePaths(fields, nil, false)
	want := []referencePath{
		{[]string{"ExtensionConfigs", "*", "Passwords", "*", "AssetReference"}, []string{"password"}},
		{[]string{"ExtensionConfigs", "*", "Certificates", "*", "AssetReference"}, []string{"certificate"}},
	}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("%+v", refs)
	}
}

// TestReferenceArrays checks discovery of declaration references inside arrays.
func TestReferenceArrays(t *testing.T) {
	refs := referencePaths([]Key{{Key: "PublicKeys", Type: "<array>", AssetTypes: []string{"data"}, Subkeys: []Key{{Key: "Key", Type: "<string>"}}}}, nil, false)
	want := []referencePath{{[]string{"PublicKeys", "*"}, []string{"data"}}}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("%+v", refs)
	}
}
