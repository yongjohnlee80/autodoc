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

// emitOpenFor is open, for buffer b: a file loaded into it (shown or not), or the draft it became.
// Loading is not an edit: b's revision is its change event's starting point.
func (h *Host) emitOpenFor(b *openBuffer) {
	if b.buf != nil {
		b.luaRev = b.buf.Revision()
	}
	h.emitLua("open", h.bufferInfo(b))
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

// changeSoonFor emits change for buffer b after the pause, for its last edit only, into the engine
// running when it was set (a reload bumps the generation), and not for a handler's own edits. Each
// buffer debounces on its own: an edit to one never swallows another's change.
func (h *Host) changeSoonFor(b *openBuffer) {
	if h.scripts == nil || !h.scripts.Handles("change") || b.buf == nil {
		return
	}
	b.luaGen++
	gen, eng := b.luaGen, h.luaChangeGen
	h.after(luaChangeWait, func() {
		if gen != b.luaGen || eng != h.luaChangeGen || b.buf.Revision() == b.luaRev {
			return // a later edit's timer emits; a reloaded engine; or only a handler's own edits since
		}
		h.emitLua("change", map[string]any{"buffer": b.n, "revision": b.buf.Revision()}) // as ADR 1792076688 §4.5: {buffer, revision}
		b.luaRev = b.buf.Revision()                                                      // after the handlers: their own edits are in it
	})
}
