package tui

import (
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// SETTING UP AN AGENT — the first run's card (docs/decisions/crew.md#first-run-agent-setup): when
// the setup was never answered and no profile exists, AutoDoc offers the agent CLIs it finds on
// PATH, the first one chosen. Set up saves an ordinary profile through the profiles dialog's own
// save (storeAgentProfile) and starts it, typing nothing into it; No is remembered. System › Set up
// an agent… opens the card again at any time.

// agentCLI is one agent CLI AutoDoc knows to offer: the command a profile runs, and the name the
// card fills in.
type agentCLI struct{ command, name string }

// agentCLIs are the CLIs the setup offers, in a fixed order: the first one found is the default.
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

// agentSetupPending is the first-run card's condition, read in one place: the setup never answered,
// and no profile. A user with a profile is never asked.
func agentSetupPending(s *seams, p prefs) bool {
	return s.offerAgentSetup && p.Value(prefAgentSetup) == "" && len(agentProfilesOf(p.Value(prefAgentProfiles))) == 0
}

// Started warms what each Claude profile's Claude Code knows (warmClaude), and offers the setup on a first run (startup's About waits for it, startup.go), once the
// Vim question before it is answered (vimquestion.go opens it then).
func (a *agents) Started() {
	for _, p := range a.profiles() {
		if a.crew != nil && p.kind() == crew.KindClaude {
			a.warmClaude(p) // and a Claude agent's on what its Claude Code knows
		}
	}
	if p := a.c.Prefs(); agentSetupPending(a.seams, p) && !vimQuestionPending(a.seams, p) {
		a.openAgentSetup()
	}
}

// openAgentSetup fills and opens the card.
func (a *agents) openAgentSetup() {
	look := a.seams.lookAgentCLI
	if look == nil {
		env := a.seams.env
		look = func(name string) (string, error) { return procenv.LookPath(env, name) }
	}
	found := detectAgentCLIs(look)
	a.setupFound = found
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
	reject := a.c.Tr("autodoc.text.agentsetup.no")
	if len(found) == 0 {
		reject = a.c.Tr("autodoc.text.agentsetup.close")
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
	p, ok := a.storeAgentProfile(name, cli.command, "", "", "")
	if !ok {
		return // refused: storeAgentProfile opened agentEdit with the reason, and nothing is recorded
	}
	a.c.SetPref(prefAgentSetup, "done")
	a.c.Close("agentSetup")
	a.useProfile(p)
}

// agentSetupDecline is the card's No (or Escape): asked once, never at start again. The none-found
// card's Close records nothing, so the next start asks again.
func (a *agents) agentSetupDecline() {
	if len(a.setupFound) > 0 {
		a.c.SetPref(prefAgentSetup, "declined")
	}
	a.c.Close("agentSetup")
}
