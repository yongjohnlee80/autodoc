package tui

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE COMMAND CATALOG (ADR 1791762474) — every command a user can run, wherever it was declared:
// AutoDoc's own, a feature's, a Lua script's (autodoc.command) and a plugin's ([[commands]]). The
// editor's ':' prompt, F1's COMMANDS, the keys (Key.Cmd), SPC p and Lua's autodoc.run all read
// it, so a command declared once is reachable from each.
//
// Only what a person would invoke is here: a menu row, a key, a verb. The App.* handlers stay the
// document's wiring (commands.go); an entry's Run calls the same function as its App.* twin.
//
// An id is <subject>.<action>, lower-case and dotted:
//
//	subject  what the command acts on, one word: file, search, find, explorer, links, terminal,
//	         agent, panel, workspace, view, editor, notices, todos, prefs, plugins, help, app
//	action   what it does to it, words joined by _: open, toggle, new, save, open_recent
//
// so file.open, todos.open, terminal.toggle, find.clear. Two namespaces are owned: lua.<name> is
// Lua's alone (lua.upper), plugin.<plugin>.<id> that plugin's alone (plugin.tetris.new). A feature
// may use neither.
//
// A ':' name (an entry's Verb or one of its Aliases) has exactly one owner at a time. A built-in
// or a feature's entry takes no name golib's own verbs have (w, q, x…) and none another entry has,
// or the program does not start. A Lua command takes any name, golib's too, as it always could:
// the name moves to it, and only that name — the entry it came from keeps its id and its other
// names, and gets the name back when the Lua command goes. The editor's registry
// (widget.Commands) is kept in step: each entry is registered under the names it owns.
//
// # Concurrency Model
//
//   - Ownership: loop-owned, as the editor core it registers into: every method runs on the UI
//     loop, the registry's build (before the program runs) excepted.

// Source is where an entry was declared.
type Source string

const (
	SourceBuiltin Source = "builtin" // AutoDoc's own, not yet a feature of its own (legacy)
	SourceFeature Source = "feature" // a Feature's Catalog()
	SourceLua     Source = "lua"     // autodoc.command{…}
	SourcePlugin  Source = "plugin"  // a manifest's [[commands]]
)

// Entry is one user command, wherever it was declared.
type Entry struct {
	ID     string // stable, dotted: "file.open", "lua.upper", "plugin.tetris.new"
	Source Source // set by whoever adds it: the registry, the Lua engine, the plugins
	Owner  string // the feature's Name(), the Lua file, or the plugin's name
	Title  string // what a card and F1 call it: "open a document"
	// Verb is its ':' name ("terminal"); "" when ':' does not reach it. Aliases are more ':' names
	// ("term"), and need a Verb.
	Verb    string
	Aliases []string
	Args    string // ':' completion's help: "<term>"
	Help    string // one line
	// Twin is the QML its App.* twin is invoked with ("App.openTodos()"), when it has one: a key
	// running it is the entry's key, as F1 lists it.
	Twin string
	Run  func(Invocation) error // on the UI loop
}

// Invocation is how an entry was run: what ':' passes, so nothing is lost on the way.
type Invocation struct {
	Args []string // the words after the verb; a key's or autodoc.run's arguments
	Line string   // everything after the verb, as typed: ":find two words" is "two words"
	Bang bool     // the verb ended in '!'
	// Core is the editor a Lua command's ed wraps: ':' passes its own, every other run the
	// program's attached editor.
	Core *widget.EditorCore
}

// Cataloged is a Feature with user commands. The registry sets each entry's Source and Owner.
type Cataloged interface{ Catalog() []Entry }

// errUnknownCommand is catalog.run's answer to an id no entry has: "unknown: <id>".
var errUnknownCommand = errors.New("unknown")

// commandID is an id's shape: a lower-case first word, then dotted words.
var commandID = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[A-Za-z0-9_-]+)+$`)

// takenOver are golib's verbs AutoDoc's own entries replace: find and nohl are the page's find
// (find.go), so ':' and / share one.
var takenOver = map[string]bool{"find": true, "nohl": true}

// golibVerbs are the names of golib's own verbs an entry may not take (a Lua command excepted).
var golibVerbs = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range widget.DefaultCommands() {
		for _, n := range append([]string{c.Name}, c.Aliases...) {
			m[n] = true
		}
	}
	return m
}()

// catalog is the program's entries: the features' at start, then Lua's and plugins' as they
// register.
type catalog struct {
	byID  map[string]*Entry
	order []string
	names map[string]string // ':' name → the id of the entry owning it
	// the editor's registry, kept in step, and the core every run outside ':' is given; nil before
	// attach
	cmds   *widget.Commands
	core   *widget.EditorCore
	synced map[string][]string // the names each entry is registered under in cmds
	// changed hears every add and remove: F1 is made from the catalog
	changed func()
}

func newCatalog() *catalog {
	return &catalog{byID: map[string]*Entry{}, names: map[string]string{}, synced: map[string][]string{}}
}

// declared are e's ':' names: its verb, then its aliases.
func (e *Entry) declared() []string {
	if e.Verb == "" {
		return nil
	}
	return append([]string{e.Verb}, e.Aliases...)
}

// who is how a refusal names an entry's owner.
func (e *Entry) who() string {
	switch e.Source {
	case SourceLua:
		return "Lua's " + e.Owner
	case SourcePlugin:
		return "the plugin " + e.Owner + "'s"
	case SourceBuiltin:
		return "AutoDoc's"
	}
	return e.Owner + "'s"
}

// validVerb is a ':' name golib takes: letters, digits, - and _, and not a number.
func validVerb(n string) bool {
	_, err := widget.NewCommands(widget.Command{Name: n, Run: func(widget.CommandContext) error { return nil }})
	return err == nil
}

// check is why e cannot be added, nil when it can. A Lua entry replaces its own id.
func (c *catalog) check(e *Entry) error {
	// the names first: a Lua command's id is made from its name, so a bad name is said as one
	for _, n := range e.declared() {
		if !validVerb(n) {
			return fmt.Errorf("tui: %q is not a ':' name: letters, digits, - and _, and not all digits", n)
		}
	}
	switch {
	case !commandID.MatchString(e.ID):
		return fmt.Errorf("tui: the command id %q: want dotted lower-case words, file.open", e.ID)
	case e.Run == nil:
		return fmt.Errorf("tui: the command %s has nothing to run", e.ID)
	}
	lua, plugin := strings.HasPrefix(e.ID, "lua."), strings.HasPrefix(e.ID, "plugin.")
	switch e.Source {
	case SourceLua:
		if !lua {
			return fmt.Errorf("tui: the Lua command %s: a Lua id is lua.<name>", e.ID)
		}
	case SourcePlugin:
		if !strings.HasPrefix(e.ID, "plugin."+e.Owner+".") {
			return fmt.Errorf("tui: the command %s: the plugin %s's ids are plugin.%s.<id>", e.ID, e.Owner, e.Owner)
		}
		if e.Verb != "" || len(e.Aliases) > 0 {
			return fmt.Errorf("tui: the plugin command %s: a plugin's command has no ':' name", e.ID)
		}
	default:
		if lua || plugin {
			return fmt.Errorf("tui: the command %s: lua.* is Lua's and plugin.* the plugins'", e.ID)
		}
	}
	if prev := c.byID[e.ID]; prev != nil && !(e.Source == SourceLua && prev.Source == SourceLua) {
		return fmt.Errorf("tui: the command %s is both %s and %s", e.ID, prev.who(), e.who())
	}
	if e.Verb == "" && len(e.Aliases) > 0 {
		return fmt.Errorf("tui: the command %s has aliases and no verb", e.ID)
	}
	seen := map[string]bool{}
	for _, n := range e.declared() {
		switch owner := c.names[n]; {
		case seen[n]:
			return fmt.Errorf("tui: the command %s names :%s twice", e.ID, n)
		case e.Source == SourceLua:
		case golibVerbs[n] && !(e.Source == SourceBuiltin && takenOver[n]):
			return fmt.Errorf("tui: the command %s: :%s is the editor's own", e.ID, n)
		case owner != "" && owner != e.ID:
			return fmt.Errorf("tui: :%s is both %s (%s) and %s (%s)", n, c.byID[owner].who(), owner, e.who(), e.ID)
		}
		seen[n] = true
	}
	return nil
}

// add adds e, or refuses it whole: its id, its verb and every alias are checked before any is
// taken. A Lua entry replaces the Lua entry with its id.
func (c *catalog) add(e Entry) error {
	if err := c.check(&e); err != nil {
		return err
	}
	affected := map[string]bool{e.ID: true}
	if c.byID[e.ID] != nil {
		c.drop(e.ID, affected)
	}
	c.byID[e.ID] = &e
	c.order = append(c.order, e.ID)
	for _, n := range e.declared() {
		if prev := c.names[n]; prev != "" {
			affected[prev] = true // a Lua command takes the name, and only it
		}
		c.names[n] = e.ID
	}
	c.sync(affected)
	return nil
}

// remove takes the entry of id out: a plugin uninstalled. Its names go back to the first entry
// still declaring them.
func (c *catalog) remove(id string) {
	if c.byID[id] == nil {
		return
	}
	affected := map[string]bool{id: true}
	c.drop(id, affected)
	c.sync(affected)
}

// drop forgets id, giving its names back, and notes the entries that changed.
func (c *catalog) drop(id string, affected map[string]bool) {
	delete(c.byID, id)
	c.order = slices.DeleteFunc(c.order, func(o string) bool { return o == id })
	var freed []string
	for n, owner := range c.names {
		if owner == id {
			freed = append(freed, n)
		}
	}
	for _, n := range freed {
		delete(c.names, n)
		for _, o := range c.order {
			if slices.Contains(c.byID[o].declared(), n) {
				c.names[n] = o
				affected[o] = true
				break
			}
		}
	}
}

// owned are the names id owns now, in the order it declares them.
func (c *catalog) owned(id string) []string {
	e := c.byID[id]
	if e == nil {
		return nil
	}
	return slices.DeleteFunc(e.declared(), func(n string) bool { return c.names[n] != id })
}

// entries are every entry, in the order they were added.
func (c *catalog) entries() []*Entry {
	out := make([]*Entry, len(c.order))
	for i, id := range c.order {
		out[i] = c.byID[id]
	}
	return out
}

// lookup is the entry of id, nil for none.
func (c *catalog) lookup(id string) *Entry {
	if c == nil {
		return nil
	}
	return c.byID[id]
}

// attach registers the entries' names in core's command line, and gives every run outside ':'
// core.
func (c *catalog) attach(core *widget.EditorCore) {
	if c == nil {
		return // a host built without its options (tests)
	}
	c.core, c.cmds = core, core.Commands()
	all := map[string]bool{}
	for _, id := range c.order {
		all[id] = true
	}
	c.sync(all)
}

// sync re-registers the affected entries in the editor's registry, each under the names it owns,
// and tells changed. Every old registration goes before any new one is made, so a name moving
// from one entry to another is never removed with its new owner.
func (c *catalog) sync(affected map[string]bool) {
	if c.cmds != nil {
		for id := range affected {
			c.cmds.Remove(c.synced[id]...)
			delete(c.synced, id)
		}
		for _, id := range c.order {
			own := c.owned(id)
			if !affected[id] || len(own) == 0 {
				continue
			}
			e := c.byID[id]
			// Set: a Lua command replaces golib's verb of its name, as it always has
			_ = c.cmds.Set(widget.Command{Name: own[0], Aliases: own[1:], Args: e.Args, Help: e.Help,
				Run: func(x widget.CommandContext) error {
					return e.Run(Invocation{Args: x.Args, Line: x.Line, Bang: x.Bang, Core: x.Core})
				}})
			c.synced[id] = own
		}
	}
	if c.changed != nil {
		c.changed()
	}
}

// run runs the entry of id with args, as a key, autodoc.run or a plugin's host.call does: on the
// attached editor, its line the arguments joined.
func (c *catalog) run(id string, args []string) error {
	e := c.lookup(id)
	if e == nil {
		return fmt.Errorf("%w: %s", errUnknownCommand, id)
	}
	defer renamePanic(id) // a panic says which command it came out of (crash.go)
	return e.Run(Invocation{Args: args, Line: strings.Join(args, " "), Core: c.core})
}
