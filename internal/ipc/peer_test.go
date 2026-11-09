package ipc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
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

// PeerPID names the process at the OTHER end, not this one: this test binary, run again as a helper
// in a process of its own, is named by its pid at the daemon's end when it dials, and at a client's
// end when it serves. (Both ends in one process could not tell a peer's pid from this process's.)
func TestPeerPIDNamesAnotherProcess(t *testing.T) {
	if addr := os.Getenv("IPC_PEER_HELPER"); addr != "" {
		peerHelper(os.Getenv("IPC_PEER_HELPER_ROLE"), addr)
		return
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("no peer process record on " + runtime.GOOS)
	}
	helper := func(role, addr string) (*exec.Cmd, *bufio.Reader, io.WriteCloser) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestPeerPIDNamesAnotherProcess$")
		cmd.Env = append(os.Environ(), "IPC_PEER_HELPER="+addr, "IPC_PEER_HELPER_ROLE="+role)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { // closing stdin ends the helper; one that has not ended in 5s is killed
			_ = in.Close()
			done := make(chan struct{})
			go func() { _ = cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		})
		return cmd, bufio.NewReader(out), in
	}
	ready := func(r *bufio.Reader, want string) {
		t.Helper()
		if line, err := r.ReadString('\n'); err != nil || line != want+"\n" {
			t.Fatalf("the helper said %q, %v; want %s", line, err, want)
		}
	}

	// the daemon's end: a client in another process
	ln, addr := socketIn(t)
	defer ln.Close()
	client, out, _ := helper("dial", addr)
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ready(out, "dialled")
	if pid, err := PeerPID(server); err != nil || pid != client.Process.Pid || pid == os.Getpid() {
		t.Errorf("PeerPID at the daemon's end: %d, %v; want the client's process, %d (this one is %d)", pid, err, client.Process.Pid, os.Getpid())
	}

	// a client's end: a server in another process
	addr2 := freshAddr(t)
	srv, out2, _ := helper("serve", addr2)
	ready(out2, "listening")
	c, err := rawDial(addr2)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if pid, err := PeerPID(c); err != nil || pid != srv.Process.Pid || pid == os.Getpid() {
		t.Errorf("PeerPID at a client's end: %d, %v; want the server's process, %d (this one is %d)", pid, err, srv.Process.Pid, os.Getpid())
	}
}

// peerHelper is TestPeerPIDNamesAnotherProcess's other process: it dials addr, or serves at it and
// accepts once, says so, and holds the connection until its stdin closes. Its stdin closing ends it
// whatever it is doing, an Accept still waiting among them: the test's failure paths close it too.
func peerHelper(role, addr string) {
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}()
	var c net.Conn
	switch role {
	case "dial":
		var err error
		if c, err = rawDial(addr); err != nil {
			os.Exit(2)
		}
		fmt.Println("dialled")
	case "serve":
		ln, err := rawListen(addr)
		if err != nil {
			os.Exit(2)
		}
		fmt.Println("listening")
		if c, err = ln.Accept(); err != nil {
			os.Exit(2)
		}
	}
	_ = c
	time.Sleep(time.Hour) // until stdin closes, which exits the process
}
