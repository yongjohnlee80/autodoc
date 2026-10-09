//go:build gui

package tui

import (
	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	"github.com/yongjohnlee80/golib/highlight"
	"github.com/yongjohnlee80/golib/indent"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"strings"
	"testing"
)

func TestAutoDocNativeConstructionUsesAnInjectedSourceProvider(t *testing.T) {
	var seen []*highlight.Catalog
	def := highlight.Definition{Name: "Native custom", Extensions: []string{"*.nativesource"}, Aliases: []string{"nativesource"}, SourceFactory: func(c *highlight.Catalog) highlight.Source {
		seen = append(seen, c)
		return highlight.Source{Highlighter: highlight.HighlighterFunc(func(line string, s highlight.State) ([]highlight.Span, highlight.State) {
			at := strings.Index(line, "token")
			if at < 0 {
				return nil, s
			}
			return []highlight.Span{{Start: at, End: at + 5, Style: highlight.Keyword}}, s
		}), Indenter: indent.PolicyFunc(func(r indent.Request) (indent.Decision, bool) { return indent.Decision{Prefix: "  "}, r.StateKnown })}
	}}
	r := runTUI(t, NewSession("unused", nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(guidecl.Native()), tuidecl.Highlighters(def)}})
	for _, tc := range []struct {
		path, text string
		ln         int
	}{
		{"/tmp/demo.nativesource", "token", 0}, {"/tmp/demo.md", "```nativesource\ntoken\n```", 1},
	} {
		ok := onLoop(r, func() bool {
			r.h.show("", tc.path, tc.text, "v1", false)
			_, native := r.h.editor.(*guiwidget.Editor)
			f := r.h.core.BeginHighlight(0)
			defer f.Close()
			styles := f.Styles(tc.ln)
			return native && len(styles) > 0 && styles[0] == highlight.Keyword
		})
		if !ok {
			t.Fatal("native source/fence selection failed", tc.path)
		}
	}
	ok := onLoop(r, func() bool {
		for _, c := range seen {
			if c != r.h.p.SourceLanguages() {
				return false
			}
		}
		return len(seen) >= 2
	})
	if !ok {
		t.Fatal("native catalog propagation failed")
	}
}
