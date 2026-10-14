package tui

import (
	"errors"
	"time"

	"github.com/yongjohnlee80/autodoc/script"
)

// LUA EVENTS (ADR 1792004705 §4.4): autodoc.on(event, fn) hears what happens to the page.
//
//	open    a file shown in the editor (show), or the draft after a close: { path, workspace }; path
//	        nil for the draft. Not readPage's: it runs again on an open page when registrations come
//	save    a save that wrote the editor's text: { path, workspace }
//	mode    the editor's mode changed (App.syncMode): { mode, previous }, as ed:mode() says them
//	change  the text changed, once a pause follows the edits: { revision }, as ed:revision() says it
//
// A handler's own edits do not emit change again: the revision after the handlers is the one the
// next emit compares with.

// luaChangeWait is the pause after the last edit before change is emitted.
// NOTE: debounce: a burst of keystrokes is one change, which no event announces the end of.
const luaChangeWait = 300 * time.Millisecond

// emitLua runs event's handlers; their errors are notifications.
func (h *Host) emitLua(event string, info map[string]any) {
	if h.scripts == nil || !h.scripts.Handles(event) {
		return
	}
	for _, err := range h.scripts.Emit(event, info) {
		if !errors.Is(err, script.ErrStale) {
			h.notify("a Lua handler failed: " + err.Error())
		}
	}
}

// emitOpen is open, for the page readPage set up. Setting it up is not an edit: its revision is
// change's starting point.
func (h *Host) emitOpen() {
	h.luaChangeRev = h.core.Revision()
	info := map[string]any{}
	if h.file.open {
		info["path"] = h.file.path
		if h.file.ws != "" {
			info["workspace"] = h.file.ws
		}
	}
	h.emitLua("open", info)
}

// emitMode is mode, when the editor's mode is another than the one last emitted.
func (h *Host) emitMode() {
	m := script.ModeName(h.core.Mode())
	if m == h.luaMode {
		return
	}
	prev := h.luaMode
	h.luaMode = m
	info := map[string]any{"mode": m}
	if prev != "" {
		info["previous"] = prev
	}
	h.emitLua("mode", info)
}

// changeSoon emits change after the pause, for the last edit only, into the engine running when it
// was set (a reload bumps the generation), and not for a handler's own edits.
func (h *Host) changeSoon() {
	if h.scripts == nil || !h.scripts.Handles("change") {
		return
	}
	h.luaChangeGen++
	gen := h.luaChangeGen
	h.after(luaChangeWait, func() {
		if gen != h.luaChangeGen || h.core.Revision() == h.luaChangeRev {
			return // a later edit's timer emits; or nothing but a handler's own edits since
		}
		h.emitLua("change", map[string]any{"revision": h.core.Revision()})
		h.luaChangeRev = h.core.Revision() // after the handlers: their own edits are in it
	})
}
