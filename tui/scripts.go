package tui

//go:generate go run ../internal/luarefgen -ref ../docs/lua/reference.md

import (
	"cmp"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tuicore "github.com/yongjohnlee80/golib/tui"
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
// so a user's command named as one of AutoDoc's replaces it. autodoc.commands() lists the catalog
// and autodoc.document() the page (luadocument.go); print, now and date are the engine's.

// ownScripts are AutoDoc's own Lua commands.
//
//go:embed lua/*.lua
var ownScripts embed.FS

// loadScripts starts the Lua engine on the editor and loads the scripts. A script that fails to
// load is said once the program runs; the others still load.
func (h *Host) loadScripts() {
	for _, err := range h.startScripts() {
		h.p.Post(func() { h.failed("a Lua command did not load", err) })
	}
}

// startScripts makes a new engine, binds the program's functions into it and loads AutoDoc's
// scripts, then the user's: what failed to load.
func (h *Host) startScripts() []error {
	h.scripts = script.New(h.core, script.WithGlobal("autodoc"), script.WithNotify(h.notify))
	if h.cat != nil {
		h.scripts.SetRegistrar(h.registerLua)
	}
	// autodoc.call and autodoc.run now; autodoc.<domain> once connected (scripting.go)
	h.luaFeature().bind(h.scripts, h.runFromLua)
	h.luaFeature().bindHost(h.scripts, luaHostOf{h}) // autodoc.clipboard, .ui, .agents (luahost.go)
	h.bindDocument(h.scripts)                        // autodoc.document, autodoc.commands (luadocument.go)
	h.luaFeature().reload = h.reloadScripts
	h.luaFeature().install = h.installPackage
	h.luaFeature().line = h.core.RunCommand
	h.core.SetChordHook(h.luaFeature().chord) // a script's chords, through golib (luakeys.go)
	errs := h.scripts.LoadDir(ownScripts, "lua")
	if h.luaDir != "" {
		errs = append(errs, h.scripts.LoadDir(os.DirFS(h.luaDir), ".")...)
	}
	return append(errs, h.loadPackages()...) // lua/<name>/: gated (luapackages.go)
}

// reloadScripts is :lua-reload: every script loaded again, on a new engine, so an edited file is
// read anew and a removed one is gone. The old engine's commands leave the catalog first, giving
// back the names they took; a callback of the old engine still on its way is dropped when it
// comes (script.ErrStale). A script cannot reload from inside itself: its engine is running.
func (h *Host) reloadScripts() error {
	old := h.scripts
	if old != nil && old.Running() {
		return errors.New("lua-reload: not from inside a script")
	}
	if h.cat == nil {
		// without the catalog the commands are the editor's registry's, which keeps no record of
		// whose they are: the old engine's would stay, bound to a closed state
		return errors.New("lua-reload: this editor has no command catalog")
	}
	var lua []string
	for _, e := range h.cat.entries() {
		if e.Source == SourceLua {
			lua = append(lua, e.ID)
		}
	}
	for _, id := range lua {
		h.cat.remove(id)
	}
	oldRoots := h.luaRoots
	h.luaRoots = nil
	errs := h.startScripts()
	if old != nil {
		old.Close()
	}
	closeRoots(oldRoots)
	h.luaChangeGen++ // a change timer set for the old engine's handlers emits nothing
	if len(errs) > 0 {
		for _, err := range errs {
			h.failed("a Lua command did not load", err)
		}
		return fmt.Errorf("lua-reload: %d of the scripts did not load", len(errs))
	}
	h.notify("reloaded the Lua scripts")
	return nil
}

// closeRoots closes the package folders an engine read from (luapackages.go).
func closeRoots(roots []*os.Root) {
	for _, r := range roots {
		_ = r.Close()
	}
}

// reloadOnSave reloads the scripts when the file just saved, p of workspace ws ("" outside every
// workspace), is one of the user's: a .lua file directly in the config folder's lua/, wherever it
// was opened from (that folder may lie inside a workspace).
func (h *Host) reloadOnSave(ws, p string) {
	p = h.pathOnDisk(ws, p)
	if h.luaDir == "" || filepath.Ext(p) != ".lua" || !filepath.IsAbs(p) {
		return
	}
	dir, err := filepath.Abs(h.luaDir)
	if err != nil {
		return
	}
	if rel, err := filepath.Rel(dir, p); err != nil || rel != filepath.Base(p) {
		return
	}
	if err := h.reloadScripts(); err != nil {
		h.notify(err.Error())
	}
}

// registerLua is a Lua command{…}, as the catalog takes it: its verb its name, and run on the core
// it is given: the prompt's own, or the attached editor from anywhere else (a key, autodoc.run, a
// plugin's host.call), as one undo step.
func (h *Host) registerLua(r script.Registration) error {
	cmd := r.Command
	return h.cat.add(Entry{ID: r.ID, Source: SourceLua, Owner: cmp.Or(r.File, "a script"), Title: r.Title,
		Verb: cmd.Name, Aliases: cmd.Aliases, Args: cmd.Args, Help: cmd.Help, Complete: cmd.Complete,
		Run: func(inv Invocation) error {
			core := cmp.Or(inv.Core, h.core)
			return core.Batch(func() error {
				return cmd.Run(widget.CommandContext{Core: core, Name: cmp.Or(inv.Name, cmd.Name), Bang: inv.Bang, Args: inv.Args, Line: inv.Line})
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

// luaHostOf is the program as the script functions reach it (luahost.go).
type luaHostOf struct{ h *Host }

func (l luaHostOf) copyText(text string) bool { return l.h.p.App().CopyToClipboard(text) }
func (l luaHostOf) readText(done func(string, error)) error {
	_, err := l.h.p.App().ReadClipboard(0, func(_ tuicore.ClipboardRead, text string, err error) { done(text, err) })
	return err
}
func (l luaHostOf) agentEntries() []AgentEntry { return l.h.agents.AgentEntries() }
func (l luaHostOf) sendText(to AgentRef, text string, done func(error)) error {
	return l.h.agents.SendText(to, text, done)
}
func (l luaHostOf) post(fn func()) { l.h.p.Post(fn) }
