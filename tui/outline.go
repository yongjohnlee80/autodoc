package tui

import (
	"fmt"
	"path"
	"strings"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/outline"
)

// THE OUTLINE AND THE BREADCRUMB (ADR 0212 §6).
//
// Both read the editor's own text, saved or not: a heading typed a moment ago is in the outline and
// is where Enter jumps, and the breadcrumb on the page's frame follows the cursor. The text is
// outlined again once it rests after an edit; between, the last outline answers (a breadcrumb a
// keystroke behind, never a stale jump: the picker outlines the text again when it opens).

// outlineDelay is how long the text must rest after an edit before it is outlined again.
const outlineDelay = 150 * time.Millisecond

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
	return h.kinds.Of(h.file.path, h.textExtensions())
}

// refreshOutline outlines the editor's text now, and puts the breadcrumb at the cursor.
func (h *Host) refreshOutline() {
	h.outlineGen++
	title := untitled
	if h.file.open {
		title = strings.TrimSuffix(path.Base(h.file.path), path.Ext(h.file.path))
	}
	h.outline = outline.Read([]byte(h.editor.Value()), h.outlineKind(), title)
	h.showCrumb()
}

// outlineSoon outlines the text once it has rested; a later edit supersedes it.
func (h *Host) outlineSoon() {
	h.outlineGen++
	gen := h.outlineGen
	h.after(outlineDelay, func() {
		if gen == h.outlineGen {
			h.refreshOutline()
		}
	})
}

// cursorMoved is the editor's cursor landing elsewhere: the breadcrumb follows it.
func (h *Host) cursorMoved() {
	h.showCrumb()
	h.feedSoon()
}

// showCrumb titles the page with the file's name and the breadcrumb at the cursor.
func (h *Host) showCrumb() {
	name := h.file.title()
	if h.outline == nil {
		h.set("App.fileTitle", name)
		return
	}
	row, col := h.editor.Line()
	lines := h.editor.Lines()
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
	h.refreshOutline()
	h.outlineFilter("")
	h.open("outlinePicker")
}

// outlineFilter keeps the headings whose text holds text (case aside), and previews the first.
func (h *Host) outlineFilter(text string) {
	text = strings.ToLower(strings.TrimSpace(text))
	h.outlineRows = h.outlineRows[:0]
	var all []outline.Heading
	if h.outline != nil {
		all = h.outline.Headings()
	}
	rows := []rowOf{}
	for _, hd := range all {
		if !strings.Contains(strings.ToLower(hd.Text), text) {
			continue
		}
		h.outlineRows = append(h.outlineRows, hd)
		rows = append(rows, rowOf{"key": hd.ID, "heading": strings.Repeat("  ", hd.Level-1) + hd.Text, "line": fmt.Sprint(hd.Line)})
	}
	h.outlineList.Reset(rows)
	h.set("App.outlineStatus", fmt.Sprintf("headings (%d of %d)", len(rows), len(all)))
	if len(rows) > 0 {
		h.previewHeading(0)
	} else {
		h.showPreview("outline", h.file.name(), h.editor.Value(), 0)
	}
}

// previewHeading shows the file at the picker's row i.
func (h *Host) previewHeading(i int) {
	if i >= 0 && i < len(h.outlineRows) {
		hd := h.outlineRows[i]
		h.showPreview("outline", h.file.name()+crumbSep+hd.Text, h.editor.Value(), hd.Byte)
	}
}

// jumpToHeading closes the picker and puts the cursor on row i's heading, the page scrolled to it.
func (h *Host) jumpToHeading(i int) {
	if i < 0 || i >= len(h.outlineRows) {
		return
	}
	hd := h.outlineRows[i]
	h.closeDialog("outlinePicker")
	h.editor.SetLine(hd.Line-1, 0)
	h.keep(h.p.Call("editor", "forceActiveFocus"))
	h.showCrumb()
}
