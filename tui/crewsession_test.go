package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget"

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

// readContext is the one session context in the agent folder dir (crew.ContextDir), and its file;
// ok is false when there is none, and more than one fails the test.
func readContext(t *testing.T, dir string) (crew.Context, os.FileInfo, bool) {
	t.Helper()
	names := contextFiles(t, dir)
	if len(names) == 0 {
		return crew.Context{}, nil, false
	}
	if len(names) > 1 {
		t.Fatalf("%s holds %d contexts: %v", dir, len(names), names)
	}
	path := crew.ContextPath(dir, names[0])
	fi, err := os.Stat(path)
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

// contextFiles is the context files in the agent folder dir, by name; a write in progress (a dot
// file) is one too, so none is left behind unseen.
func contextFiles(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(dir, crew.ContextDir))
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

// startedEnv is the variables instance's tab starts its program with, through the tab's own start
// (runAgent), its program not run; err is the start's refusal.
func startedEnv(a *agents, c *identityHost, instance *agentInstance) (args []string, spawned bool, err error) {
	cfg := c.panel.configs[instance.tab]
	spawn := a.spawn
	defer func() { a.spawn = spawn }()
	a.spawn = func(cmd pty.Cmd) (widget.TerminalProcess, error) { args, spawned = cmd.Args, true; return nil, nil }
	_, err = cfg.Start(pty.Cmd{Path: cfg.Command, Args: cfg.Args, Dir: cfg.Dir})
	return args, spawned, err
}

// TestAnAgentIsRegisteredBeforeItStarts (docs/decisions/crew.md#sessions-and-the-guard,
// docs/decisions/crew.md#the-session-context): an agent of a window holding a scope starts only
// once its crew session is registered and its context written (0600) in its folder, which its
// process is told; its exit ends the session and removes the context, so no later process reads a
// dead session's credential, and the codename is free for its next session.
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
	if fi.Name() != instance.session.ID {
		t.Fatalf("the context file %s: want its session's own, %s", fi.Name(), instance.session.ID)
	}
	args, _, err := startedEnv(a, c, instance)
	if err != nil || !slices.Contains(args, "AUTODOC_CREW_CONTEXT="+crew.ContextPath(dir, instance.session.ID)) {
		t.Fatalf("the process isn't told its context file: %v %v", err, args)
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
// no context, says why, and is not kept to be replayed.
func TestARefusedRegistrationStartsNothing(t *testing.T) {
	a, c, w := crewAgents(t, oneTwo)
	w.d.refuse = &golibrpc.Error{Code: crew.CodeCodenameBusy, Message: "the codename is busy"}
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
	if len(a.crew.pending) != 0 {
		t.Fatalf("a refused registration is kept to be replayed: %v", a.crew.pending)
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

// fakeClaudeBin writes an executable "Claude Code" at dir/claude whose binary names events as the
// real one does (quoted literals), and returns its path.
func fakeClaudeBin(t *testing.T, dir string, events ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("\x7fELF ")
	for _, e := range events {
		b.WriteString(`"` + e + `";`)
	}
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// claudeHookEvents are every hook event a crew Claude agent's settings may name.
var claudeHookEvents = []string{"SessionStart", "PermissionRequest", "Notification", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "Stop"}

// claudeSettingsOf is dir's .claude/settings.json, read, and its text.
func claudeSettingsOf(t *testing.T, dir string) (map[string]any, string) {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	var s map[string]any
	_ = json.Unmarshal(b, &s)
	return s, string(b)
}

// crewHookEvents are the hook events in settings s that run one of the crew's commands
// (`crew endpoint`, `crew attention`), sorted: the workspace notice's UserPromptSubmit, which every
// Claude agent gets, is not one.
func crewHookEvents(s map[string]any) []string {
	hooks, _ := s["hooks"].(map[string]any)
	var out []string
	for e, v := range hooks {
		if c := fmt.Sprint(v); strings.Contains(c, " crew endpoint ") || strings.Contains(c, " crew attention ") {
			out = append(out, e)
		}
	}
	slices.Sort(out)
	return out
}

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

// promptsReported is crew.list's prompts_reported for codename, on the window's daemon.
func promptsReported(t *testing.T, w *crewWindow, codename string) bool {
	t.Helper()
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

// TestAClaudeSessionAcceptsWakes (docs/decisions/crew.md#wake-acceptance-not-tool-approvals): a
// Claude agent's crew session, for a Claude Code that knows every hook, gets the SessionStart hook
// that registers its endpoint, crossSessionInbound "accept", and the hooks that report its
// permission prompts, nothing about tool approvals, and registers with prompts, so crew.list
// reports its prompts; an agent of another CLI gets none of it.
func TestAClaudeSessionAcceptsWakes(t *testing.T) {
	bin := t.TempDir()
	fakeClaudeBin(t, bin, append(claudeHookEvents, "crossSessionInbound")...)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	a, c, w := crewAgents(t, `[{"name":"Claude","command":"claude --model haiku","codename":"cl"},{"name":"One","command":"one","codename":"one"}]`)
	for code, wants := range map[string]bool{"cl": true, "one": false} {
		p, _ := a.profile(code)
		instance, err := a.startInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		s, b := claudeSettingsOf(t, c.panel.configs[instance.tab].Dir)
		accepts := s["crossSessionInbound"] == "accept" && strings.Contains(b, "crew endpoint --claude")
		if accepts != wants {
			t.Errorf("%s's settings accept wakes %v, want %v:\n%s", code, accepts, wants, b)
		}
		// and its permission prompts are reported, on each of the hooks that tell of them
		hooks, _ := s["hooks"].(map[string]any)
		for _, event := range claudeHookEvents[1:] {
			reported := strings.Contains(fmt.Sprint(hooks[event]), "crew attention --claude-hook")
			if reported != wants {
				t.Errorf("%s's %s hook reports its permission prompts %v, want %v", code, event, reported, wants)
			}
		}
		// the one allowance is the guide's own commands, exactly (crew.ClaudeAllow): never a deny
		// rule, a mode, or any other allow
		for _, never := range []string{"deny", "defaultMode", "autodoc *", "Bash(autodoc:"} {
			if strings.Contains(b, never) {
				t.Errorf("%s's settings name %q: a wake never loosens tool approvals beyond the guide's commands", code, never)
			}
		}
		perm, _ := s["permissions"].(map[string]any)
		var allow []string
		rules, _ := perm["allow"].([]any)
		for _, r := range rules {
			allow = append(allow, r.(string))
		}
		if wants != slices.Equal(allow, crew.ClaudeAllow()) || (!wants && perm != nil) || (perm != nil && len(perm) != 1) {
			t.Errorf("%s's permissions %v, want exactly the guide's commands allowed for a crew Claude, nothing for another", code, perm)
		}
		if got := promptsReported(t, w, code); got != wants {
			t.Errorf("%s's prompts_reported %v, want %v (its registration's prompts)", code, got, wants)
		}
	}
	if n := unreportedNotices(c.notes); n != 0 {
		t.Errorf("notices %v: a binary that was read is never told of as unreported", c.notes)
	}
}

// TestAnOlderClaudeGetsOnlyTheHooksItKnows: a Claude Code whose binary lacks PermissionRequest gets
// the SessionStart hook alone, never an event it would stop at a warning for, the prompt hooks an
// earlier start wrote for a newer one taken out, and registers without prompts: crew.list's
// prompts_reported is false, so a window types to it without pressing Enter. The binary is asked
// again at each spawn: an update between two is seen.
func TestAnOlderClaudeGetsOnlyTheHooksItKnows(t *testing.T) {
	bin := t.TempDir()
	path := fakeClaudeBin(t, bin, append(claudeHookEvents, "crossSessionInbound")...)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	a, c, w := crewAgents(t, `[{"name":"Claude","command":"claude","codename":"cl"}]`)
	p, _ := a.profile("cl")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := c.panel.configs[instance.tab].Dir
	if _, b := claudeSettingsOf(t, dir); !strings.Contains(b, "PermissionRequest") || !promptsReported(t, w, "cl") {
		t.Fatalf("the newer Claude Code's start: no prompt hooks, or its prompts unreported:\n%s", b)
	}
	c.panel.exit(instance.tab, 0)
	fakeClaudeBin(t, bin, "SessionStart", "Notification", "PostToolUse", "UserPromptSubmit", "Stop", "crossSessionInbound")
	at := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := a.startInstance(p); err != nil {
		t.Fatal(err)
	}
	s, b := claudeSettingsOf(t, dir)
	if named := crewHookEvents(s); !slices.Equal(named, []string{"SessionStart"}) || strings.Contains(b, "--claude-hook") || s["crossSessionInbound"] != "accept" {
		t.Errorf("the older Claude Code's crew hooks are %v, want SessionStart alone, with crossSessionInbound:\n%s", named, b)
	}
	if hooks, _ := s["hooks"].(map[string]any); hooks["PermissionRequest"] != nil {
		t.Errorf("the older Claude Code's settings still name PermissionRequest:\n%s", b)
	}
	if promptsReported(t, w, "cl") {
		t.Error("a session whose Claude Code has no prompt hooks reports its prompts")
	}
	if n := unreportedNotices(c.notes); n != 0 {
		t.Errorf("notices %v: a binary that was read is never told of as unreported", c.notes)
	}
}

// TestAWrapperClaudeGetsTheRealOnesHooks: a wrapper script first on the agent's PATH (`exec mise x
// claude -- claude`, say) is taken to run the Claude Code later on PATH, whose hooks it gets, with
// prompts, and no notice.
func TestAWrapperClaudeGetsTheRealOnesHooks(t *testing.T) {
	wrappers, bin := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(wrappers, "claude"), []byte("#!/bin/bash\nexec mise x claude -- claude \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeClaudeBin(t, bin, append(claudeHookEvents, "crossSessionInbound")...)
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", wrappers+sep+bin+sep+os.Getenv("PATH"))
	a, c, w := crewAgents(t, `[{"name":"Claude","command":"claude","codename":"cl"}]`)
	p, _ := a.profile("cl")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, b := claudeSettingsOf(t, c.panel.configs[instance.tab].Dir); !strings.Contains(b, "PermissionRequest") || !promptsReported(t, w, "cl") {
		t.Errorf("a wrapper's agent: no prompt hooks, or its prompts unreported:\n%s", b)
	}
	if n := unreportedNotices(c.notes); n != 0 {
		t.Errorf("notices %v: the real binary was read, so nothing is unreported", c.notes)
	}
}

// TestAClaudeThatCantBeReadGetsTheFloor: a profile whose claude can't be read as a Claude Code (a
// wrapper script) gets what a wake needs, SessionStart and crossSessionInbound, no prompt hooks,
// registers without prompts, and is told of once, however often it starts.
func TestAClaudeThatCantBeReadGetsTheFloor(t *testing.T) {
	wrapper := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec somewhere-else \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, c, w := crewAgents(t, `[{"name":"Claude","command":"`+wrapper+`","codename":"cl"}]`)
	p, _ := a.profile("cl")
	for range 2 {
		instance, err := a.startInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		s, b := claudeSettingsOf(t, c.panel.configs[instance.tab].Dir)
		if named := crewHookEvents(s); !slices.Equal(named, []string{"SessionStart"}) || s["crossSessionInbound"] != "accept" {
			t.Errorf("an unreadable Claude Code's crew hooks are %v:\n%s\nwant SessionStart and crossSessionInbound alone", named, b)
		}
		if promptsReported(t, w, "cl") {
			t.Error("an unreadable Claude Code's session reports its prompts")
		}
		c.panel.exit(instance.tab, 0)
	}
	if n := unreportedNotices(c.notes); n != 1 {
		t.Errorf("notices %v: %d say its prompts aren't reported, want one, however often it starts", c.notes, n)
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
	c.panel = &modelPanel{owner: c}                             // takes a bracketed paste at once
	a.promptsReported = func(string, bool) bool { return true } // delivered is sent: the base presses Enter
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

func calledTimes(w *crewWindow, verb string) int {
	n := 0
	for _, v := range w.calls {
		if v == verb {
			n++
		}
	}
	return n
}

// TestALostRegistrationReplyIsReplayed: a registration the daemon committed whose answer was lost
// starts nothing, and the codename's next start replays it as it was: the session the daemon
// committed comes back, its credential the one the daemon holds, never a fresh registration that
// the first would leave busy.
func TestALostRegistrationReplyIsReplayed(t *testing.T) {
	a, c, w := crewAgents(t, oneTwo)
	w.lost = map[string]int{"crew.register": 1}
	p, _ := a.profile("one")
	first, err := a.startInstance(p)
	if err != nil || first.running || first.starting {
		t.Fatalf("a lost answer: %v, running %v, starting %v", err, first.running, first.starting)
	}
	if len(a.crew.pending) != 1 {
		t.Fatalf("the lost registration is not kept: %v", a.crew.pending)
	}
	again, err := a.startInstance(p)
	if err != nil || !again.running || again.session.ID == "" {
		t.Fatalf("its next start: %v, running %v, notices %v", err, again.running, c.notes)
	}
	ctx, _, ok := readContext(t, c.panel.configs[again.tab].Dir)
	if !ok || ctx.Session != again.session.ID {
		t.Fatalf("the context: present %v, %+v; want session %s", ok, ctx, again.session.ID)
	}
	if id, err := w.d.m.Whoami(context.Background(), ctx.Credential, ""); err != nil || id.Session != again.session.ID {
		t.Fatalf("the replayed credential: %+v, %v", id, err)
	}
	if len(a.crew.pending) != 0 {
		t.Fatalf("still pending after its replay: %v", a.crew.pending)
	}
}

// TestALostRegistrationNobodyStartsIsEnded: a lost registration whose codename is not started again
// is replayed and ended once the daemon answers again (the next renewal), so its codename is free.
func TestALostRegistrationNobodyStartsIsEnded(t *testing.T) {
	a, _, w := crewAgents(t, oneTwo)
	w.lost = map[string]int{"crew.register": 1}
	p, _ := a.profile("one")
	if first, err := a.startInstance(p); err != nil || first.running {
		t.Fatalf("a lost answer: %v", err)
	}
	w.fire() // the renewal: the daemon answers
	if len(a.crew.pending) != 0 || len(a.crew.owed) != 0 || calledTimes(w, "crew.register") != 2 || calledTimes(w, "crew.exit") != 1 {
		t.Fatalf("after the renewal: pending %v, owed %v, calls %v; want it replayed and ended", a.crew.pending, a.crew.owed, w.calls)
	}
	again, err := a.startInstance(p)
	if err != nil || !again.running {
		t.Fatalf("a start after it was ended: %v, running %v (its codename still busy?)", err, again.running)
	}
}

// TestALateRegistrationStartsNothing: a registration answered once the window no longer holds the
// scope and epoch it was made under, or is connected to another daemon or store, writes no context
// and starts nothing; its session is ended.
func TestALateRegistrationStartsNothing(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, a *agents, w *crewWindow){
		"the scope taken over": func(t *testing.T, a *agents, w *crewWindow) {
			if _, err := w.d.m.Takeover(context.Background(), a.crew.scope, "replacement-owner", ""); err != nil {
				t.Fatal(err)
			}
			a.crew.scope, a.crew.epoch = "autodoc:0000000000000001", 1 // the window claimed another
		},
		"a new epoch":    func(_ *testing.T, a *agents, _ *crewWindow) { a.crew.epoch++ },
		"another store":  func(_ *testing.T, _ *agents, w *crewWindow) { w.store = "store-2" },
		"another daemon": func(_ *testing.T, _ *agents, w *crewWindow) { w.socket = "/run/test/other.sock" },
	} {
		t.Run(name, func(t *testing.T) {
			a, c, w := crewAgents(t, oneTwo)
			w.hold, w.late = true, true // the daemon commits the registration; its answer comes after the change
			p, _ := a.profile("one")
			instance, err := a.startInstance(p)
			if err != nil || !instance.starting {
				t.Fatalf("start: %v", err)
			}
			change(t, a, w)
			w.release()
			if instance.running || c.panel.tabs[0].Running || instance.session.ID != "" {
				t.Fatalf("a late registration started the agent: running %v", instance.running)
			}
			if _, _, ok := readContext(t, instance.dir); ok {
				t.Fatal("a late registration wrote its context")
			}
			if len(a.crew.sessions) != 0 || calledTimes(w, "crew.exit") != 1 {
				t.Fatalf("its session: live %v, exits sent %d; want it ended", a.crew.sessions, calledTimes(w, "crew.exit"))
			}
		})
	}
}

// TestAnEndedSessionLeavesAReplacementsContext: ending a session removes its own context file, by
// its session's name: a newer session's, published in the same agent folder by another host, stays.
func TestAnEndedSessionLeavesAReplacementsContext(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil || !instance.running {
		t.Fatalf("start: %v", err)
	}
	dir := c.panel.configs[instance.tab].Dir
	// another host, the scope's next owner, publishes its session's context into the same agent
	// folder while this one's agent still runs
	if _, err := crew.WriteContext(dir, crew.Context{Session: "newer", Scope: instance.session.Scope}); err != nil {
		t.Fatal(err)
	}
	a.crew.exit(instance.session, crew.EndedExit)
	if ctx, _, ok := readContext(t, dir); !ok || ctx.Session != "newer" {
		t.Fatalf("the replacement's context: present %v, %+v", ok, ctx)
	}
}

// TestALostExitIsAskedAgain: an exit whose call fails before the daemon answers is owed, its context
// gone at once; the codename stays busy at the daemon until the exit is asked again on its next
// answer, and is free after. An exit the daemon refuses as stale is owed no longer.
func TestALostExitIsAskedAgain(t *testing.T) {
	a, c, w := crewAgents(t, oneTwo)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil || !instance.running {
		t.Fatalf("start: %v", err)
	}
	dir := c.panel.configs[instance.tab].Dir
	w.down = map[string]int{"crew.exit": 1}
	c.panel.exit(instance.tab, 0)
	if len(a.crew.owed) != 1 {
		t.Fatalf("a lost exit is not owed: %v", a.crew.owed)
	}
	if _, _, ok := readContext(t, dir); ok {
		t.Fatal("the context outlived the exit")
	}
	if busy, _ := a.startInstance(p); busy.running {
		t.Fatal("precondition: the daemon never heard the exit, so the codename is busy")
	}
	w.fire()
	if len(a.crew.owed) != 0 {
		t.Fatalf("the exit is still owed after the daemon answered: %v", a.crew.owed)
	}
	again, err := a.startInstance(p)
	if err != nil || !again.running {
		t.Fatalf("a start after the exit was asked again: %v, notices %v", err, c.notes)
	}
}

// TestAStartedAgentKeepsItsSessionWhenItsTabCantBeShown: a process that started is running, under
// its session and with its context, whatever selecting its tab answers: that failure is said, and
// the session ends with the process.
func TestAStartedAgentKeepsItsSessionWhenItsTabCantBeShown(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
	c.panel = &modelPanel{owner: c, selectErr: errors.New("the panel is not mounted")}
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := c.panel.configs[instance.tab].Dir
	if !instance.running || instance.session.ID == "" || len(a.crew.sessions) != 1 {
		t.Fatalf("a started agent whose tab can't be shown: running %v, session %q, live %v", instance.running, instance.session.ID, a.crew.sessions)
	}
	if ctx, _, ok := readContext(t, dir); !ok || ctx.Session != instance.session.ID {
		t.Fatalf("its context: present %v, %+v", ok, ctx)
	}
	if len(c.notes) == 0 || !strings.Contains(c.notes[len(c.notes)-1], "could not be shown") {
		t.Fatalf("notices %v, want the tab's failure said", c.notes)
	}
	c.panel.exit(instance.tab, 0)
	if len(a.crew.sessions) != 0 {
		t.Fatal("its session outlived the process")
	}
}

// TestAgentKindReadsTheShellsWords: an agent's kind is the program its command runs, its words read
// as the shell reads them, past assignments and the wrappers named; an unknown wrapper is its kind.
func TestAgentKindReadsTheShellsWords(t *testing.T) {
	for command, want := range map[string]string{
		"claude --model haiku":                   "claude",
		"NOTE='two words' claude --model haiku":  "claude",
		`A=1 B="x y" /usr/local/bin/codex`:       "codex",
		`'/opt/my tools/claude' --resume`:        "claude",
		`"cla"ude`:                               "claude",
		`claude\ code`:                           "claude code",
		"env -i HOME=/tmp claude":                "claude",
		"env -u FOO -C /tmp NOTE='a b' codex":    "codex",
		"/usr/bin/env -- codex":                  "codex",
		"env -S 'X=1 claude --model haiku'":      "claude",
		"exec -a name claude":                    "claude",
		"nohup codex":                            "codex",
		"command -- claude":                      "claude",
		"mise exec node@22 -- codex --full-auto": "codex",
		"mise x -- claude":                       "claude",
		"mise exec node@22":                      "mise",
		"'NOTE=x' claude":                        "NOTE=x", // a quoted NAME is no assignment: the shell runs it
		"firejail claude":                        "firejail",
		"":                                       "agent",
		"A=1":                                    "agent",
	} {
		if got := agentKind(command); got != want {
			t.Errorf("agentKind(%q) = %q, want %q", command, got, want)
		}
	}
}

// TestACrewTabRestartedFromItselfIsRefused: a crew agent's tab started again from the tab itself
// (Enter after its program exited) runs nothing, since no session is registered for that start;
// the agents panel's start registers one, and its program is told that session's file.
func TestACrewTabRestartedFromItselfIsRefused(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil || !instance.running {
		t.Fatalf("start: %v", err)
	}
	c.panel.exit(instance.tab, 0)
	if _, spawned, err := startedEnv(a, c, instance); !errors.Is(err, errCrewStartHere) || spawned {
		t.Fatalf("a restart from the tab: %v, spawned %v; want refused, nothing run", err, spawned)
	}
	again, err := a.startInstance(p)
	if err != nil || !again.running {
		t.Fatalf("a start from the panel: %v", err)
	}
	if args, spawned, err := startedEnv(a, c, again); err != nil || !spawned ||
		!slices.Contains(args, "AUTODOC_CREW_CONTEXT="+crew.ContextPath(again.dir, again.session.ID)) {
		t.Fatalf("its program after the panel's start: %v, spawned %v, %v", err, spawned, args)
	}
}

// TestAnAgentHereIsRaisedWhateverItsTab: a crew toast's click finds this window's agent by its
// session even when its tab can't be selected (that failure is kept), so it is never said to be in
// another window; a session no agent here has is not found.
func TestAnAgentHereIsRaisedWhateverItsTab(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil || !instance.running {
		t.Fatalf("start: %v", err)
	}
	c.panel.selectErr = errors.New("the panel is not mounted")
	if !a.raiseSession(instance.session.ID) {
		t.Fatal("this window's agent, its tab unselectable, was taken for another window's")
	}
	if a.raiseSession("elsewhere") {
		t.Fatal("a session no agent here has was found")
	}
}

// TestTheGuidesAllowanceIsMergedIntoTheFolderAlone (docs/decisions/crew.md#wake-acceptance-not-tool-approvals):
// a crew Claude agent's folder settings get the guide's commands allowed; a rule the user wrote
// there is kept, and an allow rule AutoDoc wrote before (a command no longer the guide's) taken
// out; nothing is written anywhere but the agent's folder.
func TestTheGuidesAllowanceIsMergedIntoTheFolderAlone(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	user := `{"permissions":{"allow":["Bash(git status:*)","Bash(autodoc mail peek:*)"],"deny":["Bash(rm:*)"]},"model":"haiku"}`
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeWakeAcceptance(dir, crew.KindClaude, crew.ClaudeFloor()); err != nil {
		t.Fatal(err)
	}
	s, b := claudeSettingsOf(t, dir)
	perm, _ := s["permissions"].(map[string]any)
	var allow []string
	rules, _ := perm["allow"].([]any)
	for _, r := range rules {
		allow = append(allow, r.(string))
	}
	want := append([]string{"Bash(git status:*)"}, crew.ClaudeAllow()...)
	if !slices.Equal(allow, want) || fmt.Sprint(perm["deny"]) != "[Bash(rm:*)]" || s["model"] != "haiku" {
		t.Errorf("the merged settings:\n%s\nwant the user's rules kept, AutoDoc's old one gone, the guide's commands allowed", b)
	}
	if es, _ := os.ReadDir(home); len(es) != 0 {
		t.Errorf("the allowance was written into the user's home: %v", es)
	}
}
