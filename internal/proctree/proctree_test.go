package proctree

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// The contract every OS keeps: a program StartDetached starts is detached (a session of its own on
// Unix; no console on Windows: detachedFact) and Alive while it runs; once it has ended it is not,
// and neither is a pid nothing has. The helper is this test binary run again.
func TestAProgramStartedDetachedLivesApartAndIsAskedAfter(t *testing.T) {
	if os.Getenv("PROCTREE_HELPER") != "" {
		proctreeHelper()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAProgramStartedDetachedLivesApartAndIsAskedAfter$")
	cmd.Env = append(os.Environ(), "PROCTREE_HELPER=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := StartDetached(cmd); err != nil {
		t.Fatalf("StartDetached: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = in.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	fact, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the helper said nothing: %v", err)
	}
	if !detached(t, fact[:len(fact)-1]) {
		t.Errorf("the program started detached is not: %q", fact)
	}
	if !Alive(cmd.Process.Pid) {
		t.Errorf("Alive(%d) of the running program is false", cmd.Process.Pid)
	}
	if !Alive(os.Getpid()) {
		t.Error("Alive of this very process is false")
	}
	_ = in.Close() // the helper ends
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the helper did not end when its stdin closed")
	}
	if Alive(cmd.Process.Pid) {
		t.Errorf("Alive(%d) of the program that ended is true", cmd.Process.Pid)
	}
	if Alive(0) || Alive(-1) {
		t.Error("Alive of a pid nothing has is true")
	}
}

// proctreeHelper says how it is detached (detachedFact), then lives until its stdin closes.
func proctreeHelper() {
	os.Stdout.WriteString(detachedFact() + "\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
