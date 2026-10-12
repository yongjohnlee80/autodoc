package tui

import (
	"context"
	"fmt"
	"path"
	"strings"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/outline"
)

// THE OUTLINE AND THE BREADCRUMB (ADR 0212 §6), a feature of its own.
//
// Both read the page's text, saved or not: a heading typed a moment ago is in the outline and is
// where Enter jumps, and the breadcrumb on the page's frame follows the cursor. The text is outlined
// again at each typing pause (a DocWatcher's Edited); between, the last outline answers (a
// breadcrumb a keystroke behind, never a stale jump: the picker outlines the page as it is now when
// it opens).

// crumbSep separates the breadcrumb's parts, and the file's name from them.
const crumbSep = " › "

// showTitle titles the page's frame: the file's name, then the breadcrumb a feature put there.
func (h *Host) showTitle() {
	title := h.file.title()
	if len(h.crumbs) > 0 {
		title += crumbSep + strings.Join(h.crumbs, crumbSep)
	}
	h.set("App.fileTitle", title)
}

// cursorMoved is the editor's cursor landing elsewhere: the watchers hear it (the breadcrumb follows).
func (h *Host) cursorMoved() {
	h.followCursorInPane()
	line, _ := h.core.Line()
	h.reg.moved(line, h.cursorBytes())
}

// clusterBytes is how many bytes the first n characters (grapheme clusters) of line take: the
// editor counts its column in characters.
func clusterBytes(line string, n int) int {
	b := 0
	for c := range tuicore.Graphemes(line) {
		if n == 0 {
			break
		}
		b += len(c)
		n--
	}
	return b
}

// outliner is Go › Outline and the breadcrumb: the page's outline, read at each pause, and the
// cursor's place in it.
type outliner struct {
	c        Core
	page     Page
	doc      *outline.Doc // nil while no page is shown
	line, at int          // the cursor: its line (0-based) and byte offset
	pick     *pick[outline.Heading]
}

func newOutline(c Core) *outliner {
	o := &outliner{c: c}
	o.pick = &pick[outline.Heading]{c: c, dialog: "outlinePicker", name: "outline",
		list:   newRows(tuidecl.NewListModel("key", "heading", "line"), headingRowOf),
		field:  "App.outlineField",
		status: "App.outlineStatus", noun: "headings",
		match: func(hd outline.Heading) string { return hd.Text },
		preview: func(hd outline.Heading) func(context.Context) answerOf[pane] {
			pn := pane{title: pageName(o.page) + crumbSep + hd.Text, text: string(o.page.Text), at: hd.Byte}
			return func(context.Context) answerOf[pane] { return answerOf[pane]{v: pn} }
		},
		// no heading: the whole note
		empty:  func() pane { return pane{title: pageName(o.page), text: string(o.page.Text)} },
		scope:  TicketFile,
		choose: o.jump}
	return o
}

func (*outliner) Name() string { return "outline" }

func (o *outliner) State() map[string]any {
	return map[string]any{
		"App.outlineRows":         o.pick.list.Model(),
		"App.outlineField":        "",
		"App.outlineStatus":       "headings",
		"App.outlinePreviewTitle": "",
		"App.outlinePreviewText":  "",
		"App.outlinePreviewAt":    0,
	}
}

func (o *outliner) Commands() map[string]Command {
	return map[string]Command{"App.openOutline": none(o.open)}
}

// Catalog is the outline's user command (catalog.go): it runs what App.openOutline runs.
func (o *outliner) Catalog() []Entry {
	return []Entry{builtin("file.outline", "the file's outline", "App.openOutline()", o.open)}
}

func (o *outliner) Picks() map[string]picker { return map[string]picker{"outline": o.pick} }

// The page's lifecycle: outlined when shown, at each pause and when read as another kind; the
// breadcrumb follows the cursor, from where it was when the page was shown; nothing is outlined
// once the page goes.
func (o *outliner) Shown(p Page) {
	o.line, o.at = p.Line, p.At
	o.read(p)
}
func (o *outliner) Edited(p Page) { o.read(p) }
func (o *outliner) Reread(p Page) { o.read(p) }
func (o *outliner) Moved(line, at int) {
	o.line, o.at = line, at
	o.crumb()
}
func (o *outliner) Closed() {
	o.page, o.doc = Page{}, nil
	o.pick.setAll(nil)
	o.pick.list.Reset(nil)
	o.c.Crumbs(nil)
}

// read outlines page p and puts the breadcrumb at the cursor. The cursor is Moved's, not p's: a
// page read again (Reread, the picker's) may be a snapshot taken before the cursor last moved.
func (o *outliner) read(p Page) {
	o.page = p
	title := untitled
	if p.Ref.path != "" {
		title = strings.TrimSuffix(path.Base(p.Ref.path), path.Ext(p.Ref.path))
	}
	o.doc = outline.Read(p.Text, p.Kind, title)
	o.crumb()
}

// crumb puts the breadcrumb at the cursor: none for plain text, whose title is the name already on
// the frame.
func (o *outliner) crumb() {
	if o.doc == nil || o.page.Kind == kind.Text {
		o.c.Crumbs(nil)
		return
	}
	// the cursor's byte in its line: the line starts where the outline's text has it (the lines
	// above the cursor are as typed when the text was taken)
	o.c.Crumbs(o.doc.Crumbs(o.line+1, max(o.at-o.doc.LineStart(o.line+1), 0)))
}

// open opens Go › Outline over the page's headings, as they are now.
func (o *outliner) open() {
	p := o.c.Page()
	if p.Kind != kind.Markdown {
		what := "its title"
		if p.Kind == kind.YAML {
			what = "its key path, on the page's frame"
		}
		o.c.Notify(fmt.Sprintf("%s has no headings to navigate: the breadcrumb shows %s", pageName(p), what))
		return
	}
	o.read(p)
	o.pick.setAll(o.doc.Headings())
	o.pick.open()
}

// jump puts the cursor on heading hd, the page scrolled to it: the picker has closed.
func (o *outliner) jump(hd outline.Heading) {
	o.c.GoToLine(hd.Line - 1)
	o.Moved(hd.Line-1, o.doc.LineStart(hd.Line))
}

// pageName is the page's file as its title names it: its path, or untitled for the draft.
func pageName(p Page) string {
	if p.Ref.path == "" {
		return untitled
	}
	return p.Ref.path
}

// headingRowOf is a heading's row, indented by its level.
func headingRowOf(_ int, hd outline.Heading) rowOf {
	return rowOf{"key": hd.ID, "heading": strings.Repeat("  ", hd.Level-1) + hd.Text, "line": fmt.Sprint(hd.Line)}
}
