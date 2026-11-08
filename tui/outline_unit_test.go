package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// at is where line (0-based) of text starts, plus col bytes: the cursor as Moved hands it.
func at(text string, line, col int) int {
	off := 0
	for range line {
		off += strings.IndexByte(text[off:], '\n') + 1
	}
	return off + col
}

// The outline, a feature of its own: the breadcrumb follows the page and its cursor, from where
// the cursor was when the page was shown; a page read again keeps the cursor Moved last put; plain
// text has none, YAML its key path; nothing is left once the page goes.
func TestTheOutlineFollowsThePageAndItsCursor(t *testing.T) {
	c := newScripted("kb")
	o := newOutline(c)
	guide := Page{Ref: fileRef{"kb", "guide.md"}, Kind: kind.Markdown, Text: []byte(guideFile), Line: 10, At: at(guideFile, 10, 0)}
	crumbs := func(want ...string) {
		t.Helper()
		if !slices.Equal(c.crumbs, want) {
			t.Fatalf("crumbs %q, want %q", c.crumbs, want)
		}
	}
	o.Shown(guide) // the cursor on "apt", under Linux
	crumbs("Guide", "Setup", "Linux")
	o.Moved(14, at(guideFile, 14, 1)) // "run", under Usage
	crumbs("Guide", "Usage")
	o.Reread(guide) // the snapshot shown, its cursor still on "apt": the cursor stays where it moved
	crumbs("Guide", "Usage")

	o.Closed()
	crumbs()
	if o.doc != nil {
		t.Fatal("the outline outlived its page")
	}
	o.Shown(Page{Ref: fileRef{"kb", "notes.txt"}, Kind: kind.Text, Text: []byte("# not a heading\n")})
	crumbs()
	conf := "server:\n  database:\n    host: db\n"
	o.Shown(Page{Ref: fileRef{"kb", "conf.yaml"}, Kind: kind.YAML, Text: []byte(conf), Line: 2, At: at(conf, 2, 6)})
	crumbs("server", "database", "host")
}

// Go › Outline lists the headings of the page as it is now and jumps to the one chosen; a page
// without headings says what its breadcrumb shows instead.
func TestTheOutlinePickerOpensAndJumps(t *testing.T) {
	c := newScripted("kb")
	o := newOutline(c)
	c.page = Page{Ref: fileRef{"kb", "conf.yaml"}, Kind: kind.YAML, Text: []byte("a: 1\n")}
	o.open()
	if len(c.opened) != 0 || len(c.notes) != 1 || !strings.Contains(c.notes[0], "conf.yaml has no headings to navigate: the breadcrumb shows its key path") {
		t.Fatalf("a YAML page's outline: opened %v, said %q", c.opened, c.notes)
	}
	c.page = Page{Ref: fileRef{"kb", "guide.md"}, Kind: kind.Markdown, Text: []byte(guideFile)}
	o.open()
	if !slices.Equal(c.opened, []string{"outlinePicker"}) || len(o.pick.all) != 4 {
		t.Fatalf("opened %v over %d headings, want the picker over 4", c.opened, len(o.pick.all))
	}
	o.jump(o.pick.all[3]) // Usage, line 13
	if !slices.Equal(c.views, []string{"editor.line 12"}) {
		t.Fatalf("the jump moved the editor by %v", c.views)
	}
	if !slices.Equal(c.crumbs, []string{"Guide", "Usage"}) {
		t.Fatalf("crumbs after the jump %q", c.crumbs)
	}
}
