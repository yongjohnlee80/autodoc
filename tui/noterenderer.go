package tui

import (
	"fmt"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE NOTE EDITOR'S RENDERER: main.qml's document Editor declares NoteRenderer, not golib's
// MarkdownRenderer. It is golib's Markdown renderer, with the same settings, and a window build
// draws the open note's embeds with it (embeds_gui.go): the note is the one the host tells it
// (Options.NoteFile). Every other Editor (the search preview, a todo's details, the tutorial)
// declares golib's MarkdownRenderer and draws no embeds: its text is not the open note's, so the
// note's folder is not where its embeds are. A terminal's Editor has no Rendered view and ignores it.

// NoteRendererKind is the note editor's renderer kind, which a window build's style draws.
const NoteRendererKind = "note"

// NoteRendererSpec is NoteRenderer's settings: golib's MarkdownRenderer's.
type NoteRendererSpec struct{ tuidecl.MarkdownSpec }

// RendererKind is NoteRendererKind.
func (NoteRendererSpec) RendererKind() string { return NoteRendererKind }

// noteRendererType is NoteRenderer, declared as MarkdownRenderer is: its settings are taken at
// construction.
var noteRendererType = tuidecl.Type{Name: "NoteRenderer", Build: buildNoteRenderer, Ctor: []string{"headingScale", "mermaid"}}

func buildNoteRenderer(b tuidecl.Build) (tuicore.Component, []string, error) {
	if len(b.Children) != 0 {
		return nil, nil, fmt.Errorf("a NoteRenderer takes no children (at %s)", b.Pos)
	}
	var scale float64
	var mermaid bool
	consumed, err := tuidecl.ReadProps(b.Props, map[string]tuidecl.Field{
		"headingScale": tuidecl.NumberField(&scale),
		"mermaid":      tuidecl.BoolField(&mermaid),
	})
	if err != nil {
		return nil, nil, err
	}
	if scale < 0 {
		return nil, nil, fmt.Errorf("headingScale: %v is negative (at %s)", scale, b.Pos)
	}
	spec := tuidecl.MarkdownSpec{HeadingScale: float32(scale), Mermaid: mermaid, SourceLanguages: b.SourceLanguages}
	return tuidecl.RendererNode(NoteRendererSpec{spec}), consumed, nil
}
