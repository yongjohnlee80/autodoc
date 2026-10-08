package tui

import (
	"sync/atomic"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// A build's ProgramOptions reach the program, after AutoDoc's own: a style passed through them
// builds main.qml's Editor, and the host finds that editor and its core through editorWidget.
func TestProgramOptionsReachTheProgram(t *testing.T) {
	editor, ok := tuidecl.StandardType("Editor")
	if !ok {
		t.Fatal("no standard Editor type")
	}
	var built atomic.Int32
	std := editor.Build
	editor.Build = func(b tuidecl.Build) (tuicore.Component, []string, error) {
		built.Add(1)
		return std(b)
	}
	style := tuidecl.Style{Name: "probe", Types: []tuidecl.Type{editor}}
	r := runTUI(t, NewSession("unused", nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(style)}})
	if built.Load() == 0 {
		t.Fatal("the style passed in ProgramOptions did not build the page's editor")
	}
	same := onLoop(r, func() bool { return r.h.editor != nil && r.h.core != nil && r.h.core == r.h.editor.Core() })
	if !same {
		t.Fatal("the host's core is not the found editor's core")
	}
}
