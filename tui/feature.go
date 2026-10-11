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
	State() map[string]any                 // its App.* sources and their first values
	Commands() map[string]decl.HandlerFunc // its handlers, by full name: "App.openTodos"
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
	features []Feature
	state    map[string]any
	handlers map[string]decl.HandlerFunc
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
			r.handlers[name] = cmds[name]
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
	return []Feature{legacy{h}, newStartup(core{h}, h.aboutInfo.Version), newTodos(core{h})}
}

// legacy is the host as it is: everything not yet a feature of its own.
type legacy struct{ h *Host }

func (legacy) Name() string                            { return "legacy" }
func (l legacy) State() map[string]any                 { return l.h.state() }
func (l legacy) Commands() map[string]decl.HandlerFunc { return l.h.commands() }

// Core is what a feature may use of the host: all of it, so a feature never reaches for a Host
// field. Every method runs on the loop, as the feature's own handlers do.
type Core interface {
	Set(name string, v any)
	Open(id string)
	Close(id string)
	Notify(msg string)
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
	// same connection and (for TicketWorkspace) the same workspace, is Current.
	Ticket(seq *uint64, scope TicketScope) Ticket
}

// TicketScope is what makes an answer stale besides a newer one.
type TicketScope int

const (
	TicketSession   TicketScope = iota // a reconnect
	TicketWorkspace                    // a reconnect, or a workspace switch
)

// Ticket is one answer's claim to still matter.
type Ticket struct {
	h          *Host
	scope      TicketScope
	seq        *uint64
	n          uint64
	gen, epoch uint64
}

// Current reports whether no newer answer was asked for and the scope has not moved.
func (t Ticket) Current() bool {
	if *t.seq != t.n || t.h.session.Gen() != t.gen {
		return false
	}
	return t.scope == TicketSession || t.h.epoch == t.epoch
}

// core is the host as a feature sees it.
type core struct{ h *Host }

func (c core) Set(name string, v any)        { c.h.set(name, v) }
func (c core) Open(id string)                { c.h.open(id) }
func (c core) Close(id string)               { c.h.closeDialog(id) }
func (c core) Notify(msg string)             { c.h.notify(msg) }
func (c core) Workspace() string             { return c.h.ws }
func (c core) Prefs() prefs                  { return c.h.prefs }
func (c core) OpenFile(abs string)           { c.h.openAbsolute(abs) }
func (c core) Failed(what string, err error) { c.h.failed(what, err) }
func (c core) Focus(id string)               { c.h.keep(c.h.p.Call(id, "forceActiveFocus")) }
func (c core) Call(ctx context.Context, verb string, params ...any) (any, error) {
	return c.h.session.Call(ctx, verb, params...)
}
func (c core) Go(fn func(context.Context) any, then func(any)) { do(c.h, fn, then) }
func (c core) Ticket(seq *uint64, scope TicketScope) Ticket {
	*seq++
	return Ticket{h: c.h, scope: scope, seq: seq, n: *seq, gen: c.h.session.Gen(), epoch: c.h.epoch}
}
