package feature

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/golib/decl"
)

// FEATURES — what the program is made of, each contributing its own state and commands.
//
// A feature is one file's worth of the program: the App.* sources it publishes and the commands the
// document may invoke. The registry merges them into the program's Sources and Handlers, refusing a
// name two features both claim (golib would let the later one win without a word). Today's host is
// one feature, legacy (tui/featurehost.go); features move out of it one at a time.

// HostOwned marks the host's own part of the program (the host's legacy feature), which the
// catalog lists as built in, until each of its parts moves into a feature.
type HostOwned interface{ HostOwned() }

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
	// Starter hears once that the program has started: the store's preferences are in and applied
	// (startOnce), never the defaults a preference set before them was applied to.
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
	// LanguageSink hears that the UI speaks another language
	// (docs/decisions/editor.md#six-languages): it composes again the text it keeps on screen,
	// reading the language from its host (Tr, Trf).
	LanguageSink interface{ LanguageChanged() }
	// PreferenceSink projects the preferences currently in effect on the loop.
	// It also runs when preference dialogs resync; this is not a change-only event.
	PreferenceSink interface{ PreferencesChanged() }
	// PrefSetSink hears each preference set through its row alone (Core.SetPref), by name, after
	// the row's own Changed: a feature that follows another's preference (the GlobeMap, the
	// terminals' theme). A choice in the Preferences dialog is PreferencesChanged instead.
	PrefSetSink interface{ PrefSet(name string) }
	// Enterer hears each workspace entered, the one in use again included.
	Enterer interface{ Entered(ws string) }
	// EventSink hears the daemon's events of the kinds it names (sys.events), on the loop.
	EventSink interface {
		Kinds() []string
		Event(kind, workspace, detail string)
	}
	// Pickers has pickers of its own (pick.go), by the name Picker.qml's handlers pass:
	// App.pickFilter(name, text) reaches Picks()[name].
	Pickers interface{ Picks() map[string]Picker }
)

// registry is the program's features, merged.
type Registry struct {
	Features  []Feature
	State     map[string]any
	Handlers  map[string]decl.HandlerFunc
	picks     map[string]Picker // every feature's pickers, which the App.pick* commands reach by name
	Finds     []FindPane        // the panes / searches, in the features' order (tui/find.go)
	PageShown bool              // a page was shown to the DocWatchers (tui/page.go): the next one closes it first
	Catalog   *Catalog          // the user commands: the features' now, Lua's and the plugins' as they come
	Panels    *PanelRegistry
}

// newRegistry merges fs; a source, a command or a picker two features both name is an error, and so
// is a catalog entry the catalog refuses (catalog.go). It adds one feature of its own, pickCommands:
// the App.pick* commands over every feature's picks.
func NewRegistry(fs ...Feature) (*Registry, error) {
	return NewRegistryWithPanels(nil, fs...)
}

// The host binds the validated snapshot before State runs, so features read
// geometry from their actual program's panel schema even at first assembly.
func NewRegistryWithPanels(bind func([]PanelSpec), fs ...Feature) (*Registry, error) {
	panels, err := NewPanelRegistry(fs)
	if err != nil {
		return nil, err
	}
	r := &Registry{State: map[string]any{}, Handlers: map[string]decl.HandlerFunc{}, picks: map[string]Picker{},
		Catalog: NewCatalog()}
	fs = append(slices.Clip(fs), registryPicks{r})
	r.Features = fs
	r.Panels = panels
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
		if _, ok := f.(HostOwned); ok {
			src = SourceBuiltin // the host's own, until each moves into a feature
		}
		for _, e := range c.Catalog() {
			e.Source, e.Owner = src, f.Name()
			if err := r.Catalog.Add(e); err != nil {
				return nil, err
			}
		}
	}
	if err := KeyCatalog(fs).Check(MainIDs, r.Catalog); err != nil {
		return nil, err
	}
	if bind != nil {
		bind(panels.Specs)
	}
	for _, f := range fs {
		state := f.State()
		for _, name := range slices.Sorted(maps.Keys(state)) {
			if err := claim(f, name); err != nil {
				return nil, err
			}
			r.State[name] = state[name]
		}
		cmds := f.Commands()
		for _, name := range slices.Sorted(maps.Keys(cmds)) {
			if err := claim(f, name); err != nil {
				return nil, err
			}
			r.Handlers[name] = cmds[name].Handler(name)
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
			r.Finds = append(r.Finds, pane)
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
type registryPicks struct{ r *Registry }

func (registryPicks) Name() string                   { return "pickers" }
func (registryPicks) State() map[string]any          { return nil }
func (p registryPicks) Commands() map[string]Command { return PickCommands(p.r.picks) }

// stopping retires pending UI work before the program unmounts its owners.
func (r *Registry) Stopping() {
	if r == nil {
		return
	}
	for _, f := range r.Features {
		if s, ok := f.(Stopping); ok {
			s.Stopping()
		}
	}
}

// stopped tells the features that stop what they started, once the loop has ended.
func (r *Registry) Stopped() {
	if r == nil {
		return
	}
	for _, f := range r.Features {
		if s, ok := f.(Stopper); ok {
			s.Stopped()
		}
	}
}

// themeChanged tells the features that hear the theme the screen wears.
func (r *Registry) ThemeChanged() {
	if r == nil {
		return
	}
	for _, f := range r.Features {
		if t, ok := f.(ThemeSink); ok {
			t.ThemeChanged()
		}
	}
}

// languageChanged tells the features that keep text on screen the language the UI now speaks.
func (r *Registry) LanguageChanged() {
	if r == nil {
		return
	}
	for _, f := range r.Features {
		if l, ok := f.(LanguageSink); ok {
			l.LanguageChanged()
		}
	}
}

func (r *Registry) PrefSet(name string) {
	if r == nil {
		return
	}
	for _, f := range r.Features {
		if p, ok := f.(PrefSetSink); ok {
			p.PrefSet(name)
		}
	}
}

func (r *Registry) PreferencesChanged() {
	if r == nil {
		return
	}
	for _, f := range r.Features {
		if p, ok := f.(PreferenceSink); ok {
			p.PreferencesChanged()
		}
	}
}

// connected tells the features that hear connections.
func (r *Registry) Connected() {
	if r == nil {
		return // a host built without its options (tests)
	}
	for _, f := range r.Features {
		if c, ok := f.(Connecter); ok {
			c.Connected()
		}
	}
}

// started tells the features that hear the start.
func (r *Registry) Started() {
	if r == nil {
		return
	}
	for _, f := range r.Features {
		if s, ok := f.(Starter); ok {
			s.Started()
		}
	}
}

// entered tells the features that hear workspaces entered.
func (r *Registry) Entered(ws string) {
	if r == nil {
		return // a host built without its options (tests)
	}
	for _, f := range r.Features {
		if e, ok := f.(Enterer); ok {
			e.Entered(ws)
		}
	}
}

// event hands an event to the features that named its kind; whether any did.
func (r *Registry) Event(kind, workspace, detail string) bool {
	if r == nil {
		return false
	}
	heard := false
	for _, f := range r.Features {
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

// Core is what a feature may use of the host: all of it, so a feature never reaches for a Host
// field. Every method runs on the loop, as the feature's own handlers do.
type Core interface {
	// AgentEntries and HandTask are AutoDoc's own agent capability, not an
	// AutoVim mailbox. Snapshots carry exact incarnation tokens for delivery.
	AgentEntries() []AgentEntry
	HandTask(to AgentRef, task TaskRef, instruction string, done func(error)) error
	// SendText is HandTask's delivery for text, a plain message through the agent messaging
	// (tui/agentmessaging.go)
	SendText(to AgentRef, text string, done func(error)) error
	Set(name string, v any)
	Open(id string)
	Close(id string)
	// Tr is a catalog message's text in the UI's language now, and Trf the same with %1, %2 … filled
	// (tui/language.go): for a notice, a question or a status a feature composes.
	Tr(id string) string
	Trf(id string, args ...any) string
	Notify(msg string)
	// NotifyOngoing is a long task's toast, id's, updated in place; NotifyDone ends it with its last
	// message, which the history keeps (tui/notify.go)
	NotifyOngoing(id, msg string)
	NotifyDone(id, msg string)
	// NotifyOngoingClick is NotifyOngoing whose click runs onClick, on the loop, instead of opening
	// the history: a candidate's toast reopens its card (tui/candidate.go)
	NotifyOngoingClick(id, msg string, onClick func())
	// WidgetsChanged tells the host that what of a feature is alive changed (a shell, an agent, a
	// plugin run): the dock lists its widgets again (tui/dock.go)
	WidgetsChanged()
	// Ask asks a yes-or-no question (ask.go): OnYes runs on Yes; the Asked rewrites its text.
	Ask(q Question) Asked
	// Workspace is the workspace in use; "" before one is entered.
	Workspace() string
	// Prefs are the preferences in effect.
	Prefs() PrefsView
	// Env is the program's environment: its HOME, PATH and the like, and what a process it starts
	// inherits. nil is the process's own (internal/procenv).
	Env() procenv.Env
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
	// OpenAbsolute opens the file at an absolute path: as its workspace's file when one indexes it,
	// else outside every workspace.
	OpenAbsolute(abs string)
	// Say puts a message about the page or the view on the status line, until the next.
	Say(msg string)
	// SetPref sets one of the feature's preferences (its PrefSpec row names it), through the row
	// alone: read into Prefs at once, the row's chooser (Index) and State published and its Changed
	// run when the value changed, then stored, in order with the other preferences written. Nothing else is applied
	// again: what a preference changes on screen is its row's State and Changed. A name no row
	// declares is ignored.
	SetPref(name, value string)
	// Files is the workspace's files as last listed (tui/page.go): one snapshot, not to be changed.
	Files() FileList
	// Workspaces are the daemon's workspaces, as last listed.
	Workspaces() []Workspace
	// View calls a method of a view of the document, by its id: a drawer's close, a tree's
	// toggleExpanded.
	View(id, method string, args ...any) error
	// TogglePanel opens or closes a side panel, by its id, its own way (tui/panels.go).
	TogglePanel(id string)
	// TogglePanelTab is a tabbed panel's toggle for one of its tabs: closed or hidden, the panel
	// shown on that tab; shown on another tab, that tab; shown on it, closed (tui/panels.go).
	TogglePanelTab(id string, tab int)
	// PanelShown reports whether panel id is open and not hidden by the dock: on screen.
	PanelShown(id string) bool
	// HidePanel hides an open panel, not closed: it keeps what it holds, its Closed hook does not
	// run, and the dock shows it again as it was (tui/dock.go). The editor has the keyboard, as after the
	// dock's own click.
	HidePanel(id string)
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
	// Page is the page as it is now (tui/page.go: pageNow): it can take a fresh snapshot, when typed since
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
	ID                int64 // the store's own id, which a rename keeps (0 from a daemon before it)
	Name, Root, State string
	Include, Exclude  []string
	SectionTokens     int64    // the tokens a section holds: its own, or 0 for the default
	Schema            string   // its frontmatter schema's path; "" for none
	TextExtensions    []string // its own plain-text extensions
}

// same reports whether w and o are the same workspace, in the same state, over the same files.
func (w Workspace) Same(o Workspace) bool {
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

// NewTicket is a Ticket current while current says so (the host's Core.Ticket makes them).
func NewTicket(current func() bool) Ticket { return Ticket{current: current} }

// Current reports whether no newer answer was asked for and the scope has not moved; a zero
// Ticket is always current.
func (t Ticket) Current() bool { return t.current == nil || t.current() }

// FindPane is a pane / searches: what find calls it, the view whose keyboard makes it the pane
// searched, and its rows as they are now. Name and View are declarations, the same whenever read: a
// panel's View is its own PanelSpec's, never one looked up.
type FindPane struct {
	Name string // "explorer": "find in the explorer", "n and N find again in the explorer"
	View string // / searches the pane whose view has the keyboard
	// Scope is the pane's keyScope (keys.go), where it answers / n N itself; "" for none, and the
	// Window's / n N, which say where to put the keyboard, answer there
	Scope string
	// Rows are the pane's rows as it shows them, the row under its cursor (-1 for none), and how to put
	// the cursor on row i, the match at col (grapheme columns). The move is bound to this same snapshot,
	// so a pane whose rows are not plain indexes (the explorer's tree) moves to the row it listed.
	Rows func() (rows []string, cursor int, moveTo func(row, col int) error)
}

// Findable is a feature whose pane / searches. The registry takes the panes in the features' order,
// the page first (the host's own).
type Findable interface{ FindPane() FindPane }

// FileList is the current workspace's files, as last listed: replaced whole on each listing, so a
// reader may keep it, and never changes it.
type FileList struct {
	Paths  []string // nil until the first listing of the workspace in use answers
	Loaded bool     // a listing has answered since the workspace was entered
	Gen    uint64   // moves on each listing, and on entering a workspace
}

// Page is what the editor shows: a file, or the draft (Ref zero). Text is one snapshot, handed to
// every watcher as it is: a reader never changes it.
type Page struct {
	Ref     FileRef
	Kind    kind.Kind // how its text is read: derived, raw and the daemon's registrations applied
	Text    []byte
	Outside bool // a file in no workspace
	// Line and At are the cursor when Text was taken: its line (0-based) and byte offset in Text
	Line, At int
	// Version is the page's edit count when Text was taken: it rises with every edit and with every
	// page shown (a note opened or closed, a file kept as the draft), never with a cursor move
	Version int
}

// AgentRef identifies a profile and, optionally, its exact running incarnation.
// An empty Instance resolves the current incarnation at acceptance.
type AgentRef struct{ Codename, Instance string }

// TaskRef is a detached task identity; Path is the absolute task-file path.
type TaskRef struct{ Title, Path, ID string }

// AgentEntry is a value snapshot. Labels may repeat; Ref never routes by label.
type AgentEntry struct {
	Ref                      AgentRef
	Name, Command            string
	Running, Closing, Orphan bool
	// Peer is another host's crew agent (todos' A lists them beside the window's own): nil for the
	// window's own agents.
	Peer *PeerAgent
}

// PeerAgent is another host's live crew agent, as crew.list gives it: its scope (its address is
// codename@scope), the project it is labelled by, and whether a wake reaches it now (why not).
type PeerAgent struct {
	Scope, Project, Why string
	Reachable           bool
}

// fileRef is a document opened: its workspace ("" outside every one) and its path.
type FileRef struct{ WS, Path string }

// More optional parts of a Feature.
type (
	// Lister hears that the workspace's files or the daemon's workspaces were listed again; it
	// reads Core.Files and Core.Workspaces for them.
	Lister interface{ Listed() }
	// DocWatcher hears the page's lifecycle, on the loop, in this order: Closed (when a page was
	// shown), then Shown; Edited, Moved and Reread only between a Shown and the next Closed.
	DocWatcher interface {
		Shown(Page)         // a file read and shown, or the draft started
		Edited(Page)        // the text after a typing pause
		Moved(line, at int) // the cursor moved, edit or not: its line (0-based) and byte offset
		Reread(Page)        // the same text read as another kind (the build's registrations came)
		Closed()            // the page shown is going: another is about to be shown
	}
)

// listed tells the features that hear listings.
func (r *Registry) Listed() {
	if r == nil {
		return
	}
	for _, f := range r.Features {
		if l, ok := f.(Lister); ok {
			l.Listed()
		}
	}
}

// watchers are the features that hear the page.
func (r *Registry) watchers() []DocWatcher {
	if r == nil {
		return nil
	}
	var out []DocWatcher
	for _, f := range r.Features {
		if w, ok := f.(DocWatcher); ok {
			out = append(out, w)
		}
	}
	return out
}

// shown tells the watchers a page is shown, the one before it closed first.
func (r *Registry) Shown(p Page) {
	if r == nil {
		return
	}
	if r.PageShown {
		for _, w := range r.watchers() {
			w.Closed()
		}
	}
	r.PageShown = true
	for _, w := range r.watchers() {
		w.Shown(p)
	}
}

// edited, moved and reread tell the watchers, while a page is shown.
func (r *Registry) Edited(p Page) {
	if r == nil || !r.PageShown {
		return
	}
	for _, w := range r.watchers() {
		w.Edited(p)
	}
}

func (r *Registry) Moved(line, at int) {
	if r == nil || !r.PageShown {
		return
	}
	for _, w := range r.watchers() {
		w.Moved(line, at)
	}
}

func (r *Registry) Reread(p Page) {
	if r == nil || !r.PageShown {
		return
	}
	for _, w := range r.watchers() {
		w.Reread(p)
	}
}
