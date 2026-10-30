//go:build linux

package app

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestCrewEndpointClaudeNamesTheHooksSession: `crew endpoint --claude` takes the session id from the
// SessionStart hook's input over the environment's, the socket and its pid from
// $CLAUDE_CODE_MESSAGING_SOCKET, and the pid's process reference as start; with no session id, or a
// socket not named <pid>.sock, it refuses rather than register a guess.
func TestCrewEndpointClaudeNamesTheHooksSession(t *testing.T) {
	uid, pid := os.Getuid(), os.Getpid()
	sock := crew.ClaudeSocketDirs(uid)[0] + "/" + strconv.Itoa(pid) + ".sock"
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	both := env(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": sock, "CLAUDE_CODE_SESSION_ID": "from-env"})
	start, err := crew.ProcRef(pid)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		stdin string
		want  string
	}{
		"the hook's input":           {`{"session_id":"from-hook","source":"startup"}`, "from-hook"},
		"no hook input: the env's":   {"", "from-env"},
		"input that is not a hook's": {"hello", "from-env"},
	} {
		loc, err := claudeLocator(strings.NewReader(c.stdin), both, uid)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var l struct {
			Socket, Start, Session string
			PID                    int
		}
		if err := json.Unmarshal([]byte(loc), &l); err != nil {
			t.Fatal(err)
		}
		if l.Socket != sock || l.PID != pid || l.Start != start || l.Session != c.want {
			t.Errorf("%s: %+v, want socket %s, pid %d, start %s, session %s", name, l, sock, pid, start, c.want)
		}
	}
	if _, err := claudeLocator(nil, env(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": sock}), uid); err == nil {
		t.Error("no session id anywhere: registered, rather than refused")
	}
	odd := env(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks-1/x.sock", "CLAUDE_CODE_SESSION_ID": "s"})
	if _, err := claudeLocator(nil, odd, uid); err == nil {
		t.Error("a socket not named <pid>.sock: registered, rather than refused")
	}
}
