package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc"
)

// offeringSetup turns the first-run card on for one test (TestMain turns it off for the rest).
func offeringSetup(t *testing.T) {
	t.Helper()
	offerAgentSetup = true
	t.Cleanup(func() { offerAgentSetup = false })
}

// fakeClaude is a claude that writes every line typed to it into heard, a line only once its Enter
// arrives: a prompt submitted is a whole line there, a prompt pasted without Enter none. The card
// finds it (lookAgentCLI); onPath puts it on PATH for the agent's start, after runAgentTUIWith,
// which sets a PATH of its own.
func fakeClaude(t *testing.T) (heard string, onPath func()) {
	t.Helper()
	bin := t.TempDir()
	heard = filepath.Join(t.TempDir(), "heard")
	script := "#!/bin/sh\necho ready\nwhile IFS= read -r line; do printf '%s\\n' \"$line\" >> " + shellQuote(heard) + "; done\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	look := lookAgentCLI
	lookAgentCLI = func(c string) (string, error) {
		if c == "claude" {
			return filepath.Join(bin, c), nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { lookAgentCLI = look })
	return heard, func() { t.Setenv("PATH", bin+":/usr/bin:/bin") }
}

func heardLines(path string) []string {
	b, _ := os.ReadFile(path)
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// The CLIs are offered in their fixed order, absent ones left out; none found is empty. Nothing runs.
func TestDetectAgentCLIsKeepsTheOrder(t *testing.T) {
	on := func(names ...string) func(string) (string, error) {
		return func(c string) (string, error) {
			if slices.Contains(names, c) {
				return "/bin/" + c, nil
			}
			return "", errors.New("not found")
		}
	}
	name := func(cs []agentCLI) (out []string) {
		for _, c := range cs {
			out = append(out, c.command)
		}
		return out
	}
	if got := name(detectAgentCLIs(on("agy", "codex", "claude"))); !slices.Equal(got, []string{"claude", "codex", "agy"}) {
		t.Errorf("found %v; want claude, codex, agy in that order", got)
	}
	if got := detectAgentCLIs(on()); len(got) != 0 {
		t.Errorf("none found: %v", got)
	}
}

// A first run (never answered, no profile) opens the setup card in About's place; a profile, or an
// answer, opens About as before and never the card.
func TestTheSetupCardIsAFirstRunsOnly(t *testing.T) {
	offeringSetup(t)
	fakeClaude(t)
	_, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", map[string]string{prefAboutAt: "true"}, Options{})
	r.s.WaitForText(t, "Set up an AI agent?")
	if strings.Contains(r.s.String(), "about autodoc") {
		t.Fatal("About opened beside the setup card")
	}

	for name, prefs := range map[string]map[string]string{
		"a profile": {prefAboutAt: "true"},
		"an answer": {prefAboutAt: "true", prefAgentSetup: "declined"},
		"set up":    {prefAboutAt: "true", prefAgentSetup: "done"},
	} {
		var profiles []agentProfile
		if name == "a profile" {
			profiles = []agentProfile{{Name: "Mine", Command: "true", Codename: "mine"}}
		}
		_, r, _ := runAgentTUIWith(t, t.TempDir(), profiles, "", prefs, Options{})
		r.s.WaitForText(t, "about autodoc")
		if strings.Contains(r.s.String(), "Set up an AI agent?") {
			t.Errorf("%s: the setup card opened", name)
		}
	}
}

// Set up saves Claude as an ordinary profile, the default, records the answer and starts it, typing
// nothing into it; the menu's Set up later does the same.
func TestSetUpSavesAndStartsTypingNothing(t *testing.T) {
	offeringSetup(t)
	heard, onPath := fakeClaude(t)
	d, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", nil, Options{})
	onPath()
	r.s.WaitForText(t, "Set up an AI agent?")
	r.s.WaitForText(t, "Claude (claude)")
	r.keys(t, key('s')) // &Set up
	storedPref(t, d, r, prefAgentSetup, "done")
	r.s.WaitFor(t, "the agent started", func(string) bool { return r.agentRunningNow() })
	time.Sleep(300 * time.Millisecond) // a line would be typed within a moment of the start
	if got := heardLines(heard); len(got) != 0 {
		t.Fatalf("the setup typed into the agent: %q", got)
	}
	p := onLoop(r, func() []agentProfile { return r.h.agents.profiles() })
	if len(p) != 1 || p[0].Name != "Claude" || p[0].Command != "claude" || p[0].Codename == "" ||
		onLoop(r, func() string { return r.h.agents.defaultProfile() }) != p[0].Codename {
		t.Fatalf("the profile %+v is not Claude's, the default", p)
	}

	// the menu's card, set up again under another name: a second profile, and no prompt
	onLoop(r, func() bool { r.h.agents.openAgentSetup(); return true })
	r.s.WaitForText(t, "Set up an AI agent?")
	onLoop(r, func() bool { r.h.agents.agentSetupConfirm(0, "Claude two"); return true })
	r.s.WaitFor(t, "the second profile", func(string) bool {
		return onLoop(r, func() bool { return len(r.h.agents.profiles()) == 2 })
	})
	time.Sleep(300 * time.Millisecond)
	if got := heardLines(heard); len(got) != 0 {
		t.Fatalf("the menu's Set up typed into the agent: %q", got)
	}
}

// A taken codename is refused by storeAgentProfile with its reason: nothing is recorded, the old
// profile with that codename is not started, and nothing is typed.
func TestASetupWithATakenCodenameIsRefused(t *testing.T) {
	offeringSetup(t)
	heard, onPath := fakeClaude(t)
	old := agentProfile{Name: "Old", Command: "claude", Codename: "claude"}
	d, r, _ := runAgentTUIWith(t, t.TempDir(), []agentProfile{old}, "", nil, Options{})
	onPath()
	onLoop(r, func() bool { r.h.agents.openAgentSetup(); return true })
	r.s.WaitForText(t, "Set up an AI agent?")
	onLoop(r, func() bool { r.h.agents.agentSetupConfirm(0, "Claude"); return true })
	r.s.WaitFor(t, "the form's refusal", func(string) bool { // its state: a panel may cover the form
		return onLoop(r, func() bool {
			v, _ := r.h.p.Tree().Source("App.agentFormError")
			return strings.Contains(v.Raw, "not saved: codename taken by Old")
		})
	})
	time.Sleep(300 * time.Millisecond)
	if r.agentRunningNow() || len(heardLines(heard)) != 0 {
		t.Fatalf("a refused setup started the old profile (%v) or prompted (%q)", r.agentRunningNow(), heardLines(heard))
	}
	if m, _ := d.db.Preferences(t.Context()); m[prefAgentSetup] != "" {
		t.Fatalf("a refused setup recorded %q", m[prefAgentSetup])
	}
}

// The form's Save and storeAgentProfile refuse alike: the form is unchanged.
func TestTheFormAndTheSetupRefuseAlike(t *testing.T) {
	_, r, _ := runAgentTUI(t, t.TempDir(), nil, "")
	onLoop(r, func() bool { r.h.agents.saveAgentProfile("", "claude", ""); return true })
	r.s.WaitForText(t, "not saved: a profile is a name and the command that starts the agent")
	if onLoop(r, func() bool { _, ok := r.h.agents.storeAgentProfile("x", "", ""); return ok }) {
		t.Fatal("storeAgentProfile saved a profile with no command")
	}
}

// No records the answer: the next start opens neither the card nor a prompt.
func TestNoIsRemembered(t *testing.T) {
	offeringSetup(t)
	fakeClaude(t)
	d, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", nil, Options{})
	r.s.WaitForText(t, "Set up an AI agent?")
	r.keys(t, key('n')) // &No
	storedPref(t, d, r, prefAgentSetup, "declined")
	r2 := runTUI(t, NewSession(d.sock, nil), Options{AgentDir: t.TempDir(), ConfigPath: agentConfig})
	r2.s.WaitFor(t, "started", func(string) bool { return onLoop(r2, func() bool { return r2.h.started }) })
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(r2.s.String(), "Set up an AI agent?") {
		t.Fatal("the setup was offered again after No")
	}
}

// The card opens later from the catalog (agent.setup) and from System › Set up an agent…
// (App.openAgentSetup).
func TestTheSetupOpensFromTheCatalogAndTheMenu(t *testing.T) {
	fakeClaude(t)
	_, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", nil, Options{})
	r.s.WaitFor(t, "started", func(string) bool { return onLoop(r, func() bool { return r.h.started }) })
	if err := onLoop(r, func() error { return r.h.cat.run("agent.setup", nil) }); err != nil {
		t.Fatalf("agent.setup: %v", err)
	}
	r.s.WaitForText(t, "Claude (claude)")
	r.keys(t, key('n')) // &No
	r.s.WaitFor(t, "the card closed", func(sc string) bool { return !strings.Contains(sc, "Set up an AI agent?") })

	if err := onLoop(r, func() error { return r.h.agents.Commands()["App.openAgentSetup"].Run(nil) }); err != nil {
		t.Fatalf("App.openAgentSetup: %v", err)
	}
	r.s.WaitForText(t, "Set up an AI agent?")
}

// With no CLI found, the card says which to install and why, and offers only Close, which records
// nothing: the next start asks again.
func TestNoneFoundSaysWhichAndRecordsNothing(t *testing.T) {
	offeringSetup(t)
	look := lookAgentCLI
	lookAgentCLI = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookAgentCLI = look })
	d, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", nil, Options{})
	r.s.WaitForText(t, "No agent CLI was found on your PATH")
	if n := onLoop(r, func() int { return len(r.h.agents.setupFound) }); n != 0 {
		t.Errorf("the none-found card lists %d CLIs to set up", n)
	}
	r.keys(t, key('c')) // &Close
	r.s.WaitFor(t, "the card closed", func(sc string) bool { return !strings.Contains(sc, "No agent CLI was found") })
	if m, _ := d.db.Preferences(t.Context()); m[prefAgentSetup] != "" {
		t.Fatalf("Close recorded %q", m[prefAgentSetup])
	}
}

// AGENTS.md says what an agent can do for the user; the generated guide no longer has its own list.
func TestTheAgentsGuideHasTheJobs(t *testing.T) {
	if !strings.Contains(autodoc.AgentsGuide, "## What you can do for the user") ||
		!strings.Contains(autodoc.AgentsGuide, "Proof-read as a professional editor") {
		t.Fatal("AGENTS.md has no \"What you can do for the user\" section")
	}
	guide := agentGuide(wsInfo{name: "kb", root: "/kb"}, "/bin/autodoc", "/autodoc.toml", "/w/bin/autodoc")
	if strings.Count(guide, "What you can do for the user") != 1 || strings.Contains(guide, "## What to help with") {
		t.Fatal("the generated guide lists the jobs twice, or keeps its old list")
	}
}
