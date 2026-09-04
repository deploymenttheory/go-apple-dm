package simulator_test

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
)

func TestSharedIPadUser(t *testing.T) {
	d := simulator.New("UDID-SIP")
	u := d.SharedIPadUser("student", "Student One")
	if u.UserID != mdm.SharedIPadUserID || u.ShortName != "student" || u.LongName != "Student One" || u.Device != d {
		t.Fatalf("shared iPad user = %+v", u)
	}
	id, err := (mdm.Enrollment{UDID: d.UDID, UserID: u.UserID, UserShortName: u.ShortName}).Resolve()
	if err != nil || id.Channel != mdm.ChannelSharedIPadUser || id.ID != "UDID-SIP:student" {
		t.Fatalf("resolve = %+v %v", id, err)
	}
}
