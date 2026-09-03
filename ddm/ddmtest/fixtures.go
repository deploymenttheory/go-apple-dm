package ddmtest

import (
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	schemaddm "github.com/deploymenttheory/go-apple-dm/schema/ddm"
)

// t0 is the fixed clock every suite uses; stores never read the wall clock.
var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// typeFor picks a representative declaration type for a kind.
func typeFor(kind schemaddm.Kind) string {
	switch kind {
	case schemaddm.KindConfiguration:
		return "com.apple.configuration.test"
	case schemaddm.KindActivation:
		return "com.apple.activation.simple"
	case schemaddm.KindAsset:
		return "com.apple.asset.data"
	case schemaddm.KindManagement:
		return "com.apple.management.properties"
	case schemaddm.KindCredential, schemaddm.KindBase:
	}
	return "com.apple." + string(kind) + ".test"
}

// Decl builds a declaration whose Canonical is exactly
// {"Identifier":"<identifier>","Payload":<payload>,"Type":"<type>"} with a
// type derived from kind and ServerToken = ddm.TokenFor(Canonical). Both
// times are t0.
func Decl(identifier string, kind schemaddm.Kind, payload string) *ddm.Declaration {
	typ := typeFor(kind)
	canonical := []byte(`{"Identifier":"` + identifier + `","Payload":` + payload + `,"Type":"` + typ + `"}`)
	return &ddm.Declaration{
		Identifier: identifier, Type: typ, Kind: kind,
		ServerToken: ddm.TokenFor(canonical), Canonical: canonical,
		CreatedAt: t0, UpdatedAt: t0,
	}
}

// Device returns the device-channel identity DEVICE-<nn>.
func Device(n int) mdm.EnrollmentID {
	return mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: fmt.Sprintf("DEVICE-%02d", n)}
}

// User returns the user-channel identity "<device>:<u>" of Device(n).
func User(n int, u string) mdm.EnrollmentID {
	d := Device(n)
	return mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: d.ID + ":" + u, ParentID: d.ID}
}
