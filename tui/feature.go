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
	// Stopping completes pending UI work synchronously before the program unmounts.
	// It runs in the loop's Update phase; it must not block or post and wait.
	Stopping interface{ Stopping() }
	// Stopper hears once, after the program's loop has ended, that the program is stopping: it stops
	// what it started (processes, connections). Each stop is bounded by the Stopper's own deadline,
	// so the session closes after a stop that hangs, not never.
	Stopper interface{ Stopped() }
	// ThemeSink hears that the screen wears another theme; it reads which from its host.
	ThemeSink interface{ ThemeChanged() }
	// PreferenceSink projects the preferences currently in effect on the loop.
	// It also runs when preference dialogs resync; this is not a change-only event.
	PreferenceSink interface{ PreferencesChanged() }
	// Enterer hears each workspace entered, the one in use again included.
	Enterer interface{ Entered(ws string) }
	// EventSink hears the daemon's events of the kinds it names (sys.events), on the loop.
	EventSink interface {
		Kinds() []string
		Event(kind, workspace, detail string)
	}
	// Pickers has pickers of its own (pick.go), by the name Picker.qml's handlers pass:
	// App.pickFilter(name, text) reaches Picks()[name].
	Pickers interface{ Picks() map[string]picker }
)

// registry is the program's features, merged.
type registry struct {
	features  []Feature
	state     map[string]any
	handlers  map[string]decl.HandlerFunc
	picks     map[string]picker // every feature's pickers, which the App.pick* commands reach by name
	finds     []FindPane        // the panes / searches, in the features' order (find.go)
	pageShown bool              // a page was shown to the DocWatchers (page.go): the next one closes it first
	catalog   *catalog          // the user commands: the features' now, Lua's and the plugins' as they come
	panels    *panelRegistry
}

// newRegistry merges fs; a source, a command or a picker two features both name is an error, and so
// is a catalog entry the catalog refuses (catalog.go). It adds one feature of its own, pickCommands:
// the App.pick* commands over every feature's picks.
func newRegistry(fs ...Feature) (*registry, error) {
	return newRegistryWithPanels(nil, fs...)
}

// The host binds the validated snapshot before State runs, so features read
// geometry from their actual program's panel schema even at first assembly.
func newRegistryWithPanels(bind func([]PanelSpec), fs ...Feature) (*registry, error) {
	panels, err := newPanelRegistry(fs)
	if err != nil {
		return nil, err
	}
	r := &registry{state: map[string]any{}, handlers: map[string]decl.HandlerFunc{}, picks: map[string]picker{},
		catalog: newCatalog()}
	fs = append(slices.Clip(fs), registryPicks{r})
	r.features = fs
	r.panels = panels
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
	for _, f := range fs {
		c, ok := f.(Cataloged)
		if !ok {
			continue
		}
		src := SourceFeature
		if _, ok := f.(legacy); ok {
			src = SourceBuiltin // the host's own, until each moves into a feature
		}
		for _, e := range c.Catalog() {
			e.Source, e.Owner = src, f.Name()
			if err := r.catalog.add(e); err != nil {
				return nil, err
			}
		}
	}
	if err := keyCatalog(fs).check(mainIDs, r.catalog); err != nil {
		return nil, err
	}
	if bind != nil {
		bind(panels.specs)
	}
	for _, f := range fs {
		state := f.State()
		for _, name := range slices.Sorted(maps.Keys(state)) {
			if err := claim(f, name); err != nil {
				return nil, err
			}
			r.state[name] = state[name]
		}
		cmds := f.Commands()
		for _, name := range slices.Sorted(maps.Keys(cmds)) {
			if err := claim(f, name); err != nil {
				return nil, err
			}
			r.handlers[name] = cmds[name].handler(name)
		}
		if p, ok := f.(Pickers); ok {
			picks := p.Picks()
			for _, name := range slices.Sorted(maps.Keys(picks)) {
				if err := claim(f, "the picker "+name); err != nil {
					return nil, err
				}
				r.picks[name] = picks[name]
			}
		}
		if p, ok := f.(Findable); ok {
			pane := p.FindPane()
			if err := checkFindPane(f, pane); err != nil {
				return nil, err
			}
			for _, name := range []string{"the find pane " + pane.Name, "the find view " + pane.View} {
				if err := claim(f, name); err != nil {
					return nil, err
				}
			}
			r.finds = append(r.finds, pane)
		}
	}
	return r, nil
}

// checkFindPane refuses a pane find could not route to: one without a name, a view or its rows, and
// a panel feature's whose view is not one of its own panels'.
func checkFindPane(f Feature, pane FindPane) error {
	if pane.Name == "" || pane.View == "" || pane.Rows == nil {
		return fmt.Errorf("tui: %s's find pane needs a name, a view and its rows", f.Name())
	}
	if p, ok := f.(Panel); ok && !slices.ContainsFunc(p.Panels(), func(s PanelSpec) bool { return s.View == pane.View }) {
		return fmt.Errorf("tui: %s finds in %s, which is none of its panels' views", f.Name(), pane.View)
	}
	return nil
}

// registryPicks is the registry's own feature: Picker.qml's handlers, over every feature's picks
// (the map fills as the registry merges them).
type registryPicks struct{ r *registry }

func (registryPicks) Name() string                   { return "pickers" }
func (registryPicks) State() map[string]any          { return nil }
func (p registryPicks) Commands() map[string]Command { return pickCommands(p.r.picks) }

// stopping retires pending UI work before the program unmounts its owners.
func (r *registry) stopping() {
	if r == nil {
		return
	}
	for _, f := range r.features {
		if s, ok := f.(Stopping); ok {
			s.Stopping()
		}
	}
}

// stopped tells the features that stop what they started, once the loop has ended.
func (r *registry) stopped() {
	if r == nil {
		return
	}
	for _, f := range r.features {
		if s, ok := f.(Stopper); ok {
			s.Stopped()
		}
	}
}

// themeChanged tells the features that hear the theme the screen wears.
func (r *registry) themeChanged() {
	if r == nil {
		return
	}
	for _, f := range r.features {
		if t, ok := f.(ThemeSink); ok {
			t.ThemeChanged()
		}
	}
}

func (r *registry) preferencesChanged() {
	if r == nil {
		return
	}
	for _, f := range r.features {
		if p, ok := f.(PreferenceSink); ok {
			p.PreferencesChanged()
		}
	}
}

// assembleFeatures is initial program construction, before views can run.
// A failed schema or service binding must not publish the candidate registry.
func (h *Host) assembleFeatures(fs ...Feature) error {
	previous, defs := h.reg, h.prefs.panelDefs
	r, err := newRegistryWithPanels(func(specs []PanelSpec) { h.prefs.panelDefs = specs }, fs...)
	if err != nil {
		h.prefs.panelDefs = defs
		return err
	}
	h.reg = r
	if err := r.panels.bind(core{h}); err != nil {
		h.reg, h.prefs.panelDefs = previous, defs
		return err
	}
	return nil
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
	fs := []Feature{pageFind{h}, legacy{h}, h.terminals, h.agents, h.asker, h.noticeLog, h.recent, h.explorer, h.outline, h.rel, h.ai, h.plugins, newStartup(core{h}, h.aboutInfo.Version), newTodos(core{h}), h.luaFeature()}
	fs = append(fs, h.moreFeatures...)
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

// Listed is the workspace's files listed again: File › Open and the draft's name list them.
func (l legacy) Listed() {
	l.h.openPick.setAll(l.h.files.Paths)
	l.h.newPick.setAll(l.h.files.Paths)
}

// Picks are the host's own pickers.
func (l legacy) Picks() map[string]picker {
	return map[string]picker{"search": l.h.searchPick, "open": l.h.openPick, "new": l.h.newPick}
}

// Core is what a feature may use of the host: all of it, so a feature never reaches for a Host
// field. Every method runs on the loop, as the feature's own handlers do.
type Core interface {
	// AgentEntries and HandTask are AutoDoc's own agent capability, not an
	// AutoVim mailbox. Snapshots carry exact incarnation tokens for delivery.
	AgentEntries() []AgentEntry
	HandTask(to AgentRef, task TaskRef, instruction string, done func(error)) error
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
	// SetPref sets one of the feature's preferences (its PrefSpec row names it), through the row
	// alone: read into Prefs at once, the row's chooser (Index) and State published and its Changed
	// run when the value changed, then stored, in order with the other preferences written. Nothing else is applied
	// again: what a preference changes on screen is its row's State and Changed. A name no row
	// declares is ignored.
	SetPref(name, value string)
	// Files is the workspace's files as last listed (page.go): one snapshot, not to be changed.
	Files() FileList
	// Workspaces are the daemon's workspaces, as last listed.
	Workspaces() []Workspace
	// View calls a method of a view of the document, by its id: a drawer's close, a tree's
	// toggleExpanded.
	View(id, method string, args ...any) error
	// TogglePanel opens or closes a side panel, by its id, its own way (panels.go).
	TogglePanel(id string)
	// HasFocus reports whether the view id, or one inside it, has the keyboard: a key bound across
	// the window that only one pane answers.
	HasFocus(id string) bool
	// SearchModelsChanged is a change of search's models: the hits are searched again; with ranker,
	// the Rerank box reads the ranker in use first. A feature that changes them does not reach into
	// the search.
	SearchModelsChanged(ranker bool)
	// Post runs fn on the loop once the event being handled is done: a view still changing (a tab)
	// must not have what it shows changed under it.
	Post(fn func())
	// Page is the page as it is now (page.go: pageNow): it can take a fresh snapshot, when typed since
	// the last one (on the loop, a copy of the editor's text), so a reader that cannot wait for the
	// pause (a picker opened, a plugin started mid-typing) has the text and Version as they are.
	Page() Page
	// Crumbs puts a breadcrumb after the file's name on the page's frame; nil, the name alone. The
	// host composes the title, so the name and its badges are right with no breadcrumb at all.
	Crumbs(parts []string)
	// GoToLine puts the editor's cursor at the start of line (0-based), the page scrolled to it,
	// and gives the editor the keyboard: a feature moves the cursor without reaching the editor.
	GoToLine(line int)
}

// Workspace is one of the daemon's workspaces as a feature reads it: its name, its state, and what
// decides its files.
type Workspace struct {
	Name, Root, State string
	Include, Exclude  []string
	SectionTokens     int64 // the tokens a section holds: its own, or 0 for the default
}

// same reports whether w and o are the same workspace, in the same state, over the same files.
func (w Workspace) same(o Workspace) bool {
	return w.Name == o.Name && w.Root == o.Root && w.State == o.State &&
		slices.Equal(w.Include, o.Include) && slices.Equal(w.Exclude, o.Exclude)
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
func (c core) Workspaces() []Workspace {
	list := make([]Workspace, 0, c.h.workspaces.Len())
	for _, w := range c.h.workspaces.All() {
		list = append(list, Workspace{Name: w.name, Root: w.root, State: w.state, Include: w.include, Exclude: w.exclude, SectionTokens: w.sectionTokens})
	}
	return list
}
func (c core) View(id, method string, args ...any) error { return c.h.p.Call(id, method, args...) }
func (c core) TogglePanel(id string)                     { c.h.togglePanel(id) }
func (c core) SearchModelsChanged(ranker bool) {
	if ranker {
		c.h.loadStageRanker(true) // which searches again, once
		return
	}
	c.h.refreshSearch()
}
func (c core) Post(fn func()) { c.h.p.Post(fn) }
func (c core) HasFocus(id string) bool {
	comp, ok := c.h.p.Find(id)
	return ok && c.h.p.App().FocusWithin(comp)
}
func (c core) Page() Page            { return c.h.pageNow() }
func (c core) Crumbs(parts []string) { c.h.crumbs = parts; c.h.showTitle() }
func (c core) GoToLine(line int) {
	c.h.core.SetLine(line, 0)
	c.h.keep(c.h.p.Call("editor", "forceActiveFocus"))
}
func (c core) SetPref(name, value string) {
	for _, row := range c.h.prefRows {
		if row.Name == name {
			c.h.setRowPref(row, value)
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
