package tui

import (
	"context"
	"path/filepath"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// FILES ANYWHERE — File › Open file… (SPC f) and File › New file… (Ctrl+N, SPC n), the desktop's
// file dialogs over the local disk (ADR 1791430651 §4.3). A file chosen is asked of file.locate
// first: inside a workspace's root it opens as that workspace's file, entering the workspace;
// elsewhere it opens outside every workspace (files.go). Open document… (Ctrl+O, SPC o) stays the
// fast picker over the workspace in use.

// errRemote is what a file.* refusal says: the daemon serves another machine's disk, so the files
// of this one are not its to open.
var errRemote = &golibrpc.Error{Code: golibrpc.CodeAccessDenied, Message: "local files only: this AutoDoc is remote"}

// localOnly is err, or errRemote when the daemon refused a file.* verb to this peer.
func localOnly(err error) error {
	if code(err) == golibrpc.CodeAccessDenied {
		return errRemote
	}
	return err
}

// openFileDialog opens the file dialog at the browse folder.
func (h *Host) openFileDialog() {
	h.set("App.browseFolder", h.browseFolder())
	h.open("openAnyFile")
}

// newFile opens the save dialog for a new file's path, asking first over unsaved changes.
func (h *Host) newFile() {
	h.guard("start a new file", func() {
		h.draft = nil
		h.set("App.browseFolder", h.browseFolder())
		h.open("newAnyFile")
	})
}

// browseFolder is where the file dialogs start: the open file's folder, else the workspace's
// root, else home.
func (h *Host) browseFolder() string {
	if full := h.diskPath(); full != "" {
		return filepath.Dir(full)
	}
	if root := h.rootOf(h.ws); root != "" {
		return root
	}
	return homeDir()
}

// diskPath is the open file's absolute path on disk; "" when none is open, or its workspace's
// root is not known yet.
func (h *Host) diskPath() string {
	if !h.file.open {
		return ""
	}
	return h.pathOnDisk(h.file.ws, h.file.path)
}

// pathOnDisk is p of workspace ws on this machine's disk: p itself outside every workspace (""),
// else under the workspace's folder; "" until the workspace is listed.
func (h *Host) pathOnDisk(ws, p string) string {
	if ws == "" {
		return p
	}
	root := h.rootOf(ws)
	if root == "" {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(p))
}

// rootOf is workspace ws's folder, "" until it is listed.
func (h *Host) rootOf(ws string) string {
	for _, w := range h.wsList {
		if w.name == ws {
			return w.root
		}
	}
	return ""
}

// located is file.locate's answer: the workspace that indexes a path, and its path there; ws ""
// is none.
type located struct {
	ws, path string
	err      error
}

// locate asks the daemon which workspace indexes abs.
func (h *Host) locate(ctx context.Context, abs string) located {
	res, err := h.call(ctx, "file.locate", abs)
	if err != nil {
		return located{err: localOnly(err)}
	}
	m := asMap(res)
	return located{ws: str(m, "workspace"), path: str(m, "path")}
}

// openAbsolute opens the file at abs, chosen in the file dialog: as its workspace's file when one
// indexes it, else outside every workspace.
func (h *Host) openAbsolute(abs string) {
	h.closeDialog("openAnyFile")
	if abs == "" {
		return
	}
	abs = filepath.Clean(abs)
	ep := h.epoch
	do(h, func(ctx context.Context) located { return h.locate(ctx, abs) }, func(l located) {
		if ep != h.epoch {
			return
		}
		switch {
		case l.err != nil:
			h.failed("open "+abs, l.err)
		case l.ws != "":
			h.openIn(l.ws, l.path)
		default:
			h.openOutside(abs)
		}
	})
}

// openOutside opens the absolute path abs, a file outside every workspace, asking first over
// unsaved changes.
func (h *Host) openOutside(abs string) { h.openRef(fileRef{"", abs}, true, nil) }

// createAbsolute creates an empty file at abs (".md" added when it has no extension), chosen in
// the save dialog, and opens it: in its workspace when one would index it, else outside every
// workspace. A path that exists is not touched.
func (h *Host) createAbsolute(abs string) {
	h.closeDialog("newAnyFile")
	if abs == "" {
		return
	}
	abs = filepath.Clean(abs)
	if filepath.Ext(abs) == "" {
		abs += ".md"
	}
	ep := h.epoch
	type answer struct {
		at  located
		err error
	}
	do(h, func(ctx context.Context) answer {
		at := h.locate(ctx, abs)
		if at.err != nil {
			return answer{err: at.err}
		}
		ws, p := at.ws, at.path
		if ws == "" {
			p = abs
		}
		_, err := h.writeFile(ctx, ws, p, []byte{}, "", false)
		return answer{at: at, err: err}
	}, func(a answer) {
		if ep != h.epoch {
			return
		}
		switch {
		case code(a.err) == rpc.CodeConflict:
			h.notify(abs + " exists: File › Open file… opens it")
			return
		case a.err != nil && code(a.err) != rpc.CodeCommitted:
			h.failed("create "+abs, a.err)
			return
		}
		if a.at.ws == "" {
			h.openOutside(abs)
			return
		}
		h.openIn(a.at.ws, a.at.path)
		if a.at.ws == h.ws {
			h.listFiles()
		}
		h.loadWorkspaces() // the explorer lists it
	})
}
