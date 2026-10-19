package tui

import (
	"os/exec"
	"slices"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// SETTING UP AN AGENT — the first run's card (ADR 1792395801): when the setup was never answered
// and no profile exists, AutoDoc offers the agent CLIs it finds on PATH, the first one chosen.
// Set up saves an ordinary profile through the profiles dialog's own save (storeAgentProfile),
// starts it, and submits one first prompt; No is remembered. System › Set up an agent… opens the
// card again at any time, without the first prompt.

// agentCLI is one agent CLI AutoDoc knows to offer: the command a profile runs, and the name the
// card fills in.
type agentCLI struct{ command, name string }

// agentCLIs are the CLIs the setup offers, in the order Johno set: the first one found is the default.
var agentCLIs = []agentCLI{{"claude", "Claude"}, {"codex", "Codex"}, {"grok", "Grok"}, {"agy", "Antigravity"}}

// detectAgentCLIs is those of agentCLIs that look finds on PATH, in order. Nothing is run.
// look is exec.LookPath; tests pass their own.
func detectAgentCLIs(look func(string) (string, error)) []agentCLI {
	var out []agentCLI
	for _, c := range agentCLIs {
		if _, err := look(c.command); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// prefAgentSetup records the setup's answer: "" never answered, "done" set up, "declined" No.
// Only the first-run card reads it; System › Set up an agent… opens the card whatever it says.
const prefAgentSetup = "tui.agent.setup"

// offerAgentSetup is whether a first run offers the setup at all; a test of something else turns
// it off, as its first card would otherwise be this one.
var offerAgentSetup = true

// agentSetupPending is the first-run card's condition, read in one place: the setup never answered,
// and no profile. A user with a profile is never asked.
func agentSetupPending(p prefs) bool {
	return offerAgentSetup && p.Value(prefAgentSetup) == "" && len(agentProfilesOf(p.Value(prefAgentProfiles))) == 0
}

// lookAgentCLI is how the card finds the CLIs; a test sets its own.
var lookAgentCLI = exec.LookPath

// Started offers the setup on a first run (startup's About waits for it, startup.go).
func (a *agents) Started() {
	if agentSetupPending(a.c.Prefs()) {
		a.openAgentSetup(true)
	}
}

// openAgentSetup fills and opens the card. first marks the first-run card: only its Set up
// submits the first prompt.
func (a *agents) openAgentSetup(first bool) {
	found := detectAgentCLIs(lookAgentCLI)
	a.setupFirst, a.setupFound = first, found
	rows := make([]rowOf, len(found))
	for i, c := range found {
		rows[i] = rowOf{"key": c.command, "label": c.name + " (" + c.command + ")"}
	}
	a.setupList().Reset(rows)
	a.c.Set("App.agentSetupIndex", -1) // moved away first, so the same row twice still moves it
	a.c.Set("App.agentSetupIndex", 0)
	name := ""
	if len(found) > 0 {
		name = found[0].name
	}
	a.c.Set("App.agentSetupName", name)
	a.c.Set("App.agentSetupNone", len(found) == 0)
	a.c.Set("App.agentSetupSome", len(found) > 0)
	reject := "&No"
	if len(found) == 0 {
		reject = "&Close"
	}
	a.c.Set("App.agentSetupReject", reject)
	a.c.Open("agentSetup")
}

// setupList is the card's dropdown of the CLIs found: one model, its rows reset as the card opens.
func (a *agents) setupList() *tuidecl.ListModel {
	if a.setupCLIs == nil {
		a.setupCLIs = tuidecl.NewListModel("key", "label")
	}
	return a.setupCLIs
}

// agentSetupPicked is the dropdown at row i, name the field's text: the name follows the CLI while
// it is still a CLI's own name, or empty, and is left alone once the user typed their own.
func (a *agents) agentSetupPicked(i int, name string) {
	if i < 0 || i >= len(a.setupFound) {
		return
	}
	cur := strings.TrimSpace(name)
	if cur == "" || slices.ContainsFunc(a.setupFound, func(c agentCLI) bool { return c.name == cur }) {
		a.c.Set("App.agentSetupName", a.setupFound[i].name)
	}
}

// agentSetupConfirm is Set up: an ordinary profile, through the profiles dialog's own save.
func (a *agents) agentSetupConfirm(i int, name string) {
	if i < 0 || i >= len(a.setupFound) {
		return
	}
	cli := a.setupFound[i]
	a.editingAgent, a.editingAgentProfile = "", nil // a new profile, never an edit
	p, ok := a.storeAgentProfile(name, cli.command, "")
	if !ok {
		return // refused: storeAgentProfile opened agentEdit with the reason, and nothing is recorded
	}
	a.c.SetPref(prefAgentSetup, "done")
	a.c.Close("agentSetup")
	a.useProfile(p)
	if a.setupFirst {
		a.firstPrompt(p)
	}
}

// agentSetupDecline is the card's No (or Escape): asked once, never at start again. The none-found
// card's Close records nothing, so the next start asks again.
func (a *agents) agentSetupDecline() {
	if len(a.setupFound) > 0 {
		a.c.SetPref(prefAgentSetup, "declined")
	}
	a.c.Close("agentSetup")
}

// SendPrompt types one line into the agent and submits it: deliver's own path, as HandTask's, with
// the line as written. A line with CR or LF is refused, so no CLI submits part of it early.
func (a *agents) SendPrompt(to AgentRef, line string, done func(error)) error {
	check := func() error {
		if line == "" || strings.ContainsAny(line, "\r\n") || len(line) > widget.TerminalInputLimit {
			return ErrAgentText
		}
		return nil
	}
	return a.deliver(to, check, "this prompt", true, func(panel TerminalPanel, tab widget.TerminalTabID) error {
		return panel.SendPaste(tab, line)
	}, done)
}

// firstAgentPrompt is the one line the first-run setup submits.
const firstAgentPrompt = "Hello! In a few lines, what can you do for me in AutoDoc? " +
	"Answer from the \"What you can do for the user\" section of your AGENTS.md, then ask what I'd like to start with."

// firstPrompt submits it to p once. A refusal or a failed delivery is a notification, never a retry.
// done(nil) means typed and submitted, not received: a CLI that drops the line says nothing to AutoDoc.
func (a *agents) firstPrompt(p agentProfile) {
	err := a.SendPrompt(AgentRef{Codename: p.Codename}, firstAgentPrompt, func(err error) {
		if err != nil {
			a.c.Notify("the first prompt was not sent: " + err.Error())
		}
	})
	if err != nil {
		a.c.Notify("the first prompt was not sent: " + err.Error())
	}
}
