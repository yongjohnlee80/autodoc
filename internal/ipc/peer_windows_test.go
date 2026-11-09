//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"

	"github.com/Microsoft/go-winio"
)

// The Windows fixture of the contract suite (peer_test.go): a named pipe with the user-only DACL,
// dialled as a client does; and the Windows form of CheckDir's cells.

var pipeSeq atomic.Int64

// testPipe is a pipe name of this test's own.
func testPipe() string {
	return fmt.Sprintf(`\\.\pipe\autodoc-test-%d-%d`, os.Getpid(), pipeSeq.Add(1))
}

// socketIn listens on a fresh named pipe with the user-only DACL, unguarded.
func socketIn(t *testing.T) (net.Listener, string) {
	t.Helper()
	sddl, err := userOnlySDDL()
	if err != nil {
		t.Fatal(err)
	}
	path := testPipe()
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		t.Fatal(err)
	}
	return ln, path
}

// rawDial connects to the pipe at path without Dial's checks, as a client of any kind can; it is a
// client's end (its peer is the server).
func rawDial(path string) (net.Conn, error) {
	c, err := winio.DialPipeContext(context.Background(), path)
	if err != nil {
		return nil, err
	}
	return &clientConn{Conn: c}, nil
}

// A pipe whose DACL admits another user (here everyone, WD) is refused by Dial, before a byte: the
// Windows form of a socket in a folder others can write. The user-only pipe is the control.
func TestDialRefusesAPipeWithAWiderAccessList(t *testing.T) {
	wide := testPipe()
	ln, err := winio.ListenPipe(wide, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;WD)"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	var unsafe *UnsafeDirError
	if c, err := Dial(context.Background(), wide); !errors.As(err, &unsafe) {
		if err == nil {
			_ = c.Close()
		}
		t.Fatalf("Dial to a pipe everyone may open: %v, want an UnsafeDirError", err)
	}

	own, err := Listen(testPipe(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer own.Close()
	go func() {
		for {
			c, err := own.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	c, err := Dial(context.Background(), own.addr)
	if err != nil {
		t.Fatalf("Dial to this user's own pipe: %v", err)
	}
	_ = c.Close()
}

// A second Listen at a live pipe's name is ErrInUse, and the live pipe keeps answering; a name that
// is not a pipe's is refused before anything is created.
func TestListenOnAPipeInUseOrNotAPipe(t *testing.T) {
	name := testPipe()
	first, err := Listen(name, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if ln, err := Listen(name, nil); !errors.Is(err, ErrInUse) {
		if err == nil {
			_ = ln.Close()
		}
		t.Fatalf("a second Listen at a live pipe: %v, want ErrInUse", err)
	}
	var unsafe *UnsafeDirError
	if _, err := Listen(`C:\not-a-pipe`, nil); !errors.As(err, &unsafe) {
		t.Errorf("Listen at a name that is not a pipe's: %v, want an UnsafeDirError", err)
	}
}
