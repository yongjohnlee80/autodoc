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

// THE OUTLINE AND THE BREADCRUMB (ADR 0212 §6).
//
// Both read the editor's own text, saved or not: a heading typed a moment ago is in the outline and
// is where Enter jumps, and the breadcrumb on the page's frame follows the cursor. The text is
// outlined again once it rests after an edit; between, the last outline answers (a breadcrumb a
// keystroke behind, never a stale jump: the picker outlines the text again when it opens).

// crumbSep separates the breadcrumb's parts, and the file's name from them.
const crumbSep = " › "

// outlineKind is how the open file (or the draft, a Markdown file to be) is read.
func (h *Host) outlineKind() kind.Kind {
	if !h.file.open {
		return kind.Markdown
	}
	if h.file.derived != "" {
		return kind.Markdown // a derived document's text
	}
	return h.resolver().Of(h.file.path)
}

// refreshOutline outlines the editor's text now, and puts the breadcrumb at the cursor.
func (h *Host) refreshOutline() {
	h.outlineGen++
	title := untitled
	if h.file.open {
		title = strings.TrimSuffix(path.Base(h.file.path), path.Ext(h.file.path))
	}
	h.outline = outline.Read(h.page.Text, h.outlineKind(), title)
	h.showCrumb()
}

// cursorMoved is the editor's cursor landing elsewhere: the breadcrumb follows it.
func (h *Host) cursorMoved() {
	h.showCrumb()
	h.feedSoon()
	h.followCursorInPane()
	line, _ := h.core.Line()
	h.reg.moved(line, h.cursorBytes())
}

// showCrumb titles the page with the file's name and the breadcrumb at the cursor.
func (h *Host) showCrumb() {
	name := h.file.title()
	if h.outline == nil {
		h.set("App.fileTitle", name)
		return
	}
	row, col := h.core.Line()
	lines := h.core.Lines()
	byteCol := 0
	if row < len(lines) {
		byteCol = clusterBytes(lines[row], col)
	}
	crumbs := h.outline.Crumbs(row+1, byteCol)
	if h.outlineKind() == kind.Text {
		crumbs = nil // the title is the name already on the frame
	}
	if len(crumbs) == 0 {
		h.set("App.fileTitle", name)
		return
	}
	h.set("App.fileTitle", name+crumbSep+strings.Join(crumbs, crumbSep))
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

// openOutline opens Go › Outline over the file's headings, as the editor's text has them now.
func (h *Host) openOutline() {
	if k := h.outlineKind(); k != kind.Markdown {
		what := "its title"
		if k == kind.YAML {
			what = "its key path, on the page's frame"
		}
		h.notify(fmt.Sprintf("%s has no headings to navigate: the breadcrumb shows %s", h.file.name(), what))
		return
	}
	h.pageNow() // the headings as they are, before the typing pause
	h.refreshOutline()
	var all []outline.Heading
	if h.outline != nil {
		all = h.outline.Headings()
	}
	h.outlinePick.setAll(all)
	h.outlinePick.open()
}

// newOutlinePick is Go › Outline's picker over the file's headings; its preview is the editor's
// text at the heading.
func (h *Host) newOutlinePick() *pick[outline.Heading] {
	return &pick[outline.Heading]{c: core{h}, dialog: "outlinePicker", name: "outline",
		list:   newRows(tuidecl.NewListModel("key", "heading", "line"), headingRowOf),
		status: "App.outlineStatus", noun: "headings",
		match: func(hd outline.Heading) string { return hd.Text },
		preview: func(hd outline.Heading) func(context.Context) answerOf[pane] {
			pn := pane{title: h.file.name() + crumbSep + hd.Text, text: h.core.Value(), at: hd.Byte}
			return func(context.Context) answerOf[pane] { return answerOf[pane]{v: pn} }
		},
		// no heading: the whole note
		empty:  func() pane { return pane{title: h.file.name(), text: h.core.Value()} },
		scope:  TicketFile,
		choose: h.jumpToHeading}
}

// headingRowOf is a heading's row, indented by its level.
func headingRowOf(_ int, hd outline.Heading) rowOf {
	return rowOf{"key": hd.ID, "heading": strings.Repeat("  ", hd.Level-1) + hd.Text, "line": fmt.Sprint(hd.Line)}
}

// jumpToHeading puts the cursor on heading hd, the page scrolled to it: the picker has closed.
func (h *Host) jumpToHeading(hd outline.Heading) {
	h.core.SetLine(hd.Line-1, 0)
	h.keep(h.p.Call("editor", "forceActiveFocus"))
	h.showCrumb()
}
