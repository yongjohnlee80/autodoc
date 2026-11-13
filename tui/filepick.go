package tui

import (
	"context"
	"path"
	"strings"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE FILE PICKERS — File › Open (FileOpen.qml) and the draft's name (FileName.qml): each a
// pick[string] over the workspace's files, previewed through the daemon.

// The open picker: a filter over the workspace's files.

// newOpenPick is File › Open's picker over the workspace's files.
func (h *Host) newOpenPick() *feature.Pick[string] {
	return &feature.Pick[string]{Core: core{h}, Dialog: "fileOpen", Name: "open",
		List:   feature.NewRows(tuidecl.NewListModel("key", "path"), pathRowOf),
		Field:  "App.openField",
		Status: "App.pickerStatus", Noun: "files",
		Match:   func(p string) string { return p },
		Preview: func(p string) func(context.Context) feature.AnswerOf[feature.Pane] { return h.readPreview(p, 0, false) },
		Scope:   feature.TicketWorkspace,
		Choose: func(p string) {
			newBuffer := h.openNewBuffer
			h.openNewBuffer = false
			h.openRefWith(feature.FileRef{WS: h.ws, Path: p}, true, nil, nil, newBuffer)
		}}
}

// openPicker opens File › Open over the workspace's files.
func (h *Host) openPicker() {
	h.openNewBuffer = false
	h.openPick.SetAll(h.files.Paths)
	h.openPick.Open()
}

// openPickerNewBuffer is File › Open with the file chosen opened in a buffer of its own, even when
// the buffer shown is empty (a tab's Open file in a new buffer…): an open file is shown where it is.
func (h *Host) openPickerNewBuffer() {
	h.openPicker()
	h.openNewBuffer = true
}

// pathRowOf is a file's row in a list of paths.
func pathRowOf(_ int, p string) feature.RowOf { return feature.RowOf{"key": p, "path": p} }

// The new-file picker: a path, over the workspace's files to take a folder from.

// newNewPick is the draft's name: the path typed, over the workspace's files under it. Enter on a
// file takes its folder; accept creates the file at the path.
func (h *Host) newNewPick() *feature.Pick[string] {
	return &feature.Pick[string]{Core: core{h}, Dialog: "fileName", Name: "new",
		List:    feature.NewRows(tuidecl.NewListModel("key", "path"), pathRowOf),
		Match:   func(p string) string { return p },
		Preview: func(p string) func(context.Context) feature.AnswerOf[feature.Pane] { return h.readPreview(p, 0, false) },
		Scope:   feature.TicketWorkspace,
		Enter:   h.newFileFolder,
		Submit:  h.createFile}
}

// newFileFolder writes file p's folder into the path, for a name to follow.
func (h *Host) newFileFolder(p string) {
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	} else {
		dir += "/"
	}
	h.set("App.newFilePath", "\x00") // moved away first, so the same folder twice still writes it
	h.set("App.newFilePath", dir)
}

// readPreview is the work that reads file p of the workspace for a picker's preview, the cursor at
// byte at: through the daemon, off the loop. held is a hit of a document another backend's build
// indexed, which says so when this one cannot read it.
func (h *Host) readPreview(p string, at int, held bool) func(context.Context) feature.AnswerOf[feature.Pane] {
	ws := h.ws
	heldWhy := h.tr("autodoc.text.pick.held_this_backend_s_build_does_not_read_this_fil") // on the loop
	return func(ctx context.Context) feature.AnswerOf[feature.Pane] {
		res, err := h.call(ctx, "doc.read", ws, p)
		if err != nil {
			why := feature.WireMessage(err)
			if held {
				// a held hit this backend cannot read (a PDF under a community build): say why
				why = strings.ReplaceAll(heldWhy, "%1", why)
			}
			return feature.Answered(feature.Pane{Title: p + " · " + why}, err)
		}
		b, _ := feature.AsMap(res)["content"].([]byte)
		return feature.AnswerOf[feature.Pane]{Value: feature.Pane{Title: p, Text: string(b), At: at}}
	}
}
