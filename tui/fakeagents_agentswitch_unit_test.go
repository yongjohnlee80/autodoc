//go:build linux || darwin

package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// Copies of the agents' test fakes agentswitch_unit_test.go declares, for the host's cells.

// recordingAgent is an agent CLI that, its tty raw, writes every byte it is sent to report. With
// bracketed it asks for bracketed paste (mode 2004), as agent CLIs do.
func recordingAgentCopy(t *testing.T, bracketed bool) (agent, report string) {
	t.Helper()
	dir := t.TempDir()
	agent, report = filepath.Join(dir, "recording-agent"), filepath.Join(dir, "report")
	mode := ""
	if bracketed {
		mode = `printf '\033[?2004h'` + "\n"
	}
	script := "#!/bin/sh\nstty raw -echo\n" + mode + "printf 'ready\\r\\n'\nexec cat > " + shellQuoteCopy(report) + "\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return agent, report
}

func reportedCopy(report string) string { b, _ := os.ReadFile(report); return string(b) }
