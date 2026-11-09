package ipc

import (
	"context"
	"testing"
	"time"
)

// The contract every OS keeps: a scratch address is the daemon's kind of address, its own for each
// folder and name, the same when asked again, and one a listener and a dial can meet on.
func TestAScratchAddressIsARealAddressOfItsOwn(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	addr := ScratchAddr(dir, "a.sock")
	if again := ScratchAddr(dir, "a.sock"); again != addr {
		t.Errorf("asked again: %q, was %q", again, addr)
	}
	for _, o := range []string{ScratchAddr(dir, "b.sock"), ScratchAddr(other, "a.sock")} {
		if o == addr {
			t.Errorf("two scratch addresses are both %q", addr)
		}
	}
	if err := CheckAddr(addr); err != nil {
		t.Fatalf("CheckAddr(%q): %v", addr, err)
	}
	ln, err := Listen(addr, func(error) {})
	if err != nil {
		t.Fatalf("Listen(%q): %v", addr, err)
	}
	defer ln.Close()
	accepted := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
		accepted <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial(%q): %v", addr, err)
	}
	defer c.Close()
	if err := <-accepted; err != nil {
		t.Errorf("Accept: %v", err)
	}
}
