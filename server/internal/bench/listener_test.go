package bench

import (
	"net"
	"testing"
)

func TestEmbeddedAddressRetainsPort(t *testing.T) {
	t.Parallel()
	addr, listener, err := address(t.Context(), "127.0.0.1:0", true)
	if err != nil {
		t.Fatal(err)
	}
	if listener == nil {
		t.Fatal("embedded runtime lost the reserved socket")
	}
	defer listener.Close()
	if addr != listener.Addr().String() {
		t.Fatalf("advertised %s; bound %s", addr, listener.Addr())
	}
	if other, err := net.Listen("tcp", addr); err == nil {
		other.Close()
		t.Fatal("embedded address released before runtime startup")
	}
}
