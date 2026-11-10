package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude is a claude that writes every line typed to it into heard, a line only once its Enter
// arrives: a prompt submitted is a whole line there, a prompt pasted without Enter none. The card
// finds it (the seams' lookAgentCLI); onPath puts it on r's program's PATH for the agent's start, after
// runAgentTUIWith, which sets a PATH of its own.
func fakeClaude(t *testing.T) (heard string, onPath func(*running)) {
	t.Helper()
	bin := t.TempDir()
	heard = filepath.Join(t.TempDir(), "heard")
	script := "#!/bin/sh\necho ready\nwhile IFS= read -r line; do printf '%s\\n' \"$line\" >> " + shellQuote(heard) + "; done\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tune(t).lookAgentCLI = func(c string) (string, error) {
		if c == "claude" {
			return filepath.Join(bin, c), nil
		}
		return "", errors.New("not found")
	}
	return heard, func(r *running) {
		onLoop(r, func() bool { r.h.seams.env = r.h.seams.env.With("PATH=" + bin + ":/usr/bin:/bin"); return true })
	}
}

// Set up saves Claude as an ordinary profile, the default, records the answer and starts it, typing
// nothing into it; the menu's Set up later does the same.
func TestSetUpSavesAndStartsTypingNothing(t *testing.T) {
	t.Parallel()
	offeringSetup(t)
	heard, onPath := fakeClaude(t)
	d, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", nil, Options{})
	onPath(r)
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
	t.Parallel()
	offeringSetup(t)
	heard, onPath := fakeClaude(t)
	old := agentProfile{Name: "Old", Command: "claude", Codename: "claude"}
	d, r, _ := runAgentTUIWith(t, t.TempDir(), []agentProfile{old}, "", nil, Options{})
	onPath(r)
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
	t.Parallel()
	_, r, _ := runAgentTUI(t, t.TempDir(), nil, "")
	onLoop(r, func() bool { r.h.agents.saveAgentProfile("", "claude", ""); return true })
	r.s.WaitForText(t, "not saved: a profile is a name and the command that starts the agent")
	if onLoop(r, func() bool { _, ok := r.h.agents.storeAgentProfile("x", "", "", "", ""); return ok }) {
		t.Fatal("storeAgentProfile saved a profile with no command")
	}
}

// No records the answer: the next start opens neither the card nor a prompt.
func TestNoIsRemembered(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	fakeClaude(t)
	_, r, _ := runAgentTUIWith(t, t.TempDir(), nil, "", nil, Options{})
	r.s.WaitFor(t, "started", func(string) bool { return onLoop(r, func() bool { return r.h.started }) })
	if err := onLoop(r, func() error { return r.h.cat.Run("agent.setup", nil) }); err != nil {
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
	t.Parallel()
	offeringSetup(t)
	tune(t).lookAgentCLI = func(string) (string, error) { return "", errors.New("not found") }
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
