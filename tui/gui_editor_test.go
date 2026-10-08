//go:build gui

package tui

import (
	"testing"

	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// Under --gui's style, main.qml's Editor is gui's Editor: the host binds it through editorWidget,
// its core is the host's, and the MarkdownRenderer gives it a Rendered view. No window opens: the
// session runs on the test backend.
func TestTheGUIStyleBuildsTheGUIEditor(t *testing.T) {
	r := runTUI(t, NewSession("unused", nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(guidecl.Native())}})
	got := onLoop(r, func() bool {
		e, ok := r.h.editor.(*guiwidget.Editor)
		return ok && r.h.core == e.Core()
	})
	if !got {
		t.Fatalf("main.qml's editor under the native style is %T, not gui's Editor bound to the host's core", onLoop(r, func() any { return r.h.editor }))
	}
	rendered := onLoop(r, func() bool {
		e := r.h.editor.(*guiwidget.Editor)
		e.SetMode(guiwidget.Rendered)
		return e.Mode() == guiwidget.Rendered
	})
	if !rendered {
		t.Error("the gui Editor has no Rendered view: main.qml's MarkdownRenderer did not reach it")
	}
}
