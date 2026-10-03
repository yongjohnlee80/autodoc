package tui

import (
	"context"
	"fmt"
	"path"
	"strings"

	tuicore "github.com/yongjohnlee80/golib/tui"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE NOTE IN THE EDITOR — opening, saving, and what is asked on the way.
//
// UNSAVED WORK IS NEVER LOST QUIETLY. Opening another note, switching workspace, reloading or
// quitting over unsaved edits asks first (save, discard, or stay), with Save focused. A save is
// written only over the version the note was read at (doc.write's condition), so a note that
// changed on disk since is never overwritten without asking: the conflict dialog offers keep,
// reload or overwrite.
//
// A load is off the loop, and applied only if it is still the latest open under the same workspace
// and connection: a slow load cannot replace a later one.
//
// THE PAGE IS ALWAYS WRITABLE. With no note open, what is typed is an untitled draft: unsaved work
// like a note's, guarded the same way; saving it asks for its path in the new-note picker, creates
// the note with the draft's text, and opens it there, the cursor where it was.

type note struct {
	path    string
	version string // the version the editor's text was read or written at
	open    bool
	dirty   bool
	gen     uint64 // numbers the opens; the latest wins
	// then is what the unsaved question guards: run after save or discard, dropped by stay.
	then func()
}

// untitled is the draft's name wherever a note's path would show.
const untitled = "untitled"

// name is the note's path, or untitled for the draft.
func (n note) name() string {
	if n.open {
		return n.path
	}
	return untitled
}

// guard runs then, asking first when the note has unsaved changes.
func (h *Host) guard(action string, then func()) {
	if !h.note.dirty {
		then()
		return
	}
	h.note.then = then
	h.set("App.unsavedQuestion", fmt.Sprintf("%s has unsaved changes. Save them before you %s?", h.note.name(), action))
	h.open("unsavedNote")
}

// unsaved answers the unsaved question.
func (h *Host) unsaved(answer string) {
	then := h.note.then
	h.note.then = nil
	h.closeDialog("unsavedNote")
	switch answer {
	case "save":
		if !h.note.open {
			h.nameDraft(then) // the draft has no path yet: name it, then go on
			return
		}
		h.write(h.editor.Value(), h.note.version, then)
	case "discard":
		h.setDirty(false)
		if then != nil {
			then()
		}
	}
}

// openPath opens a note, asking first over unsaved changes.
func (h *Host) openPath(p string) {
	if p == h.note.path && h.note.open && !h.note.dirty {
		h.keep(h.p.Call("editor", "forceActiveFocus"))
		return
	}
	h.guard("open "+p, func() { h.load(p) })
}

// reload reads the note again from disk, asking first over unsaved changes.
func (h *Host) reload() {
	if !h.note.open {
		return
	}
	h.guard("reload it", func() { h.load(h.note.path) })
}

// load reads p into the editor.
func (h *Host) load(p string) {
	h.note.gen++
	gen, ep, ws := h.note.gen, h.epoch, h.ws
	type answer struct {
		content, version string
		err              error
	}
	h.say("opening " + p + "…")
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "doc.read", ws, p)
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		b, _ := m["content"].([]byte)
		return answer{content: string(b), version: str(m, "version")}
	}, func(a answer) {
		if gen != h.note.gen || ep != h.epoch {
			return
		}
		if a.err != nil {
			h.failed("open "+p, a.err)
			return
		}
		h.show(p, a.content, a.version)
		h.say("opened " + p)
		h.keep(h.p.Call("editor", "forceActiveFocus"))
	})
}

// show puts a note's text in the editor, clean.
func (h *Host) show(p, content, version string) {
	h.editor.SetValue(content) // reports no textChanged: only typing does
	h.syncPageWidth()
	if at := h.openAt; at >= 0 {
		// opened from a search hit: the cursor at its section
		h.openAt = -1
		h.editor.SetCursorPosition(cursorAt(content, min(at, len(content))))
	}
	h.note.path, h.note.version, h.note.open = p, version, true
	h.set("App.noteTitle", p)
	switch kind.Of(p, h.textExtensions()) {
	case kind.Text:
		h.set("App.syntaxDefinition", "Plain text (find)")
	case kind.YAML:
		h.set("App.syntaxDefinition", "YAML (find)")
	default:
		h.set("App.syntaxDefinition", "Markdown (find)")
	}
	h.setDirty(false)
	h.validateSoon()
	h.backlinks.Reset(nil)
	h.loadBacklinks(p)
}

// closeNote empties the editor: no note is open, and the page is a new draft.
func (h *Host) closeNote() {
	h.note.gen++
	h.editor.SetValue("")
	h.syncPageWidth()
	h.note = note{gen: h.note.gen}
	h.set("App.noteTitle", untitled)
	h.set("App.syntaxDefinition", "Markdown (find)")
	h.set("App.statusCenter", "")
	h.backlinks.Reset(nil)
	h.set("App.linksTitle", "backlinks")
	h.clearDiagnostics()
}

// edited is the editor's text changing: typed, so the note (or the draft) has unsaved changes.
func (h *Host) edited() {
	h.setDirty(true)
	h.validateSoon()
	h.syncPageWidth() // a line count with another number of digits widens the gutter
}

func (h *Host) setDirty(v bool) {
	h.note.dirty = v
	mark := ""
	if v {
		mark = " [+]"
	}
	if h.note.open || v {
		h.set("App.statusCenter", h.note.name()+mark)
	} else {
		h.set("App.statusCenter", "")
	}
}

// syncMode brings the status line's mode up to date with the editor's.
func (h *Host) syncMode() { h.setWhere(h.where) }

// save writes the note at the version it was read at; the draft is named first.
func (h *Host) save() {
	if !h.note.open {
		h.nameDraft(nil)
		return
	}
	h.write(h.editor.Value(), h.note.version, nil)
}

// nameDraft opens the new-note picker to save the draft under a path; then runs once it is saved
// (a guarded open or switch), and is dropped when the picker is closed without one.
func (h *Host) nameDraft(then func()) {
	h.draft = &draftSave{then: then}
	h.set("App.noteNameError", draftHelp)
	h.setField("App.newNotePath", "")
	h.newNoteFilter("")
	h.open("noteName")
}

// draftSave is a draft being named: what runs once it is saved.
type draftSave struct{ then func() }

// draftHelp is the new-note picker's line when it names the draft.
const draftHelp = "save the draft: a path in the workspace; .md is added when it has none · Enter on a note takes its folder"

// cursorBytes is the editor's cursor as a byte offset of its text, for the note the draft becomes.
func (h *Host) cursorBytes() int {
	row, col := h.editor.Line()
	lines := h.editor.Lines()
	at := 0
	for i := 0; i < row && i < len(lines); i++ {
		at += len(lines[i]) + 1
	}
	if row < len(lines) {
		n := 0
		for c := range tuicore.Graphemes(lines[row]) {
			if n == col {
				break
			}
			at += len(c)
			n++
		}
	}
	return at
}

// write writes content over the version want, then runs after. A stale version opens the conflict
// dialog. A write that landed before a follow-up failed (Committed) is read back: when the disk
// holds what was written, its version is adopted; otherwise someone wrote after it, which is a
// conflict. It is never sent again blindly.
func (h *Host) write(content, want string, after func()) {
	gen, ep, ws, p := h.note.gen, h.epoch, h.ws, h.note.path
	type answer struct {
		version string
		err     error
		readErr error // the read-back after Committed
		same    bool  // the read-back holds what was written
	}
	h.say("saving " + p + "…")
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "doc.write", ws, p, []byte(content), want)
		if err == nil {
			return answer{version: str(asMap(res), "version")}
		}
		if code(err) != rpc.CodeCommitted {
			return answer{err: err}
		}
		back, rerr := h.call(ctx, "doc.read", ws, p)
		if rerr != nil {
			return answer{err: err, readErr: rerr}
		}
		m := asMap(back)
		b, _ := m["content"].([]byte)
		return answer{err: err, version: str(m, "version"), same: string(b) == content}
	}, func(a answer) {
		if gen != h.note.gen || ep != h.epoch {
			return
		}
		switch {
		case a.err == nil, code(a.err) == rpc.CodeCommitted && a.readErr == nil && a.same:
			h.note.version = a.version
			// typing during the save leaves the note unsaved: what is on disk is what was written
			newer := h.editor.Value() != content
			h.setDirty(newer)
			h.notify("saved " + p)
			switch {
			case after == nil:
			case newer:
				// what the save guarded (an open, a switch, a quit) would drop the newer edit: ask again
				h.note.then = after
				h.set("App.unsavedQuestion", fmt.Sprintf("%s changed again while it was saved. Save the newer changes first?", p))
				h.open("unsavedNote")
			default:
				after()
			}
		case code(a.err) == rpc.CodeCommitted && a.readErr != nil:
			h.notify("saved " + p + ", but it could not be read back: " + wireMessage(a.readErr) + " — reload before saving again")
		case code(a.err) == rpc.CodeConflict, code(a.err) == rpc.CodeCommitted:
			h.set("App.conflictQuestion", fmt.Sprintf("%s changed on disk since you opened it. Keep editing, reload the disk's version (your changes are lost), or overwrite it with yours?", p))
			h.open("noteConflict")
		case code(a.err) == rpc.CodeNotFound:
			h.notify(p + " is gone from disk: File › New note to write it again")
		default:
			h.failed("save "+p, a.err)
		}
	})
}

// conflict answers the conflict dialog.
func (h *Host) conflict(answer string) {
	h.closeDialog("noteConflict")
	switch answer {
	case "reload":
		// the note stays unsaved until the disk's version is in the editor: a failed read keeps the
		// edits guarded
		h.load(h.note.path)
	case "overwrite":
		h.overwrite()
	default:
		h.notify("kept your changes; the disk's version is newer")
	}
}

// overwrite writes the editor's text over whatever version is on disk now: it reads the version
// first, so the write is still conditional (a third writer in between is still a conflict).
func (h *Host) overwrite() {
	gen, ep, ws, p := h.note.gen, h.epoch, h.ws, h.note.path
	type answer struct {
		version string
		gone    bool
		err     error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "doc.read", ws, p)
		if code(err) == rpc.CodeNotFound {
			return answer{gone: true}
		}
		if err != nil {
			return answer{err: err}
		}
		return answer{version: str(asMap(res), "version")}
	}, func(a answer) {
		if gen != h.note.gen || ep != h.epoch {
			return
		}
		switch {
		case a.err != nil:
			h.failed("overwrite "+p, a.err)
		default:
			// gone: a create ("") writes it back
			h.write(h.editor.Value(), a.version, nil)
		}
	})
}

// newNoteHelp is the new-note picker's line under its path.
const newNoteHelp = "a path in the workspace; .md is added when it has none · Enter on a note takes its folder"

// newNote asks for a new note's path, asking first over unsaved changes.
func (h *Host) newNote() {
	h.guard("start a new note", func() {
		h.draft = nil
		h.set("App.noteNameError", newNoteHelp)
		h.setField("App.newNotePath", "")
		h.newNoteFilter("")
		h.open("noteName")
	})
}

// createNote creates a note at name (".md" added when it has no extension) and opens it, closing
// the picker (Enter in its path field is not its Create button): empty, or holding the draft when it
// is the draft being named. A path that exists, or is not a note, asks again with the reason.
func (h *Host) createNote(name string) {
	h.closeDialog("noteName")
	name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	if name == "" {
		h.set("App.noteNameError", "a name is required")
		h.open("noteName")
		return
	}
	if path.Ext(name) == "" {
		name += ".md"
	}
	draft := h.draft
	content := []byte{}
	if draft != nil {
		content = []byte(h.editor.Value())
	}
	ep, ws, gen := h.epoch, h.ws, h.note.gen
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "doc.write", ws, name, content, "")
		return err
	}, func(err error) {
		if ep != h.epoch {
			return
		}
		if err != nil && code(err) != rpc.CodeCommitted {
			reason := wireMessage(err)
			if code(err) == rpc.CodeConflict {
				reason = name + " exists: pick another name, or open it"
			}
			h.set("App.noteNameError", reason)
			h.open("noteName")
			return
		}
		h.draft = nil
		if draft != nil {
			if gen != h.note.gen || h.note.open {
				// the page moved on while the draft was written: the note is on disk; open it only
				// when asked
				h.notify("saved the draft as " + name)
				return
			}
			h.openAt = h.cursorBytes()
			h.setDirty(false) // written: the load below finds it saved
		}
		h.load(name)
		h.listNotes()
		h.loadWorkspaces() // the explorer lists it
		if draft != nil && draft.then != nil {
			draft.then()
		}
	})
}

// quit quits, asking first over unsaved changes.
func (h *Host) quit() {
	if h.note.dirty {
		h.open("confirmQuit")
		return
	}
	h.p.Quit()
}
