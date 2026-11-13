package proctree

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
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

// The contract every OS keeps: Replace runs the program with the arguments and environment it is
// given, on this process's output, and whoever waits on this process sees the program's exit code.
// A program that cannot start is an error, and the caller goes on. The helpers are this test binary
// run again.
func TestReplaceRunsTheProgramInThisOnesPlace(t *testing.T) {
	const run = "-test.run=^TestReplaceRunsTheProgramInThisOnesPlace$"
	switch os.Getenv("PROCTREE_REPLACE") {
	case "first":
		err := Replace(os.Args[0], []string{os.Args[0], run, "the-new-argument"}, procenv.Env(os.Environ()).With("PROCTREE_REPLACE=second"))
		os.Stdout.WriteString("Replace returned: " + err.Error() + "\n")
		os.Exit(9)
	case "second":
		os.Stdout.WriteString("the program ran with " + os.Args[len(os.Args)-1] + "\n")
		os.Exit(37)
	}
	cmd := exec.Command(os.Args[0], run)
	cmd.Env = append(os.Environ(), "PROCTREE_REPLACE=first")
	out, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 37 {
		t.Errorf("the process that replaced itself ended with %v; want the program's exit code, 37", err)
	}
	if !strings.Contains(string(out), "the program ran with the-new-argument") {
		t.Errorf("the program's output on this process's: %q", out)
	}
	missing := t.TempDir() + string(os.PathSeparator) + "no-such-program"
	if err := Replace(missing, []string{missing}, os.Environ()); err == nil {
		t.Error("Replace of a program that does not exist returned no error")
	}
}
