//go:build gui

package tui

import (
	"testing"

	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// In the GUI a CSV file opens as its table, drawn in pixels, in the Rendered view the window starts
// in: a cell kept by its keys is the file's text, undone by the editor's own undo key. A raw HTML
// file after it gets the page back as the document view (ADR 1792303119 §4.7).
func TestACSVFileIsATableInTheGUI(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.csv", "h,v\na,1\nb,2\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.s.WaitForText(t, "· kb")
	page := onLoop(r, func() *widget.HTMLView { v, _ := r.h.pageView(); return v })
	if page == nil {
		t.Fatal("the GUI editor has no page view")
	}

	onLoop(r, func() bool { r.h.openPath("a.csv"); return true })
	r.waitFile(t, "a.csv")
	type shown struct {
		mode  guiwidget.EditorMode
		table *widget.TableDocument
		page  bool
	}
	now := func() shown {
		return onLoop(r, func() shown {
			e := r.h.editor.(*guiwidget.Editor)
			tb, _ := e.DocumentComponent().(*widget.TableDocument)
			return shown{e.Shown(), tb, e.DocumentComponent() == tuicore.Component(page)}
		})
	}
	r.s.WaitFor(t, "the table shown", func(string) bool { s := now(); return s.mode == guiwidget.Rendered && s.table != nil })
	tb := now().table
	if bound := onLoop(r, func() bool { return tb.Table().BoundLayout() != widget.TableLayout(tb.Table()) }); !bound {
		t.Fatal("the GUI's table is not laid out in pixels")
	}
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter},
		tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: '9', Text: "9"},
		tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter})
	r.s.WaitFor(t, "the cell kept", func(string) bool { return r.editorText() == "h,v\na9,1\nb,2\n" })
	undo := onLoop(r, func() tuicore.KeyEvent {
		u := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'u', Text: "u"}
		if act, ok := r.h.core.ActionOf(u); ok && act == widget.ActUndo {
			return u
		}
		return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'z', Mods: tuicore.ModCtrl}
	})
	r.keys(t, undo)
	r.s.WaitFor(t, "undone in the table", func(string) bool { return r.editorText() == "h,v\na,1\nb,2\n" })

	onLoop(r, func() bool {
		r.h.nativeViews = func() bool { return true } // a GUI window: the page is the raw HTML file's Rendered view
		r.h.documentFor("kb", "page.html", true)
		return true
	})
	if s := now(); !s.page || s.table != nil {
		t.Fatalf("a raw HTML file after the table: the page is the document view %v, a table %v", s.page, s.table != nil)
	}
}
