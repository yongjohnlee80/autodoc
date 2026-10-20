package tui

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/kind"
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

// pageView is the editor's own document view, an HTMLView; false for an editor without one (a
// terminal's). It is kept as first read, at the editor's start: a CSV buffer's table takes the
// document view's place while it is shown (tabledoc.go), and the page comes back for an HTML file.
func (h *Host) pageView() (*widget.HTMLView, bool) {
	if h.pageDoc != nil {
		return h.pageDoc, true
	}
	dc, ok := h.editor.(documentComponent)
	if !ok {
		return nil, false
	}
	v, ok := dc.DocumentComponent().(*widget.HTMLView)
	if ok {
		h.pageDoc = v
	}
	return v, ok
}

// documentFor turns the page on for a raw HTML file under --gui and off for anything else, before
// the editor is given the file's text, and points its images at the file's folder when that
// changes.
func (h *Host) documentFor(ws, p string, raw bool) {
	h.refusedNoted = false // another document: what its page refuses is said again
	if h.noteFile != nil {
		// before its text arrives: the Rendered view lays the note's embeds out from its folder
		note := ""
		if p != "" && !raw && h.resolver().Of(p) == kind.Markdown {
			note = h.pathOnDisk(ws, p)
		}
		h.noteFile(note)
	}
	page := raw && h.native()
	var table *widget.TableDocument
	if comma := tableComma(p); comma != 0 && !raw && h.active != nil && p != "" {
		table = h.tableOf(h.active, comma)
	}
	v, hasPage := h.pageView()
	switch {
	case table != nil:
		setDocumentView(h.editor, table)
	case page && hasPage:
		setDocumentView(h.editor, v)
	}
	on := table != nil || page
	switch {
	case h.docSwitch != nil:
		h.docSwitch(on)
	default:
		if ds, ok := h.editor.(documentSwitch); ok {
			ds.SetRenderedDocument(on)
		}
	}
	if !page || !hasPage {
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
	h.notify(refusalNoticeIn(h.trf, src, h.file.ws))
}

// refusalNotice says why a page's src was not loaded: a web or file: address, an absolute path, a
// path out of the workspace (ws), or out of a file's own folder outside every workspace, which is
// the one adding the folder as a workspace answers. It is English: refusalNoticeIn says it in the
// UI's language.
func refusalNotice(src, ws string) string { return refusalNoticeIn(inEnglish{}.Trf, src, ws) }

// refusalNoticeIn is refusalNotice in trf's language (h.trf).
func refusalNoticeIn(trf func(string, ...any) string, src, ws string) string {
	src = strings.TrimSpace(src)
	u, err := url.Parse(src)
	switch {
	case err != nil:
		return trf("autodoc.notice.htmldoc.a_resource_the_page_names_is_not_loaded", src)
	case u.Scheme != "" || u.Host != "" || strings.HasPrefix(src, "//"):
		return trf("autodoc.notice.htmldoc.web_and_file_addresses_in_a_page_are_not_loaded", src)
	case strings.HasPrefix(u.Path, "/"):
		return trf("autodoc.notice.htmldoc.absolute_paths_in_a_page_are_not_loaded", src)
	case ws != "":
		return trf("autodoc.notice.htmldoc.resources_outside_the_workspace_are_not_loaded", ws, src)
	}
	return trf("autodoc.notice.htmldoc.resources_outside_this_file_s_folder_are_not_loa", src)
}

// linkPage gives the page its links, once the editor is built: they open as the preview pane's do,
// against the open file's folder.
func (h *Host) linkPage() {
	if v, ok := h.pageView(); ok {
		v.SetOnLink(h.htmlLink)
		v.SetOnRefused(h.resourceRefused)
	}
}
