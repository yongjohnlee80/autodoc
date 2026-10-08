package tui

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// AN HTML FILE OPENED RAW (ADR 1791474356): its own bytes in the editor, editable and saved through
// the *_raw verbs. Under --gui the editor's Rendered view is the page itself: golib's HTMLView,
// declared in main.qml as the Editor's HTMLDocumentView and turned on here for the file, off for any
// other. Ctrl+T flips between the source and the page; the page's links open as the preview
// pane's do (htmlLink), and its images load from the file's folder.

// documentSwitch is an editor whose Rendered view can be its document view: golib's gui Editor.
type documentSwitch interface{ SetRenderedDocument(on bool) }

// documentComponent is an editor with a document view, reached as a component: the gui Editor's
// HTMLView, named without a gui type.
type documentComponent interface{ DocumentComponent() tuicore.Component }

// pageView is the editor's document view, an HTMLView; false for an editor without one (a terminal's).
func (h *Host) pageView() (*widget.HTMLView, bool) {
	dc, ok := h.editor.(documentComponent)
	if !ok {
		return nil, false
	}
	v, ok := dc.DocumentComponent().(*widget.HTMLView)
	return v, ok
}

// documentFor turns the page on for a raw HTML file under --gui and off for anything else, before
// the editor is given the file's text, and points its images at the file's folder when that
// changes.
func (h *Host) documentFor(ws, p string, raw bool) {
	h.refusedNoted = false // another document: what its page refuses is said again
	on := raw && h.native()
	switch {
	case h.docSwitch != nil:
		h.docSwitch(on)
	default:
		if ds, ok := h.editor.(documentSwitch); ok {
			ds.SetRenderedDocument(on)
		}
	}
	if !on {
		return
	}
	v, ok := h.pageView()
	if !ok {
		return
	}
	if root, base := h.resourcesFor(ws, p); root+"\x00"+base != h.pageDir {
		h.pageDir = root + "\x00" + base
		v.SetImageResolver(widget.DirResources(root, base))
	}
}

// resourcesFor is where a page's stylesheets and images are read from (ADR 1791488696 §4.1): a
// workspace file's from the workspace's folder, its paths read against its own folder, so a corpus
// reaches its shared ../assets; a file outside every workspace's from its own folder alone, where
// ../ is refused. Adding a folder as a workspace is how a user lets its pages reach above
// themselves.
func (h *Host) resourcesFor(ws, p string) (root, base string) {
	if ws == "" {
		return filepath.Dir(p), ""
	}
	root = h.rootOf(ws)
	if base = path.Dir(p); base == "." {
		base = ""
	}
	return root, base
}

// resourceRefused is a page's report of a stylesheet or image its resolver refused: said once per
// document, saying why.
func (h *Host) resourceRefused(src string) {
	if h.refusedNoted {
		return
	}
	h.refusedNoted = true
	h.notify(refusalNotice(src, h.file.ws))
}

// refusalNotice says why a page's src was not loaded: a web or file: address, an absolute path, a
// path out of the workspace (ws), or out of a file's own folder outside every workspace, which is
// the one adding the folder as a workspace answers.
func refusalNotice(src, ws string) string {
	src = strings.TrimSpace(src)
	u, err := url.Parse(src)
	switch {
	case err != nil:
		return "a resource the page names is not loaded: " + src
	case u.Scheme != "" || u.Host != "" || strings.HasPrefix(src, "//"):
		return "web and file: addresses in a page are not loaded (" + src + ")"
	case strings.HasPrefix(u.Path, "/"):
		return "absolute paths in a page are not loaded (" + src + ")"
	case ws != "":
		return "resources outside the workspace " + ws + " are not loaded (" + src + ")"
	}
	return "resources outside this file's folder are not loaded (" + src + "): add the folder as a workspace"
}

// linkPage gives the page its links, once the editor is built: they open as the preview pane's do,
// against the open file's folder.
func (h *Host) linkPage() {
	if v, ok := h.pageView(); ok {
		v.SetOnLink(h.htmlLink)
		v.SetOnRefused(h.resourceRefused)
	}
}
