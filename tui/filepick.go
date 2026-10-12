package tui

import (
	"context"
	"path"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE FILE PICKERS — File › Open (FileOpen.qml) and the draft's name (FileName.qml): each a
// pick[string] over the workspace's files, previewed through the daemon.

// The open picker: a filter over the workspace's files.

// newOpenPick is File › Open's picker over the workspace's files.
func (h *Host) newOpenPick() *pick[string] {
	return &pick[string]{c: core{h}, dialog: "fileOpen", name: "open",
		list:   newRows(tuidecl.NewListModel("key", "path"), pathRowOf),
		field:  "App.openField",
		status: "App.pickerStatus", noun: "files",
		match:   func(p string) string { return p },
		preview: func(p string) func(context.Context) answerOf[pane] { return h.readPreview(p, 0, false) },
		scope:   TicketWorkspace,
		choose:  h.openPath}
}

// openPicker opens File › Open over the workspace's files.
func (h *Host) openPicker() {
	h.openPick.setAll(h.files.Paths)
	h.openPick.open()
}

// pathRowOf is a file's row in a list of paths.
func pathRowOf(_ int, p string) rowOf { return rowOf{"key": p, "path": p} }

// The new-file picker: a path, over the workspace's files to take a folder from.

// newNewPick is the draft's name: the path typed, over the workspace's files under it. Enter on a
// file takes its folder; accept creates the file at the path.
func (h *Host) newNewPick() *pick[string] {
	return &pick[string]{c: core{h}, dialog: "fileName", name: "new",
		list:    newRows(tuidecl.NewListModel("key", "path"), pathRowOf),
		match:   func(p string) string { return p },
		preview: func(p string) func(context.Context) answerOf[pane] { return h.readPreview(p, 0, false) },
		scope:   TicketWorkspace,
		enter:   h.newFileFolder,
		submit:  h.createFile}
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
