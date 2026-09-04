package predicate_test

import (
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm/predicate"
)

func ExampleParse() {
	p, err := predicate.Parse(
		`(@property(shard) <= 75) and @status(device.identifier.serial-number) beginswith 'ZYXW'`,
	)
	if err != nil {
		fmt.Println("parse:", err)
		return
	}
	env := predicate.MapEnv{
		Properties:  map[string]any{"shard": 40},
		StatusItems: map[string]any{"device.identifier.serial-number": "ZYXW4321"},
	}
	ok, err := p.Eval(env)
	fmt.Println(p)
	fmt.Println(ok, err)

	_, err = predicate.Parse(`SELF.name LIKE 'a*'`)
	fmt.Println(err)
	// Output:
	// @property(shard) <= 75 AND @status(device.identifier.serial-number) BEGINSWITH 'ZYXW'
	// true <nil>
	// predicate: unsupported construct at offset 0: SELF is not supported
}
