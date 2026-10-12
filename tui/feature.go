package tui

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/yongjohnlee80/golib/decl"
)

// FEATURES — what the program is made of, each contributing its own state and commands.
//
// A feature is one file's worth of the program: the App.* sources it publishes and the commands the
// document may invoke. The registry merges them into the program's Sources and Handlers, refusing a
// name two features both claim (golib would let the later one win without a word). Today's host is
// one feature, legacy; features move out of it one at a time.

// Feature is one part of the program.
type Feature interface {
	Name() string
	State() map[string]any        // its App.* sources and their first values
	Commands() map[string]Command // its commands, by full name: "App.openTodos" (command.go)
}

// Optional parts of a Feature, asserted once when the registry is built.
type (
	// Connecter hears each new connection, after the host has loaded what every connection loads.
	Connecter interface{ Connected() }
	// Starter hears once that the program has started: its first preferences are applied.
	Starter interface{ Started() }
	// Enterer hears each workspace entered, the one in use again included.
	Enterer interface{ Entered(ws string) }
	// EventSink hears the daemon's events of the kinds it names (sys.events), on the loop.
	EventSink interface {
		Kinds() []string
		Event(kind, workspace, detail string)
	}
)

// registry is the program's features, merged.
type registry struct {
	features  []Feature
	state     map[string]any
	handlers  map[string]decl.HandlerFunc
	pageShown bool // a page was shown to the DocWatchers (page.go): the next one closes it first
}

// newRegistry merges fs; a source or a command two features both name is an error.
func newRegistry(fs ...Feature) (*registry, error) {
	r := &registry{features: fs, state: map[string]any{}, handlers: map[string]decl.HandlerFunc{}}
	owner := map[string]string{}
	claim := func(f Feature, name string) error {
		if prev, ok := owner[name]; ok {
			return fmt.Errorf("tui: %s is both %s's and %s's", name, prev, f.Name())
		}
		owner[name] = f.Name()
		return nil
	}
	var rows []PrefSpec
	for _, f := range fs {
		if p, ok := f.(Prefs); ok {
			rows = append(rows, p.Prefs()...)
		}
	}
	if err := checkRows(rows); err != nil {
		return nil, err
	}
	if err := keyCatalog(fs).check(mainIDs); err != nil {
		return nil, err
	}
	for _, f := range fs {
		for _, name := range slices.Sorted(maps.Keys(f.State())) {
			if err := claim(f, name); err != nil {
				return nil, err
			}
			r.state[name] = f.State()[name]
		}
		cmds := f.Commands()
		for _, name := range slices.Sorted(maps.Keys(cmds)) {
			if err := claim(f, name); err != nil {
				return nil, err
			}
			r.handlers[name] = cmds[name].handler(name)
		}
	}
	return r, nil
}

// connected tells the features that hear connections.
func (r *registry) connected() {
	if r == nil {
		return // a host built without its options (tests)
	}
	for _, f := range r.features {
		if c, ok := f.(Connecter); ok {
			c.Connected()
		}
	}
}

// started tells the features that hear the start.
func (r *registry) started() {
	if r == nil {
		return
	}
	for _, f := range r.features {
		if s, ok := f.(Starter); ok {
			s.Started()
		}
	}
}

// entered tells the features that hear workspaces entered.
func (r *registry) entered(ws string) {
	if r == nil {
		return // a host built without its options (tests)
	}
	for _, f := range r.features {
		if e, ok := f.(Enterer); ok {
			e.Entered(ws)
		}
	}
}

// event hands an event to the features that named its kind; whether any did.
func (r *registry) event(kind, workspace, detail string) bool {
	if r == nil {
		return false
	}
	heard := false
	for _, f := range r.features {
		if s, ok := f.(EventSink); ok {
			for _, k := range s.Kinds() {
				if k == kind {
					s.Event(kind, workspace, detail)
					heard = true
				}
			}
		}
	}
	return heard
}

// features are the program's parts, in the order the registry merges them.
func (h *Host) features() []Feature {
	h.asker = newAsker(core{h})
	fs := []Feature{legacy{h}, h.asker, h.noticeLog, newStartup(core{h}, h.aboutInfo.Version), newTodos(core{h}), h.luaFeature()}
	// the preferences every feature declares, read over their defaults until the store's arrive;
	// their choosers are one feature of their own
	h.prefRows = nil
	for _, f := range fs {
		if p, ok := f.(Prefs); ok {
			h.prefRows = append(h.prefRows, p.Prefs()...)
		}
	}
	for _, s := range h.prefRows {
		if s.value(h.prefs) == "" { // a feature's own row: the host's defaults hold the rest
			s.reset(&h.prefs)
		}
	}
	h.keys = keyCatalog(fs)
	return append(fs, prefChoosers{h})
}

// legacy is the host as it is: everything not yet a feature of its own.
type legacy struct{ h *Host }

func (legacy) Name() string                   { return "legacy" }
func (l legacy) State() map[string]any        { return l.h.state() }
func (l legacy) Commands() map[string]Command { return l.h.commands() }
func (l legacy) Prefs() []PrefSpec            { return legacyPrefRows(l.h, l.h.gui) }

// Core is what a feature may use of the host: all of it, so a feature never reaches for a Host
// field. Every method runs on the loop, as the feature's own handlers do.
type Core interface {
	Set(name string, v any)
	Open(id string)
	Close(id string)
	Notify(msg string)
	// Ask asks a yes-or-no question (ask.go): OnYes runs on Yes; the Asked rewrites its text.
	Ask(q Question) Asked
	// Workspace is the workspace in use; "" before one is entered.
	Workspace() string
	// Prefs are the preferences in effect.
	Prefs() prefs
	// OpenFile opens a file by its absolute path in the editor, asking first over unsaved changes.
	OpenFile(abs string)
	// Failed tells what failed and why, in the words a daemon error carries.
	Failed(what string, err error)
	// Call calls a daemon verb; it blocks, so only work Go runs calls it.
	Call(ctx context.Context, verb string, params ...any) (any, error)
	// Go runs fn off the loop and then on the loop with its result, as do does.
	Go(fn func(context.Context) any, then func(any))
	// Ticket issues the next answer of the sequence seq: only the latest one issued, under the
	// same connection and, as scope says, the same workspace and file, is Current. A nil seq
	// numbers nothing: the answer is stale only when its scope moves.
	Ticket(seq *uint64, scope TicketScope) Ticket
	// OpenRef opens a workspace's file in the editor, as the user's open: asking first over unsaved
	// changes, and recording where they left for Back.
	OpenRef(ws, path string)
	// Say puts a message about the page or the view on the status line, until the next.
	Say(msg string)
	// SetPref sets one of the feature's preferences (its PrefSpec row names it): applied to Prefs
	// at once through the row, then stored, in order with the other preferences written. A name no
	// row declares is ignored.
	SetPref(name, value string)
	// Files is the workspace's files as last listed (page.go): one snapshot, not to be changed.
	Files() FileList
	// Workspaces are the daemon's workspaces, by name, as last listed.
	Workspaces() []string
}

// TicketScope is what makes an answer stale besides a newer one.
type TicketScope int

const (
	TicketSession   TicketScope = iota // a reconnect
	TicketWorkspace                    // a reconnect, or a workspace switch
	TicketFile                         // a reconnect, a workspace switch, or another file opened
	TicketLocal                        // nothing but a newer one: work of the terminal's own, no answer
)

// Ticket is one answer's claim to still matter.
type Ticket struct{ current func() bool }

// Current reports whether no newer answer was asked for and the scope has not moved; a zero
// Ticket is always current.
func (t Ticket) Current() bool { return t.current == nil || t.current() }

// core is the host as a feature sees it.
type core struct{ h *Host }

func (c core) Set(name string, v any)        { c.h.set(name, v) }
func (c core) Open(id string)                { c.h.open(id) }
func (c core) Close(id string)               { c.h.closeDialog(id) }
func (c core) Notify(msg string)             { c.h.notify(msg) }
func (c core) Ask(q Question) Asked          { return c.h.ask(q) }
func (c core) Workspace() string             { return c.h.ws }
func (c core) Prefs() prefs                  { return c.h.prefs }
func (c core) OpenFile(abs string)           { c.h.openAbsolute(abs) }
func (c core) Failed(what string, err error) { c.h.failed(what, err) }
func (c core) Focus(id string)               { c.h.keep(c.h.p.Call(id, "forceActiveFocus")) }
func (c core) Call(ctx context.Context, verb string, params ...any) (any, error) {
	return c.h.session.Call(ctx, verb, params...)
}
func (c core) Go(fn func(context.Context) any, then func(any)) { do(c.h, fn, then) }
func (c core) Ticket(seq *uint64, scope TicketScope) Ticket    { return c.h.ticket(seq, scope) }
func (c core) OpenRef(ws, path string)                         { c.h.openIn(ws, path) }
func (c core) Say(msg string)                                  { c.h.say(msg) }
func (c core) Files() FileList                                 { return c.h.files }
func (c core) Workspaces() []string {
	names := make([]string, 0, c.h.workspaces.Len())
	for _, w := range c.h.workspaces.All() {
		names = append(names, w.name)
	}
	return names
}
func (c core) SetPref(name, value string) {
	for _, row := range c.h.prefRows {
		if row.Name == name {
			c.h.setPref(name, value, func(p *prefs) { row.read(p, value) })
			return
		}
	}
}

// ticket is Core.Ticket, for the host's own answers too: every stale-answer guard is one.
func (h *Host) ticket(seq *uint64, scope TicketScope) Ticket {
	var n uint64
	if seq != nil {
		*seq++
		n = *seq
	}
	gen, epoch, file := h.session.Gen(), h.epoch, h.file.gen
	return Ticket{current: func() bool {
		if scope == TicketLocal {
			return seq == nil || *seq == n
		}
		if seq != nil && *seq != n || h.session.Gen() != gen {
			return false
		}
		switch scope {
		case TicketSession:
			return true
		case TicketWorkspace:
			return h.epoch == epoch
		}
		return h.epoch == epoch && h.file.gen == file
	}}
}
