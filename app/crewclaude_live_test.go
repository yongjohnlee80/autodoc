//go:build darwin || linux

package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestThisClaudeSessionRegistersAnEndpoint runs only under Claude Code: `crew endpoint --claude`,
// as this session's SessionStart hook runs it, makes a locator for this session's own inbox socket
// and process, its socket in a folder the daemon accepts and its start this process's reference.
// On macOS v0.3.10 failed here ("process references need /proc"), so no Claude agent was woken.
func TestThisClaudeSessionRegistersAnEndpoint(t *testing.T) {
	if os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET") == "" {
		t.Skip("not under Claude Code")
	}
	loc, err := claudeLocator(strings.NewReader(`{"session_id":"probe"}`), os.Getenv, os.Getuid())
	if err != nil {
		t.Fatalf("crew endpoint --claude: %v", err)
	}
	var l struct {
		Socket string `json:"socket"`
		PID    int    `json:"pid"`
		Start  string `json:"start"`
	}
	if err := json.Unmarshal([]byte(loc), &l); err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, d := range crew.ClaudeSocketDirs(os.Getuid()) {
		ok = ok || strings.HasPrefix(l.Socket, d+"/")
	}
	if !ok {
		t.Errorf("socket %s lies outside %v: the daemon would refuse it", l.Socket, crew.ClaudeSocketDirs(os.Getuid()))
	}
	if crew.ProcGone(l.Start) || !strings.Contains(l.Start, ":") {
		t.Errorf("start %q doesn't name the live Claude process %d", l.Start, l.PID)
	}
	t.Logf("locator: %s", loc)
}
