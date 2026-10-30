package tui

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
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

// crewBellSound is the freedesktop message sound pw-play and paplay are given.
const crewBellSound = "/usr/share/sounds/freedesktop/stereo/message-new-instant.oga"

// crewBellWait bounds one sound's player: a stuck player must not linger.
const crewBellWait = 5 * time.Second

func (x *crewInbox) Prefs() []PrefSpec {
	return []PrefSpec{boolPref(prefAgentBell, false, func(p *prefs) *bool { return &p.agentBell })}
}

func (x *crewInbox) Catalog() []Entry {
	e := builtin("agent.bell", "ring a bell when an agent asks you or waits on you", "App.toggleAgentBell()", x.toggleBell)
	e.Verb = "agent-bell"
	return []Entry{e}
}

// toggleBell turns the bell on or off, and says which.
func (x *crewInbox) toggleBell() {
	on := !x.c.Prefs().agentBell
	x.c.SetPref(prefAgentBell, strconv.FormatBool(on))
	if on {
		x.c.Notify(x.c.Tr("autodoc.crew.bell_on"))
		return
	}
	x.c.Notify(x.c.Tr("autodoc.crew.bell_off"))
}

// ring rings the bell, while it is on and the last ring is crewBellEvery past.
func (x *crewInbox) ring() {
	if !x.c.Prefs().agentBell {
		return
	}
	now := x.now()
	if !x.rang.IsZero() && now.Sub(x.rang) < crewBellEvery {
		return
	}
	x.rang = now
	x.c.Bell()
}

// ringBell is Core.Bell: the terminal's bell in the terminal build, the desktop's message sound in
// the window, played off the loop.
func (h *Host) ringBell() {
	if !h.gui {
		if h.p != nil {
			h.p.App().Bell()
		}
		return
	}
	path, args, ok := bellPlayer(exec.LookPath, fileExists)
	if !ok {
		return
	}
	do(h, func(ctx context.Context) any {
		ctx, cancel := context.WithTimeout(ctx, crewBellWait)
		defer cancel()
		return exec.CommandContext(ctx, path, args...).Run() // a sound not played is no failure to report
	}, func(any) {})
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// bellPlayer is the program that plays the message sound, and its arguments: canberra-gtk-play by
// the sound's name, else pw-play or paplay on its file when the file is there; ok is false when
// there is none.
func bellPlayer(look func(string) (string, error), exists func(string) bool) (path string, args []string, ok bool) {
	if p, err := look("canberra-gtk-play"); err == nil {
		return p, []string{"-i", "message-new-instant"}, true
	}
	if !exists(crewBellSound) {
		return "", nil, false
	}
	for _, name := range []string{"pw-play", "paplay"} {
		if p, err := look(name); err == nil {
			return p, []string{crewBellSound}, true
		}
	}
	return "", nil, false
}
