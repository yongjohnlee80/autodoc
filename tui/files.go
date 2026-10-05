package tui

import (
	"context"
	"fmt"
	"path"
	"strings"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	tuicore "github.com/yongjohnlee80/golib/tui"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE NOTE IN THE EDITOR — opening, saving, and what is asked on the way.
//
// UNSAVED WORK IS NEVER LOST QUIETLY. Opening another file, switching workspace, reloading or
// quitting over unsaved edits asks first (save, discard, or stay), with Save focused. A save is
// written only over the version the file was read at (doc.write's condition), so a file that
// changed on disk since is never overwritten without asking: the conflict dialog offers keep,
// reload or overwrite.
//
// A load is off the loop, and applied only if it is still the latest open under the same workspace
// and connection: a slow load cannot replace a later one.
//
// A DERIVED DOCUMENT IS READ, NEVER WRITTEN. A PDF or a DOCX the daemon's build derives opens as
// its derived text in a read-only editor, badged with its kind ("[PDF · read-only]"): motions,
// find, copy and the outline work, edits and saves do not. SPC O (File › Open in System Viewer)
// opens the original in the desktop's own viewer.
//
// THE PAGE IS ALWAYS WRITABLE. With no file open, what is typed is an untitled draft: unsaved work
// like a file's, guarded the same way; saving it asks for its path in the new-file picker, creates
// the file with the draft's text, and opens it there, the cursor where it was.

type openedFile struct {
	path    string
	version string // the version the editor's text was read or written at
	open    bool
	dirty   bool
	gen     uint64 // numbers the opens; the latest wins
	// derived is the kind of a derived document ("PDF"), open read-only; "" for a file of text
	derived string
	// then is what the unsaved question guards: run after save or discard, dropped by stay.
	then func()
}

// untitled is the draft's name wherever a file's path would show.
const untitled = "untitled"

// name is the file's path, or untitled for the draft.
func (n openedFile) name() string {
	if n.open {
		return n.path
	}
	return untitled
}

// title is the file's name as the page and the status line show it: a derived document's carries
// its read-only badge.
func (n openedFile) title() string {
	if n.open && n.derived != "" {
		return n.path + "  [" + n.derived + " · read-only]"
	}
	return n.name()
}

// guard runs then, asking first when the file has unsaved changes.
func (h *Host) guard(action string, then func()) {
	if !h.file.dirty {
		then()
		return
	}
	h.file.then = then
	h.set("App.unsavedQuestion", fmt.Sprintf("%s has unsaved changes. Save them before you %s?", h.file.name(), action))
	h.open("unsavedFile")
}

// unsaved answers the unsaved question.
func (h *Host) unsaved(answer string) {
	then := h.file.then
	h.file.then = nil
	h.closeDialog("unsavedFile")
	switch answer {
	case "save":
		if !h.file.open {
			h.nameDraft(then) // the draft has no path yet: name it, then go on
			return
		}
		h.write(h.editor.Value(), h.file.version, then)
	case "discard":
		h.setDirty(false)
		if then != nil {
			then()
		}
	}
}

// openPath opens a file, asking first over unsaved changes.
func (h *Host) openPath(p string) {
	if p == h.file.path && h.file.open && !h.file.dirty {
		h.keep(h.p.Call("editor", "forceActiveFocus"))
		return
	}
	h.guard("open "+p, func() { h.load(p) })
}

// reload reads the file again from disk, asking first over unsaved changes.
func (h *Host) reload() {
	if !h.file.open {
		return
	}
	h.guard("reload it", func() { h.load(h.file.path) })
}

// load reads p into the editor. Reading the open file again yields to a save that lands while it is
// read: the page is then what the disk holds, and the read is older.
func (h *Host) load(p string) {
	h.file.gen++
	gen, ep, ws := h.file.gen, h.epoch, h.ws
	again, read := h.file.open && h.file.path == p, h.file.version
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
		if gen != h.file.gen || ep != h.epoch {
			return
		}
		if again && h.file.version != read {
			h.say("kept " + p + " as it was saved while it was read")
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

// show puts a file's text in the editor, clean.
func (h *Host) show(p, content, version string) {
	h.editor.SetValue(content) // reports no textChanged: only typing does
	h.syncPageWidth()
	if at := h.openAt; at >= 0 {
		// opened from a search hit: the cursor at its section
		h.openAt = -1
		h.editor.SetCursorPosition(cursorAt(content, min(at, len(content))))
	}
	h.file.path, h.file.version, h.file.open, h.file.dirty = p, version, true, false
	h.readPage()
	h.backlinks.Reset(nil)
	h.loadBacklinks(p)
}

// readPage decides how the open page reads its file, the one place that does: on every open, and
// again whenever the daemon's registrations arrive, which a read may have beaten.
//   - A Pro format's file is a derived document, read-only and badged, whatever the registrations
//     say or whether they have come: no build reads one but by its deriver, and none writes one.
//   - Its highlighting, outline and frontmatter check follow the daemon's kinds as last known: a
//     registered file's change from Markdown to plain text when they come.
//
// Unsaved text stays as it is: only how it is read changes.
func (h *Host) readPage() {
	p := h.file.path
	h.file.derived = ""
	if kind.Of(p, nil) == kind.Pro {
		h.file.derived = kind.Label(p)
	}
	h.editor.SetReadOnly(h.file.derived != "")
	switch h.kinds.Of(p, h.textExtensions()) { // the daemon's registrations: it is the one indexing
	case kind.Text, kind.Registered:
		h.set("App.syntaxDefinition", "Plain text (find)")
	case kind.YAML:
		h.set("App.syntaxDefinition", "YAML (find)")
	default:
		h.set("App.syntaxDefinition", "Markdown (find)")
	}
	h.setDirty(h.file.dirty) // the status line's title carries the badge
	h.validateSoon()
	h.refreshOutline() // and the frame's
}

// recheckFile checks the open file against the disk on a new connection, which may have missed a
// change: kept as it is at the version it was read at; read again, the cursor where it was, when
// it changed and has no unsaved changes (one that has finds out at its save, as a conflict); when it
// is gone, closed, after asking over unsaved changes, whose save writes it anew. A save that lands
// while it reads moves the page's version on, and the read, older, is dropped.
func (h *Host) recheckFile() {
	gen, ep, ws, p, read := h.file.gen, h.epoch, h.ws, h.file.path, h.file.version
	type answer struct {
		content, version string
		err              error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "doc.read", ws, p)
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		b, _ := m["content"].([]byte)
		return answer{content: string(b), version: str(m, "version")}
	}, func(a answer) {
		if gen != h.file.gen || ep != h.epoch || !h.file.open || h.file.version != read {
			return
		}
		switch {
		case code(a.err) == rpc.CodeNotFound, code(a.err) == golibrpc.CodeInvalidParams:
			if !h.file.dirty {
				h.closeFile()
				h.notify(p + " is gone from the workspace: closed")
				return
			}
			h.file.version = "" // gone: a save writes it anew
			h.guard("close it: it is gone from the workspace", h.closeFile)
		case a.err != nil:
			h.failed("check "+p, a.err)
		case a.version == h.file.version:
		case h.file.dirty:
			h.notify(p + " changed on disk while the backend was away: a save asks before it overwrites it")
		default:
			row, col := h.editor.Line()
			h.show(p, a.content, a.version)
			h.editor.SetLine(row, col)
			h.notify("read " + p + " again: it changed on disk while the backend was away")
		}
	})
}

// closeFile empties the editor: no file is open, and the page is a new draft.
func (h *Host) closeFile() {
	h.file.gen++
	h.editor.SetValue("")
	h.syncPageWidth()
	h.file = openedFile{gen: h.file.gen}
	h.editor.SetReadOnly(false)
	h.outline, h.outlineRows = nil, nil
	h.outlineGen++
	h.set("App.fileTitle", untitled)
	h.set("App.syntaxDefinition", "Markdown (find)")
	h.set("App.statusCenter", "")
	h.backlinks.Reset(nil)
	h.set("App.linksTitle", "backlinks")
	h.clearDiagnostics()
}

// edited is the editor's text changing: typed, so the file (or the draft) has unsaved changes.
func (h *Host) edited() {
	h.setDirty(true)
	h.validateSoon()
	h.outlineSoon()
	h.syncPageWidth() // a line count with another number of digits widens the gutter
}

func (h *Host) setDirty(v bool) {
	h.file.dirty = v
	mark := ""
	if v {
		mark = " [+]"
	}
	if h.file.open || v {
		h.set("App.statusCenter", h.file.title()+mark)
	} else {
		h.set("App.statusCenter", "")
	}
}

// syncMode brings the status line's mode up to date with the editor's.
func (h *Host) syncMode() { h.setWhere(h.where) }

// save writes the file at the version it was read at; the draft is named first.
func (h *Host) save() {
	if !h.file.open {
		h.nameDraft(nil)
		return
	}
	if h.file.derived != "" {
		h.notify(h.file.path + " is read-only: its text is derived from the " + h.file.derived + "; SPC O opens the original")
		return
	}
	h.write(h.editor.Value(), h.file.version, nil)
}

// nameDraft opens the new-file picker to save the draft under a path; then runs once it is saved
// (a guarded open or switch), and is dropped when the picker is closed without one.
func (h *Host) nameDraft(then func()) {
	h.draft = &draftSave{then: then}
	h.set("App.fileNameError", draftHelp)
	h.setField("App.newFilePath", "")
	h.newFileFilter("")
	h.open("fileName")
}

// draftSave is a draft being named: what runs once it is saved.
type draftSave struct{ then func() }

// draftHelp is the new-file picker's line when it names the draft.
const draftHelp = "save the draft: a path in the workspace; .md is added when it has none · Enter on a file takes its folder"

// cursorBytes is the editor's cursor as a byte offset of its text, for the file the draft becomes.
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
	gen, ep, ws, p := h.file.gen, h.epoch, h.ws, h.file.path
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
		if gen != h.file.gen || ep != h.epoch {
			return
		}
		switch {
		case a.err == nil, code(a.err) == rpc.CodeCommitted && a.readErr == nil && a.same:
			h.file.version = a.version
			// typing during the save leaves the file unsaved: what is on disk is what was written
			newer := h.editor.Value() != content
			h.setDirty(newer)
			h.notify("saved " + p)
			switch {
			case after == nil:
			case newer:
				// what the save guarded (an open, a switch, a quit) would drop the newer edit: ask again
				h.file.then = after
				h.set("App.unsavedQuestion", fmt.Sprintf("%s changed again while it was saved. Save the newer changes first?", p))
				h.open("unsavedFile")
			default:
				after()
			}
		case code(a.err) == rpc.CodeCommitted && a.readErr != nil:
			h.notify("saved " + p + ", but it could not be read back: " + wireMessage(a.readErr) + " — reload before saving again")
		case code(a.err) == rpc.CodeConflict, code(a.err) == rpc.CodeCommitted:
			h.set("App.conflictQuestion", fmt.Sprintf("%s changed on disk since you opened it. Keep editing, reload the disk's version (your changes are lost), or overwrite it with yours?", p))
			h.open("fileConflict")
		case code(a.err) == rpc.CodeNotFound:
			h.notify(p + " is gone from disk: File › New file to write it again")
		default:
			h.failed("save "+p, a.err)
		}
	})
}

// conflict answers the conflict dialog.
func (h *Host) conflict(answer string) {
	h.closeDialog("fileConflict")
	switch answer {
	case "reload":
		// the file stays unsaved until the disk's version is in the editor: a failed read keeps the
		// edits guarded
		h.load(h.file.path)
	case "overwrite":
		h.overwrite()
	default:
		h.notify("kept your changes; the disk's version is newer")
	}
}

// overwrite writes the editor's text over whatever version is on disk now: it reads the version
// first, so the write is still conditional (a third writer in between is still a conflict).
func (h *Host) overwrite() {
	gen, ep, ws, p := h.file.gen, h.epoch, h.ws, h.file.path
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
		if gen != h.file.gen || ep != h.epoch {
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

// newFileHelp is the new-file picker's line under its path.
const newFileHelp = "a path in the workspace; .md is added when it has none · Enter on a file takes its folder"

// newFile asks for a new file's path, asking first over unsaved changes.
func (h *Host) newFile() {
	h.guard("start a new file", func() {
		h.draft = nil
		h.set("App.fileNameError", newFileHelp)
		h.setField("App.newFilePath", "")
		h.newFileFilter("")
		h.open("fileName")
	})
}

// createFile creates a file at name (".md" added when it has no extension) and opens it, closing
// the picker (Enter in its path field is not its Create button): empty, or holding the draft when it
// is the draft being named. A path that exists, or is not a file, asks again with the reason.
func (h *Host) createFile(name string) {
	h.closeDialog("fileName")
	name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	if name == "" {
		h.set("App.fileNameError", "a name is required")
		h.open("fileName")
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
	ep, ws, gen := h.epoch, h.ws, h.file.gen
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
			h.set("App.fileNameError", reason)
			h.open("fileName")
			return
		}
		h.draft = nil
		if draft != nil {
			if gen != h.file.gen || h.file.open {
				// the page moved on while the draft was written: the file is on disk; open it only
				// when asked
				h.notify("saved the draft as " + name)
				return
			}
			h.openAt = h.cursorBytes()
			h.setDirty(false) // written: the load below finds it saved
		}
		h.load(name)
		h.listFiles()
		h.loadWorkspaces() // the explorer lists it
		if draft != nil && draft.then != nil {
			draft.then()
		}
	})
}

// quit quits, asking first over unsaved changes.
func (h *Host) quit() {
	if h.file.dirty {
		h.open("confirmQuit")
		return
	}
	h.p.Quit()
}
