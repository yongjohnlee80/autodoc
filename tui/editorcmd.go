package tui

import (
	"errors"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE COMMAND LINE — the editor's own ':' prompt (golib's VIM Commands), with AutoDoc's half:
//
//	w, wq, x   the editor's writeRequested(quit): writeFromEditor
//	q, q!      the editor's quitRequested(force): quitFromEditor
//
// and AutoDoc's own commands composed into the editor's from the command catalog (catalog.go): its
// own (editorEntries), each feature's, and the Lua ones (script.go).

// writeFromEditor saves, as File › Save does, and quits once the file is written when quit (wq, x).
// An untitled draft asks for its path first; a quit then waits for it, and is dropped if the
// picker is closed.
func (h *Host) writeFromEditor(quit bool) {
	var then func()
	if quit {
		then = h.p.Quit
	}
	switch {
	case !h.file.open:
		h.nameDraft(then)
	case h.file.derived != "":
		h.save() // says why it is read-only
	default:
		h.write(h.core.Value(), h.file.version, then)
	}
}

// quitFromEditor is q, asking over unsaved changes as every quit does, and q!, which discards them.
func (h *Host) quitFromEditor(force bool) {
	if force {
		// discarded on purpose: nothing to keep, and a copy kept earlier goes too
		h.settleRecovery(h.file.ws, map[bool]string{true: h.file.path, false: ""}[h.file.open])
		h.setDirty(false)
		h.p.Quit()
		return
	}
	h.quit()
}

// openCommandLine is Ctrl+; and Search › Command line…: the editor's ':' prompt in every editor
// mode, so the Text mode's users reach the commands, their own Lua ones among them.
func (h *Host) openCommandLine() {
	h.keep(h.p.Call("editor", "forceActiveFocus"))
	h.keep(h.p.Call("editor", "openCommandLine"))
}

// editorEntries are AutoDoc's verbs of the command line, as catalog entries (catalog.go): find and
// nohl are the page's find (marked words, "finding …", n and N, as / is), so the prompt and / share
// one find, and replace golib's own.
func (h *Host) editorEntries() []Entry {
	return []Entry{
		{ID: "find.term", Title: "find a term in the page", Verb: "find", Args: "<term>",
			Help: "find term in the page; n and N go to the next and previous",
			Run: func(x Invocation) error {
				if x.Line == "" {
					return errors.New("find: what? find <term>")
				}
				h.findInPage(x.Line)
				return nil
			}},
		{ID: "find.clear", Title: "clear the find", Verb: "nohl", Help: "clear the find", Run: func(Invocation) error {
			h.core.SetFind("", widget.FindOptions{})
			h.clearFind()
			return nil
		}},
	}
}

// findInPage starts the page's find for pattern, as / then Enter does, and goes to the first match
// from the cursor. The editor's own active find (replace leaves one) is cleared so n and N are the
// page find's.
func (h *Host) findInPage(pattern string) {
	h.core.SetFind("", widget.FindOptions{})
	h.find.pending = findPage
	h.startFind(pattern)
}
