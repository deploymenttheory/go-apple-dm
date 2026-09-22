package lab

import (
	"net"
	"testing"
)

// TestEmbeddedAddressRetainsPort checks that embedded address retains port.
func TestEmbeddedAddressRetainsPort(t *testing.T) {
	t.Parallel()
	addr, listener, err := address(t.Context(), "127.0.0.1:0", true)
	if err != nil {
		t.Fatal(err)
	}
	if listener == nil {
		t.Fatal("embedded runtime lost the reserved socket")
	}
	defer func(cleanup func() error) { _ = cleanup() }(listener.Close)
	if addr != listener.Addr().String() {
		t.Fatalf("advertised %s; bound %s", addr, listener.Addr())
	}
	if other, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", addr); err == nil {
		_ = other.Close()
		t.Fatal("embedded address released before runtime startup")
	}
}
