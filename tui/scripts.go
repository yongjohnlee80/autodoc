package tui

import (
	"embed"
	"os"

	"github.com/yongjohnlee80/autodoc/script"
)

// LUA COMMANDS — the editor's command line extended with scripts (package script): AutoDoc's own,
// embedded here (table, mermaid), then the user's, every *.lua in the config folder's lua/, in
// name order. A user's command with the name of one of AutoDoc's replaces it. Scripts register
// through the global table "autodoc": autodoc.command{…}, autodoc.notify(…).

// ownScripts are AutoDoc's own Lua commands.
//
//go:embed lua/*.lua
var ownScripts embed.FS

// loadScripts starts the Lua engine on the editor and loads the scripts. A script that fails to
// load is said once the program runs; the others still load.
func (h *Host) loadScripts() {
	h.scripts = script.New(h.core, script.WithGlobal("autodoc"), script.WithNotify(h.notify))
	errs := h.scripts.LoadDir(ownScripts, "lua")
	if h.luaDir != "" {
		errs = append(errs, h.scripts.LoadDir(os.DirFS(h.luaDir), ".")...)
	}
	for _, err := range errs {
		h.p.Post(func() { h.notify("a Lua command did not load: " + err.Error()) })
	}
}
