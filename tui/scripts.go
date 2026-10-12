package tui

import (
	"cmp"
	"embed"
	"fmt"
	"os"
	"strings"

	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/script"
)

// LUA COMMANDS — the editor's command line extended with scripts (package script): AutoDoc's own,
// embedded here (table, mermaid), then the user's, every *.lua in the config folder's lua/, in
// name order. Scripts register through the global table "autodoc": autodoc.command{…}, which is a
// command catalog entry (catalog.go), lua.<name> unless it names its own lua.* id; autodoc.run(id,
// …), which runs any entry but a plugin's; autodoc.notify(…); and call the daemon through it:
// autodoc.call(…), autodoc.search.query(…) (scripting.go). A command takes its name on the command
// line from whatever has it, and only that name; a later one of the same id replaces an earlier,
// so a user's command named as one of AutoDoc's replaces it.

// ownScripts are AutoDoc's own Lua commands.
//
//go:embed lua/*.lua
var ownScripts embed.FS

// loadScripts starts the Lua engine on the editor and loads the scripts. A script that fails to
// load is said once the program runs; the others still load.
func (h *Host) loadScripts() {
	h.scripts = script.New(h.core, script.WithGlobal("autodoc"), script.WithNotify(h.notify))
	if h.cat != nil {
		h.scripts.SetRegistrar(h.registerLua)
	}
	// autodoc.call and autodoc.run now; autodoc.<domain> once connected (scripting.go)
	h.luaFeature().bind(h.scripts, h.runFromLua)
	errs := h.scripts.LoadDir(ownScripts, "lua")
	if h.luaDir != "" {
		errs = append(errs, h.scripts.LoadDir(os.DirFS(h.luaDir), ".")...)
	}
	for _, err := range errs {
		h.p.Post(func() { h.failed("a Lua command did not load", err) })
	}
}

// registerLua is a Lua command{…}, as the catalog takes it: its verb its name, and run on the core
// it is given: the prompt's own, or the attached editor from anywhere else (a key, autodoc.run, a
// plugin's host.call), as one undo step.
func (h *Host) registerLua(r script.Registration) error {
	cmd := r.Command
	return h.cat.add(Entry{ID: r.ID, Source: SourceLua, Owner: cmp.Or(r.File, "a script"), Title: r.Title,
		Verb: cmd.Name, Aliases: cmd.Aliases, Args: cmd.Args, Help: cmd.Help,
		Run: func(inv Invocation) error {
			core := cmp.Or(inv.Core, h.core)
			return core.Batch(func() error {
				return cmd.Run(widget.CommandContext{Core: core, Name: cmd.Name, Bang: inv.Bang, Args: inv.Args, Line: inv.Line})
			})
		}})
}

// runFromLua is autodoc.run's: any entry but a plugin's, which one extension may not drive for
// another (ADR 1791762474 §R4).
func (h *Host) runFromLua(id string, args []string) error {
	if strings.HasPrefix(id, "plugin.") {
		return fmt.Errorf("refused: %s is a plugin's command, which Lua cannot run", id)
	}
	if h.cat == nil {
		return fmt.Errorf("%w: %s", errUnknownCommand, id)
	}
	return h.cat.run(id, args)
}
