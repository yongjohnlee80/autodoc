package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/term"
)

// recordingAgent is an agent CLI that, its terminal raw, writes every byte it is sent to report.
// With bracketed it asks for bracketed paste (mode 2004), as agent CLIs do. It is this test binary
// on every OS (recordingAgentMain): on Windows its terminal is the console's, its input VT
// sequences, as an agent CLI reads it.
func recordingAgent(t *testing.T, bracketed bool) (agent, report string) {
	t.Helper()
	dir := shellTempDir(t, "autodoc-recording-agent-")
	agent, report = standIn(t, dir, "recording-agent"), filepath.Join(dir, "report")
	if bracketed {
		if err := os.WriteFile(filepath.Join(dir, "bracketed"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return agent, report
}

// recordingAgentMain is the test binary as recordingAgent's agent: its terminal raw, it asks for
// bracketed paste when "bracketed" is beside it, says ready, then writes every byte it is sent to
// report beside itself, the file open only while it is written.
func recordingAgentMain() int {
	if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil {
		fmt.Fprintln(os.Stderr, "recording agent: raw input:", err)
		return 1
	}
	if _, err := os.Stat(besideMe("bracketed")); err == nil {
		fmt.Print("\x1b[?2004h")
	}
	fmt.Print("ready\r\n")
	if err := os.WriteFile(besideMe("report"), nil, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "recording agent:", err)
		return 1
	}
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			f, ferr := os.OpenFile(besideMe("report"), os.O_WRONLY|os.O_APPEND, 0o600)
			if ferr != nil {
				fmt.Fprintln(os.Stderr, "recording agent:", ferr)
				return 1
			}
			_, _ = f.Write(buf[:n])
			f.Close()
		}
		if err != nil { // its input closed: the agent's end
			return 0
		}
	}
}

func reported(report string) string { b, _ := os.ReadFile(report); return string(b) }
