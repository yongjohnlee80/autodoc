package ipc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

// playAnotherUser makes this process expect another uid than its own (PlayUser), so every real peer
// (this test's own connections) is "another user's", until the restore it answers with, or the
// test's end.
func playAnotherUser(t *testing.T) (restore func()) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("no peer credentials on " + runtime.GOOS)
	}
	restore = PlayUser(os.Getuid() + 1)
	t.Cleanup(restore)
	return restore
}

// serve accepts on ln until it closes, handing each connection to accepted; the returned wait
// closes ln and waits for the loop to end.
func serve(ln net.Listener, accepted chan<- net.Conn) (wait func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	return func() { _ = ln.Close(); <-done }
}

// TestTheDaemonDropsAnotherUsersConnection: a connection whose peer is another user's process is
// closed before a byte is read, said to refused, and never returned by Accept. The control: the
// same listener, expecting this user, hands the same kind of connection on.
func TestTheDaemonDropsAnotherUsersConnection(t *testing.T) {
	raw, path := socketIn(t)
	refused := make(chan error, 1)
	accepted := make(chan net.Conn, 1)
	ln := Guard(raw, func(err error) { refused <- err })

	restore := playAnotherUser(t)
	wait := serve(ln, accepted)
	c, err := rawDial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case err := <-refused:
		if !errors.Is(err, ErrOtherUser) {
			t.Fatalf("refused with %v, want ErrOtherUser", err)
		}
	case a := <-accepted:
		_ = a.Close()
		t.Fatal("another user's connection was accepted")
	case <-time.After(5 * time.Second):
		t.Fatal("the connection was neither accepted nor refused")
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := c.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("the refused connection read %d, %v; want it closed (EOF)", n, err)
	}
	wait()

	restore() // the control: this user's connection is handed on
	raw2, path2 := socketIn(t)
	wait2 := serve(Guard(raw2, func(err error) { t.Errorf("this user's connection refused: %v", err) }), accepted)
	defer wait2()
	c2, err := rawDial(path2)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	select {
	case a := <-accepted:
		_ = a.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("this user's connection was not accepted")
	}
}

// TestAClientRefusesAnotherUsersDaemon: Dial closes and refuses a socket whose listener is another
// user's process (a socket put at the daemon's path first); this user's is dialled.
func TestAClientRefusesAnotherUsersDaemon(t *testing.T) {
	raw, path := socketIn(t)
	accepted := make(chan net.Conn, 4)
	wait := serve(raw, accepted)
	defer wait()
	ctx := context.Background()

	c, err := Dial(ctx, path)
	if err != nil {
		t.Fatalf("this user's daemon: %v", err)
	}
	_ = c.Close()

	playAnotherUser(t)
	if c, err := Dial(ctx, path); !errors.Is(err, ErrOtherUser) {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("another user's daemon: %v, want ErrOtherUser", err)
	}
}

// PeerPID names the process at the other end, from each end of a connection (here this test's own
// process at both), and refuses a connection that is not ipc's rather than pass.
func TestPeerPIDNamesTheProcessAtTheOtherEnd(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("no peer process record on " + runtime.GOOS)
	}
	ln, path := socketIn(t)
	accepted := make(chan net.Conn, 1)
	defer serve(ln, accepted)()
	c, err := rawDial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	server := <-accepted
	defer server.Close()
	for end, conn := range map[string]net.Conn{"client": c, "server": server} {
		if pid, err := PeerPID(conn); err != nil || pid != os.Getpid() {
			t.Errorf("PeerPID at the %s's end: %d, %v; want this process, %d", end, pid, err, os.Getpid())
		}
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err := PeerPID(a); err == nil {
		t.Error("PeerPID of a pipe answered a process, rather than refusing")
	}
}
