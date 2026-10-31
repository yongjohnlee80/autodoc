package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/fsbounds"
	"github.com/yongjohnlee80/autodoc/core/kind"
	wsfilter "github.com/yongjohnlee80/autodoc/core/workspace"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// FILES ANYWHERE — File › Open file… (SPC f) and File › New file… (Ctrl+N, SPC n), the desktop's
// file dialogs over the local disk (docs/decisions/editor.md#files-outside-every-workspace). A file
// chosen is asked of file.locate first: inside a workspace's root it opens as that workspace's
// file, entering the workspace; elsewhere it opens outside every workspace (files.go). Open
// document… (SPC o) stays the fast picker over the workspace in use.

// errRemote is what a file.* refusal says: the daemon serves another machine's disk, so the files
// of this one are not its to open.
var errRemote = &golibrpc.Error{Code: golibrpc.CodeAccessDenied, Message: "local files only: this AutoDoc is remote"}

// localOnly is err, or errRemote when the daemon refused a file.* verb to this peer. A gated
// script's call refused past its bounds is access denied too, and says so itself: it is kept.
func localOnly(err error) error {
	if code(err) == golibrpc.CodeAccessDenied && !strings.HasSuffix(wireMessage(err), fsbounds.ErrRefused.Error()) {
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
	h.draft = nil // nothing is asked: the new file opens in a buffer of its own (buffers.go)
	h.set("App.browseFolder", h.browseFolder())
	h.open("newAnyFile")
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
	for _, w := range h.workspaces.All() {
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
// indexes it, else outside every workspace. A gated script's open (file.open_path, a link it
// follows) opens only what the daemon's bounds let it read: a file inside a served workspace, not
// hidden, not AutoDoc's own (docs/decisions/contract.md#path-commands-run-within-the-workspace);
// anything else is refused, and nothing opens.
func (h *Host) openAbsolute(abs string) { h.openAbsoluteAs(h.authorityNow(), abs) }

// openAbsoluteAs is openAbsolute as a acts: for a continuation, the authority its invocation began
// with.
func (h *Host) openAbsoluteAs(a authority, abs string) {
	h.closeDialog("openAnyFile")
	if abs == "" {
		return
	}
	abs = filepath.Clean(abs)
	tk := h.ticket(nil, TicketWorkspace)
	do(h, func(ctx context.Context) located {
		if a.gated() {
			res, err := h.call(withAuthority(ctx, a), "file.locate", abs)
			if err != nil {
				return located{err: localOnly(err)}
			}
			if res == nil {
				return located{err: fmt.Errorf("%s is outside the workspace: a script may not open it", abs)}
			}
		}
		return h.locate(ctx, abs)
	}, func(l located) {
		if !tk.Current() {
			return
		}
		switch {
		case l.err != nil:
			h.failed("open "+abs, l.err)
		case l.ws != "":
			h.openIn(l.ws, l.path)
		default:
			// said once the open goes ahead: over unsaved changes, Stay opens nothing to explain
			h.openRef(fileRef{"", abs}, true, func() { h.sayWhyOutside(abs) })
		}
	})
}

// whyOutside is why abs, under a workspace's root, is in no workspace
// (docs/decisions/todos.md#excluded-todo-stores-are-reported): the workspace whose root holds it,
// and the exclude pattern that keeps it out, or "" when its include patterns admit no such file. ok
// is false when no workspace's root holds it.
func whyOutside(list []wsInfo, abs string) (w wsInfo, pattern string, ok bool) {
	for _, x := range list {
		rel, err := filepath.Rel(x.root, abs)
		if x.root == "" || err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		rel = filepath.ToSlash(rel)
		if ok && len(x.root) <= len(w.root) {
			continue // the innermost root holding it
		}
		w, pattern, ok = x, "", true
		for _, p := range x.exclude {
			if wsfilter.NewMatcher(nil, []string{p}).Excluded(rel) {
				pattern = p
				break
			}
		}
	}
	return w, pattern, ok
}

// sayWhyOutside says why a file opened outside every workspace is not its workspace's when a
// workspace's root holds it and an exclude pattern keeps it out, and offers that workspace's
// settings. A file its include patterns do not admit (a type the workspace does not index) is
// opened without a word.
func (h *Host) sayWhyOutside(abs string) {
	w, pattern, ok := whyOutside(h.workspaces.All(), abs)
	if !ok || pattern == "" {
		return
	}
	h.ask(Question{Title: h.trf("autodoc.question.outside.in_no_workspace_open_s_settings", w.name),
		Text: h.trf("autodoc.question.outside.is_under_workspace_s_root_but_its_exclude_patter", abs, w.name, pattern), Yes: h.tr("autodoc.question.outside.open_the_settings"), No: h.tr("autodoc.question.outside.not_now"),
		OnYes: func() { h.openSettings(w.name, 0) }})
}

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
	tk := h.ticket(nil, TicketWorkspace)
	raw := kind.RawText(abs)
	do(h, func(ctx context.Context) created {
		return h.writeNew(ctx, "", abs, "", []byte{}) // any path: File › New file…'s own rules
	}, func(a created) {
		if !tk.Current() {
			return
		}
		switch {
		case code(a.err) == rpc.CodeConflict:
			h.notify(h.trf("autodoc.notice.outside.exists_file_open_file_opens_it", abs))
			return
		case a.err != nil:
			h.failed("create "+abs, a.err)
			return
		}
		h.openRefAs(fileRef{a.at.ws, a.at.path}, true, nil, &raw, nil, false)
		if a.at.ws == "" {
			return
		}
		if a.at.ws == h.ws {
			h.listFiles()
		}
		h.loadWorkspaces() // the explorer lists it
	})
}
