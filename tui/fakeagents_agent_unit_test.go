package tui

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The host's agent stand-ins, on every OS: this test binary, copied in under the stand-in's name
// (standIn; agentsetup_rig_test.go's init runs it as that), each writing its report beside itself.

// fakeAgent is an agent CLI that writes where it started, with what, what it was told, and which
// autodoc it calls, to report (the screen wraps long paths), calls it, then echoes what it is sent
// until told to exit.
func fakeAgent(t *testing.T) (agent, report string) {
	t.Helper()
	dir := shellTempDir(t, "autodoc-fake-agent-") // its folder outlives it as Windows needs (shellTempDir)
	return standIn(t, dir, "fake-agent"), filepath.Join(dir, "report")
}

// fakeAgentMain is the test binary as fakeAgent's agent: it writes report beside itself (its
// working folder, its arguments, the workspace's env and FAKE_GREETING, the names in its working
// folder in byte order, and the autodoc its PATH finds), runs autodoc --call workspace.list, says it
// started, then answers each line it is sent with heard-<line>, until a line "exit N" ends it with N.
func fakeAgentMain() int {
	var b strings.Builder
	wd, _ := os.Getwd()
	fmt.Fprintf(&b, "cwd=%s\nargs=%s\nenv=%s|%s|%s\n", wd, strings.Join(os.Args[1:], " "),
		os.Getenv("AUTODOC_WORKSPACE"), os.Getenv("AUTODOC_ROOT"), os.Getenv("FAKE_GREETING"))
	if entries, err := os.ReadDir("."); err == nil { // sorted by name, as LC_ALL=C ls lists them
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				fmt.Fprintln(&b, e.Name())
			}
		}
	}
	autodoc, _ := exec.LookPath("autodoc")
	fmt.Fprintf(&b, "autodoc=%s\n", autodoc)
	if err := os.WriteFile(besideMe("report"), []byte(b.String()), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "fake agent:", err)
		return 1
	}
	if autodoc != "" {
		call := exec.Command(autodoc, "--call", "workspace.list")
		call.Stdout, call.Stderr = os.Stdout, os.Stderr
		_ = call.Run()
	}
	first := ""
	if len(os.Args) > 1 {
		first = os.Args[1]
	}
	fmt.Printf("started %s\n", first)
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		line := strings.TrimRight(in.Text(), "\r")
		if rest, ok := strings.CutPrefix(line, "exit"); ok {
			code, _ := strconv.Atoi(strings.TrimSpace(rest))
			return code
		}
		fmt.Printf("heard-%s\n", line)
	}
	return 0
}
