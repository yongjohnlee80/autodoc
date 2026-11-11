package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corecrew "github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/userperm/userpermtest"
	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/crew"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// The crew mail's rig the host's agents cells share: copies of tui/crew's, which its own cells use
// there, on the host's seams (seamsFor, seamsOf).

// A copy of tui/crew's crewAgents (crewsession_test.go), on the host's seams.
// crewAgents is modelAgents whose window holds a crew scope on a real corecrew.Manager, in a workspace
// with an id, so its agents start as crew sessions.
func crewAgents(t *testing.T, profiles string) (*agents.Agents, *identityHost, *crewWindow) {
	t.Helper()
	return crewAgentsOn(t, profiles, !agents.CanRunAgentServers)
}

// crewAgentsOn is crewAgents on a host that can run an agent's own server or, noServer, one that
// can't (as the window on a system without process groups: its setups are asked with
// NoAgentServer). A Codex agent's setup there asks Codex whether it takes --no-daemon: a stand-in
// codex is on the setup's PATH, which says it does.
func crewAgentsOn(t *testing.T, profiles string, noServer bool) (*agents.Agents, *identityHost, *crewWindow) {
	t.Helper()
	c := newIdentityHost(profiles)
	c.ws = wsInfo{id: 7, name: "kb", root: t.TempDir()}
	s := seamsFor(t)
	if s == nil {
		s = &seams{}
		*s = defaultSeams
	}
	agentDir := agentsFolder(t) // on Windows an agent's process works in it, and its removal waits for the process to exit
	userpermtest.ExposeNew(t, agentDir) // a file made there the ordinary way is exposed: only a private create keeps it private
	a := testAgentsWith(c, s, agentDir, "")
	// a crew start's setup, in-process from the same source `crew setup` answers from, offering no
	// MCP server (a cell that wants one sets its own): every kind as it is without one, on CLI mail.
	// A Codex agent still gets its own app-server (an agent folder gives one in every mail mode),
	// played by a stand-in that makes its socket: no Codex is installed where the cells run.
	data, path := t.TempDir(), "/nonexistent"
	if noServer {
		path = t.TempDir()
		standIn(t, path, "codex")
	}
	seamsOf(a).agentSetupRun = func(kind, dir string, environ []string) (corecrew.Setup, error) {
		s, err := corecrew.SetupFor(corecrew.SetupInput{Kind: kind, Host: corecrew.HostWindow, Env: map[string]string{"PATH": path},
			DataDir: data, Dir: dir, AgentDir: dir, NoAgentServer: noServer}, "")
		if err == nil && len(s.Server) > 0 {
			s.Server = fakeServer(t, s.ServerSocket, true)
		}
		return s, err
	}
	w := &crewWindow{d: newCrewDaemonForHost()}
	a.Crew = crew.NewHost(w, s.env, t.TempDir(), crew.HostHooks{})
	crewOf(a).Connected()
	if _, _, ok := crewOf(a).Held(); !ok {
		t.Fatalf("the window holds no scope: %v", w.notices)
	}
	return a, c, w
}

// A copy of tui/crew's startingCrewAgents (crewsession_test.go), on the host's seams.
// startingCrewAgents is crewAgents whose tabs' starts run their starter (runAgent), as the terminal
// does, on a spawn that starts nothing: a start its starter refuses (a refused registration shown in
// its tab) leaves the tab stopped, as the terminal leaves it. The process it starts is a fake
// terminal, never nil: a Codex agent's server is bound to it, and closes it when it ends.
func startingCrewAgents(t *testing.T, profiles string) (*agents.Agents, *identityHost, *crewWindow) {
	t.Helper()
	a, c, w := crewAgents(t, profiles)
	c.panel = &modelPanel{owner: c, starts: true}
	seamsOf(a).agentSpawn = func(pty.Cmd) (terminal.TerminalProcess, error) { return newFakeTerminal(), nil }
	return a, c, w
}

// A copy of tui/crew's readContext (crewsession_test.go), on the host's seams.
// readContext is the one session context in the agent folder dir (corecrew.ContextDir), and its file;
// ok is false when there is none, and more than one fails the test.
func readContext(t *testing.T, dir string) (corecrew.Context, os.FileInfo, bool) {
	t.Helper()
	names := contextFiles(t, dir)
	if len(names) == 0 {
		return corecrew.Context{}, nil, false
	}
	if len(names) > 1 {
		t.Fatalf("%s holds %d contexts: %v", dir, len(names), names)
	}
	path := corecrew.ContextPath(dir, names[0])
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c corecrew.Context
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c, fi, true
}

// A copy of tui/crew's contextFiles (crewsession_test.go), on the host's seams.
// contextFiles is the context files in the agent folder dir, by name; a write in progress (a dot
// file) is one too, so none is left behind unseen.
func contextFiles(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(dir, corecrew.ContextDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// A copy of tui/crew's fakeClaudeBin (crewsession_test.go), on the host's seams.
// fakeClaudeBin writes an executable "Claude Code" at dir/claude (claude.exe on Windows) whose binary names events as the
// real one does (quoted literals), and returns its path.
func fakeClaudeBin(t *testing.T, dir string, events ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("\x7fELF ")
	for _, e := range events {
		b.WriteString(`"` + e + `";`)
	}
	path := filepath.Join(dir, "claude"+exeSuffix) // named as this OS names a program
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A copy of tui/crew's claudeHookEvents (crewsession_test.go), on the host's seams.
// claudeHookEvents are every hook event a crew Claude agent's settings may name.
var claudeHookEvents = []string{"SessionStart", "PermissionRequest", "Notification", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "Stop"}

// A copy of tui/crew's claudeSettingsOf (crewsession_test.go), on the host's seams.
// claudeSettingsOf is dir's .claude/settings.json, read, and its text.
func claudeSettingsOf(t *testing.T, dir string) (map[string]any, string) {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	var s map[string]any
	_ = json.Unmarshal(b, &s)
	return s, string(b)
}

// A copy of tui/crew's unreportedNotices (crewsession_test.go), on the host's seams.
// unreportedNotices counts the notices in notes saying an agent's prompts aren't reported.
func unreportedNotices(notes []string) int {
	n := 0
	for _, s := range notes {
		if strings.Contains(s, "aren't reported") {
			n++
		}
	}
	return n
}

// A copy of tui/crew's promptsReported (crewsession_test.go), on the host's seams.
// promptsReported is crew.list's prompts_reported for codename, on the window's daemon, once its
// session has started (its endpoint registered, as Claude Code's SessionStart hook does past its
// startup: a Claude's prompts are reported only then).
func promptsReported(t *testing.T, w *crewWindow, codename string) bool {
	t.Helper()
	w.d.started(t, codename)
	es, err := w.d.m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Codename == codename {
			return e.PromptsReported
		}
	}
	t.Fatalf("crew.list has no %s: %+v", codename, es)
	return false
}

// A copy of tui/crew's calledTimes (crewsession_test.go), on the host's seams.
func calledTimes(w *crewWindow, verb string) int {
	n := 0
	for _, v := range w.calls {
		if v == verb {
			n++
		}
	}
	return n
}

// A copy of tui/crew's crewOf (crewsession_test.go), on the host's seams.
// crewOf is a's crew scope as the crew host it is, for a cell that reads or sets the host's own state.
func crewOf(a *agents.Agents) *crew.CrewHost { return a.Crew.(*crew.CrewHost) }
