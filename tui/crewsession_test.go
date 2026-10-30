package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// crewAgents is modelAgents whose window holds a crew scope on a real crew.Manager, in a workspace
// with an id, so its agents start as crew sessions.
func crewAgents(t *testing.T, profiles string) (*agents, *identityHost, *crewWindow) {
	t.Helper()
	c := newIdentityHost(profiles)
	c.ws = wsInfo{id: 7, name: "kb", root: t.TempDir()}
	a := newAgents(c, t.TempDir(), "")
	w := &crewWindow{d: newCrewDaemonForHost()}
	a.crew = newCrewHost(w, t.TempDir())
	a.crew.Connected()
	if _, _, ok := a.crew.held(); !ok {
		t.Fatalf("the window holds no scope: %v", w.notices)
	}
	return a, c, w
}

const oneTwo = `[{"name":"One","command":"one","codename":"one"},{"name":"Two","command":"two","codename":"two"}]`

func readContext(t *testing.T, dir string) (crew.Context, os.FileInfo, bool) {
	t.Helper()
	path := filepath.Join(dir, crew.ContextFile)
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return crew.Context{}, nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c crew.Context
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c, fi, true
}

// TestAnAgentIsRegisteredBeforeItStarts (ADR 1793271500 §4.4, §4.5): an agent of a window holding a
// scope starts only once its crew session is registered and its context written (0600) in its
// folder, which its process is told; its exit ends the session and removes the context, so no
// later process reads a dead session's credential, and the codename is free for its next session.
func TestAnAgentIsRegisteredBeforeItStarts(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
	scope, _, _ := a.crew.held()
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil || !instance.running || instance.session.ID == "" {
		t.Fatalf("start: %v, running %v, session %q", err, instance != nil && instance.running, instance.session.ID)
	}
	dir := c.panel.configs[instance.tab].Dir
	ctx, fi, ok := readContext(t, dir)
	if !ok || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the context: present %v, mode %v; want 0600", ok, fi)
	}
	if ctx.Session != instance.session.ID || ctx.Scope != scope || ctx.Kind != "one" || ctx.Store != "store-1" ||
		ctx.Socket != "/run/test/d.sock" || !strings.HasPrefix(ctx.Credential, ctx.Session+".") || ctx.Profile == "" {
		t.Fatalf("the context: %+v", ctx)
	}
	if !slices.Contains(c.panel.configs[instance.tab].Args, "AUTODOC_CREW_CONTEXT="+dir) {
		t.Fatalf("the process isn't told its context: %v", c.panel.configs[instance.tab].Args)
	}
	first := instance.session.ID
	c.panel.exit(instance.tab, 0)
	if _, _, ok := readContext(t, dir); ok {
		t.Fatal("the context is still there after the agent exited")
	}
	again, err := a.startInstance(p)
	if err != nil || !again.running || again.session.ID == "" || again.session.ID == first {
		t.Fatalf("its next start: %v, session %q (the first was %q): want a new session, the codename free", err, again.session.ID, first)
	}
}

// TestARefusedRegistrationStartsNothing: a registration the daemon refuses starts no process, writes
// no context, and says why.
func TestARefusedRegistrationStartsNothing(t *testing.T) {
	a, c, w := crewAgents(t, oneTwo)
	w.d.refuse = errors.New("the codename is busy")
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if instance.running || instance.starting || c.panel.tabs[0].Running {
		t.Fatalf("a refused registration started the agent: running %v starting %v", instance.running, instance.starting)
	}
	if _, _, ok := readContext(t, a.agentFolder(c.ws, "one")); ok {
		t.Fatal("a refused registration left a context")
	}
	if len(c.notes) == 0 || !strings.Contains(c.notes[len(c.notes)-1], "did not start") {
		t.Fatalf("notices %v, want one saying the agent did not start", c.notes)
	}
}

// TestAFailedSpawnEndsItsSession: a start that fails after its session was registered ends the
// session (start_failed) and removes its context.
func TestAFailedSpawnEndsItsSession(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
	c.panel = &modelPanel{owner: c, startErr: errors.New("no such program")}
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if instance.running || instance.session.ID != "" {
		t.Fatalf("a failed spawn: running %v, session %q", instance.running, instance.session.ID)
	}
	if _, _, ok := readContext(t, a.agentFolder(c.ws, "one")); ok {
		t.Fatal("a failed spawn left its context")
	}
	if len(a.crew.sessions) != 0 {
		t.Fatalf("a failed spawn's session is still counted live: %v", a.crew.sessions)
	}
	c.panel.startErr = nil
	if again, err := a.startInstance(p); err != nil || !again.running {
		t.Fatalf("a start after the failed one: %v (its codename still busy?)", err)
	}
}

// TestAClaudeSessionAcceptsWakes (§4.9): a Claude agent's crew session gets the SessionStart hook
// that registers its endpoint and crossSessionInbound "accept", nothing about tool approvals; an
// agent of another CLI gets neither.
func TestAClaudeSessionAcceptsWakes(t *testing.T) {
	a, c, _ := crewAgents(t, `[{"name":"Claude","command":"claude --model haiku","codename":"cl"},{"name":"One","command":"one","codename":"one"}]`)
	for code, wants := range map[string]bool{"cl": true, "one": false} {
		p, _ := a.profile(code)
		instance, err := a.startInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		dir := c.panel.configs[instance.tab].Dir
		b, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
		var s map[string]any
		_ = json.Unmarshal(b, &s)
		accepts := s["crossSessionInbound"] == "accept" && strings.Contains(string(b), "crew endpoint --claude")
		if accepts != wants {
			t.Errorf("%s's settings accept wakes %v, want %v:\n%s", code, accepts, wants, b)
		}
		for _, never := range []string{"permissions", "allow", "deny", "defaultMode"} {
			if strings.Contains(string(b), never) {
				t.Errorf("%s's settings name %q: a wake never loosens tool approvals", code, never)
			}
		}
	}
}

// TestAQuitRemovesEverySessionsContext: the window's clean quit, which releases its scope and so
// ends every session, removes each session's context too.
func TestAQuitRemovesEverySessionsContext(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
	var dirs []string
	for _, code := range []string{"one", "two"} {
		p, _ := a.profile(code)
		instance, err := a.startInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, c.panel.configs[instance.tab].Dir)
	}
	a.crew.Stopped()
	for _, dir := range dirs {
		if _, _, ok := readContext(t, dir); ok {
			t.Errorf("%s's context outlived the quit", dir)
		}
	}
}

// TestADeliveryWaitsForItsAgentsRegistration: a task handed to an agent it starts waits while the
// agent's crew session is registered, and reaches the agent once it runs, never as a stale send.
func TestADeliveryWaitsForItsAgentsRegistration(t *testing.T) {
	a, c, w := crewAgents(t, oneTwo)
	c.panel = &modelPanel{owner: c} // takes a bracketed paste at once
	w.hold = true
	calls := 0
	var outcome error
	if err := a.HandTask(AgentRef{Codename: "one"}, TaskRef{Title: "task", Path: "/task.md"}, "work", func(err error) { calls++; outcome = err }); err != nil {
		t.Fatal(err)
	}
	c.questions[0].OnYes()
	c.drainPosts()
	instance := a.instances["one"]
	if instance == nil || !instance.starting || calls != 0 || len(c.panel.pastes) != 0 {
		t.Fatalf("while registering: starting %v, calls %d (%v), pastes %d", instance != nil && instance.starting, calls, outcome, len(c.panel.pastes))
	}
	w.release()
	c.fireAfters()
	if calls != 1 || outcome != nil || len(c.panel.pastes) != 1 || !instance.running {
		t.Fatalf("after the registration: calls %d, outcome %v, pastes %d, running %v; want the task delivered once", calls, outcome, len(c.panel.pastes), instance.running)
	}
}
