package tui

import (
	"strconv"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// THE CREW BELL (docs/decisions/crew.md#the-human-inbox): off unless the user turns it on
// (:agent-bell, the tui.agents.bell preference). Then a decision request or a permission prompt
// newly open rings, at most once every crewBellEvery, so a burst is one ring. The terminal build
// rings the terminal's own bell (BEL, through golib's App.Bell), which the terminal turns into the
// alert its user chose; the window plays the desktop's message sound: canberra-gtk-play, else
// pw-play, else paplay.

const prefAgentBell = "tui.agents.bell"

// crewBellEvery is the least time between two rings.
const crewBellEvery = 10 * time.Second

func (x *crewInbox) Prefs() []feature.PrefSpec {
	return agentBellRows()
}

func (x *crewInbox) Catalog() []feature.Entry {
	e := feature.Builtin("agent.bell", "ring a bell when an agent asks you or waits on you", "App.toggleAgentBell()", x.toggleBell)
	e.Verb = "agent-bell"
	return []feature.Entry{e}
}

// toggleBell turns the bell on or off, and says which.
func (x *crewInbox) toggleBell() {
	on := x.c.Prefs().Value(prefAgentBell) != "true"
	x.c.SetPref(prefAgentBell, strconv.FormatBool(on))
	if on {
		x.c.Notify(x.c.Tr("autodoc.crew.bell_on"))
		return
	}
	x.c.Notify(x.c.Tr("autodoc.crew.bell_off"))
}

// ring rings the bell, while it is on and the last ring is crewBellEvery past.
func (x *crewInbox) ring() {
	if x.c.Prefs().Value(prefAgentBell) != "true" {
		return
	}
	now := x.now()
	if !x.rang.IsZero() && now.Sub(x.rang) < crewBellEvery {
		return
	}
	x.rang = now
	x.c.Bell()
}

// agentBellRows are the bell's preference, bound to the host's field (hostRows reads it by name).
func agentBellRows() []feature.PrefSpec {
	return []feature.PrefSpec{feature.ValuePref(prefAgentBell, "false", []string{"true", "false"})}
}
