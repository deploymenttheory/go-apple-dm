package ddm_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
)

func TestStatusStructuralLimitsBeforePersistence(t *testing.T) {
	s := inmem.New()
	e, err := ddm.New(
		ddm.Config{Store: s, MaxStatusDepth: 8, MaxStatusPathBytes: 32, MaxStatusItems: 20},
	)
	if err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "device"}
	for _, body := range []string{`{"StatusItems":` + strings.Repeat(`{"a":`, 10) + `0` + strings.Repeat(`}`, 10) + `}`, `{"StatusItems":{"` + strings.Repeat("x", 40) + `":1}}`, `{"StatusItems":{"a":[` + strings.Repeat("0,", 21) + `0]}}`} {
		if _, err := e.Status(
			t.Context(),
			id,
			[]byte(body),
		); !errors.Is(
			err,
			ddm.ErrStatusTooLarge,
		) {
			t.Fatal(err)
		}
	}
	if _, err := e.Status(
		t.Context(),
		id,
		[]byte(`{"StatusItems":{"test":{"value":1}}}`),
	); err != nil {
		t.Fatal("bounded status rejected", err)
	}
}
