package tui

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestTheFormsEnvRoundTrips (docs/decisions/crew.md#a-profiles-environment): the form's env field
// shows a profile's env as NAME=value words, quoted where a value needs it, and reads back the same
// map, nothing expanded: ~ and $VAR stay as written until the spawn.
func TestTheFormsEnvRoundTrips(t *testing.T) {
	env := map[string]string{
		"CLAUDE_CONFIG_DIR": "~/.claude-work",
		"GREETING":          "hello world",
		"QUOTED":            `it's "x" \ y`,
		"HOMED":             "$HOME/y",
		"EMPTY":             "",
	}
	text := formatProfileEnv(env)
	got, err := parseProfileEnv(text)
	if err != nil || !maps.Equal(got, env) {
		t.Fatalf("%q read back as %v (%v), want %v", text, got, err, env)
	}
	got, err = parseProfileEnv(`A='x y' B="$HOME/z" C=~/w`)
	if want := map[string]string{"A": "x y", "B": "$HOME/z", "C": "~/w"}; err != nil || !maps.Equal(got, want) {
		t.Fatalf("typed words read as %v (%v), want %v", got, err, want)
	}
	if got, err := parseProfileEnv("  "); err != nil || got != nil {
		t.Fatalf("a blank field: %v (%v), want no env", got, err)
	}
	if formatProfileEnv(nil) != "" {
		t.Fatal("no env shows as something")
	}
}

// TestTheFormRefusesAnEnvAProfileCantSet: a word that isn't NAME=value, a key that isn't a
// variable's name, AutoDoc's own (AUTODOC_*, OPENCODE_CONFIG_CONTENT) and a key given twice are
// refused, naming the word; the profile isn't saved, and the form says why in its language.
func TestTheFormRefusesAnEnvAProfileCantSet(t *testing.T) {
	for text, want := range map[string]struct {
		word string
		why  error
	}{
		"FOO":                             {"FOO", errEnvKeyInvalid},
		"=x":                              {"=x", errEnvKeyInvalid},
		"1A=x":                            {"1A", errEnvKeyInvalid},
		"AUTODOC_ROOT=/x":                 {"AUTODOC_ROOT", errEnvKeyReserved},
		"OK=1 OPENCODE_CONFIG_CONTENT={}": {"OPENCODE_CONFIG_CONTENT", errEnvKeyReserved},
		"A=1 A=2":                         {"A", errEnvKeyTwice},
	} {
		_, err := parseProfileEnv(text)
		var e *envError
		if !errors.As(err, &e) || e.word != want.word || !errors.Is(err, want.why) {
			t.Errorf("%q: %v, want %s %v", text, err, want.word, want.why)
		}
	}
	a, c := modelAgents(t)
	before := a.profiles()
	a.saveAgentProfileWithEnv("Three", "three", "three", "CLAUDE_CONFIG_DIR=~/.w AUTODOC_ROOT=/x")
	if !slices.EqualFunc(a.profiles(), before, agentProfile.same) {
		t.Fatalf("a refused env was saved: %+v", a.profiles())
	}
	if why, _ := c.state["App.agentFormError"].(string); !strings.Contains(why, "AUTODOC_ROOT is AutoDoc's own variable") {
		t.Fatalf("the form says %q, want AUTODOC_ROOT named as AutoDoc's own", why)
	}
	a.saveAgentProfileWithEnv("Three", "three", "three", "CLAUDE_CONFIG_DIR=~/.w")
	if p, ok := a.profile("three"); !ok || p.Env["CLAUDE_CONFIG_DIR"] != "~/.w" {
		t.Fatalf("a good env: %+v %v, want CLAUDE_CONFIG_DIR saved as written", p, ok)
	}
}

// TestAHandWrittenProfileKeepsOnlyWhatAProfileCanSet: a profile written by hand into the preference
// loses the keys a profile can't set, and keeps the rest; a profile left with none has no env.
func TestAHandWrittenProfileKeepsOnlyWhatAProfileCanSet(t *testing.T) {
	ps := agentProfilesOf(`[{"name":"S","command":"claude","codename":"s","env":{"AUTODOC_ROOT":"/x","OPENCODE_CONFIG_CONTENT":"{}","1BAD":"v","CLAUDE_CONFIG_DIR":"~/.w"}},` +
		`{"name":"T","command":"claude","codename":"t","env":{"AUTODOC_SOCKET":"/s"}}]`)
	if len(ps) != 2 || !maps.Equal(ps[0].Env, map[string]string{"CLAUDE_CONFIG_DIR": "~/.w"}) || ps[1].Env != nil {
		t.Fatalf("hand-written profiles read as %+v", ps)
	}
}

// TestASavedProfileKeepsItsEnvThroughAnEditWithoutIt: App.saveAgentProfile, which names no env
// (an older form, or another caller), keeps the edited profile's env rather than clearing it.
func TestASavedProfileKeepsItsEnvThroughAnEditWithoutIt(t *testing.T) {
	a, _ := modelAgents(t)
	a.saveAgentProfileWithEnv("Three", "three", "three", "CLAUDE_CONFIG_DIR=~/.w")
	i := slices.IndexFunc(a.profiles(), func(p agentProfile) bool { return p.Codename == "three" })
	a.startEditAgent(i)
	a.saveAgentProfile("Three renamed", "three", "three")
	if p, _ := a.profile("three"); p.Name != "Three renamed" || p.Env["CLAUDE_CONFIG_DIR"] != "~/.w" {
		t.Fatalf("after an edit naming no env: %+v, want its env kept", p)
	}
}

// TestAChangedEnvRestartsAStoppedTabFresh: a stopped tab whose profile's env changed since is not
// started again as it was (its process would run on the old env), as for a changed command; Restart
// closes it and starts a fresh one on the new env. An unchanged env starts the old tab again.
func TestAChangedEnvRestartsAStoppedTabFresh(t *testing.T) {
	a, c := modelAgents(t)
	a.startEditAgent(0)
	a.saveAgentProfileWithEnv("One", "one", "one", "GREETING=first")
	p, _ := a.profile("one")
	first, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	c.panel.exit(first.tab, 0)
	if again, err := a.startInstance(p); err != nil || again != first {
		t.Fatalf("the same env: %v, want its stopped tab started again", err)
	}
	c.panel.exit(first.tab, 0)
	a.startEditAgent(0)
	a.saveAgentProfileWithEnv("One", "one", "one", "GREETING=second")
	edited, _ := a.profile("one")
	if _, err := a.startInstance(edited); !errors.Is(err, ErrAgentStale) {
		t.Fatalf("a start into the stopped tab after the env changed: %v, want ErrAgentStale", err)
	}
	a.restartAgent("one")
	fresh := a.instances["one"]
	if fresh == nil || fresh == first || !fresh.running || fresh.profile.Env["GREETING"] != "second" {
		t.Fatalf("Restart after the env changed: %+v, want a fresh tab on the new env", fresh)
	}
	args, _, err := startedEnv(a, c, fresh)
	if err != nil || !slices.Contains(args, "GREETING=second") || slices.Contains(args, "GREETING=first") {
		t.Fatalf("the fresh tab starts with %v (%v), want GREETING=second", args, err)
	}
}

// TestAClaudeProfileOnAnotherAccountIsAClaudeAgent (the Sephiroth profile): `claude` with
// CLAUDE_CONFIG_DIR in its env, and the Claude Code found on its env's PATH alone, is a crew Claude
// agent: kind claude, the SessionStart hook and crossSessionInbound written, its prompts reported,
// and its process given CLAUDE_CONFIG_DIR expanded, with AutoDoc's own folder first on its PATH.
func TestAClaudeProfileOnAnotherAccountIsAClaudeAgent(t *testing.T) {
	bin := t.TempDir()
	fakeClaudeBin(t, bin, append(claudeHookEvents, "crossSessionInbound")...)
	t.Setenv("PATH", "/usr/bin:/bin") // the Claude Code is on the profile's PATH, not AutoDoc's
	a, c, w := crewAgents(t, `[{"name":"Sephiroth","command":"claude","codename":"sephiroth","env":{"CLAUDE_CONFIG_DIR":"~/.claude-work","PATH":"`+bin+`:$PATH"}}]`)
	p, _ := a.profile("sephiroth")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := c.panel.configs[instance.tab].Dir
	if ctx, _, ok := readContext(t, dir); !ok || ctx.Kind != "claude" {
		t.Fatalf("its session's kind: %+v (%v), want claude", ctx, ok)
	}
	s, b := claudeSettingsOf(t, dir)
	if s["crossSessionInbound"] != "accept" || !strings.Contains(b, "crew endpoint --claude") || !strings.Contains(b, "PermissionRequest") {
		t.Fatalf("its settings, for the Claude Code on its PATH, don't accept wakes or report prompts:\n%s", b)
	}
	if !promptsReported(t, w, "sephiroth") {
		t.Error("its prompts aren't reported: its Claude Code was not found on the profile's PATH")
	}
	home, _ := os.UserHomeDir()
	args, _, err := startedEnv(a, c, instance)
	if err != nil || !slices.Contains(args, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude-work")) ||
		!slices.Contains(args, "PATH="+filepath.Join(dir, "bin")+string(os.PathListSeparator)+bin+string(os.PathListSeparator)+"/usr/bin:/bin") {
		t.Fatalf("its process starts with %v (%v), want CLAUDE_CONFIG_DIR expanded and its own bin, then the profile's PATH", args, err)
	}
	if n := unreportedNotices(c.notes); n != 0 {
		t.Errorf("notices %v: its Claude Code was read", c.notes)
	}
}

// TestTheFormSaysEachEnvRefusalInItsLanguage: each reason the form's env field is refused is said
// in the catalog's words, naming the word (envRefusal): a word that isn't NAME=value, AutoDoc's own
// key, and a key set twice; an error of another kind is said as it is.
func TestTheFormSaysEachEnvRefusalInItsLanguage(t *testing.T) {
	a, c := modelAgents(t)
	for text, want := range map[string]string{
		"FOO":             "not saved: FOO is not NAME=value",
		"AUTODOC_ROOT=/x": "not saved: AUTODOC_ROOT is AutoDoc's own variable",
		"A=1 A=2":         "not saved: A is set twice",
	} {
		before := a.profiles()
		a.saveAgentProfileWithEnv("Three", "three", "three", text)
		if why, _ := c.state["App.agentFormError"].(string); why != want {
			t.Errorf("%q: the form says %q, want %q", text, why, want)
		}
		if !slices.EqualFunc(a.profiles(), before, agentProfile.same) {
			t.Errorf("%q: a refused env was saved: %+v", text, a.profiles())
		}
	}
	if got := a.envRefusal(errors.New("a reason of another kind")); got != "a reason of another kind" {
		t.Errorf("another error: %q, want it as it is", got)
	}
}

// TestASaveNamingNoEnvOfANewProfileHasNone: App.saveAgentProfile with no profile being edited saves
// a new profile with no env; removing a row that isn't there asks nothing.
func TestASaveNamingNoEnvOfANewProfileHasNone(t *testing.T) {
	a, c := modelAgents(t)
	a.saveAgentProfile("Three", "three", "three")
	if p, ok := a.profile("three"); !ok || p.Env != nil {
		t.Fatalf("a new profile saved with no env named: %+v %v, want saved with none", p, ok)
	}
	asked := len(c.questions)
	a.removeAgent(len(a.profiles()) + 5)
	if len(c.questions) != asked {
		t.Errorf("removing a row that isn't there asked %v", c.questions[asked:])
	}
}
