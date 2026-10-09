package tui

import (
	"github.com/yongjohnlee80/golib/highlight"
	"github.com/yongjohnlee80/golib/indent"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
	"strings"
	"testing"
)

func TestActualSourceFileUsesItsLanguageBeforeAndAfterFind(t *testing.T) {
	r := runTUI(t, NewSession("unused", nil), Options{})
	onLoop(r, func() bool {
		r.h.show("", "/tmp/example.go", "package main\nfunc f() {\n\t", "v1", false)
		r.h.core.SetKeyset(widget.KeysetStandard)
		return true
	})
	r.s.WaitForText(t, "package main")
	got := onLoop(r, func() highlight.Style { f := r.h.core.BeginHighlight(0); defer f.Close(); return f.Styles(0)[0] })
	if got != highlight.Keyword {
		t.Fatal("source file stayed plain", got)
	}
	onLoop(r, func() bool {
		r.h.find.query = "func"
		r.h.find.target = findPage
		r.h.showFind()
		r.h.core.SetLine(1, len("func f() {"))
		r.h.core.HandleKey(tuicore.KeyEvent{Code: tuicore.KeyEnter, Kind: tuicore.KeyPress})
		return true
	})
	if text := onLoop(r, func() string { return r.h.core.Value() }); !strings.Contains(text, "func f() {\n\t") {
		t.Fatal("find dropped the source indent policy", text)
	}
}

func TestCustomProviderWorksForActualSourceAndMarkdownThroughAutoDoc(t *testing.T) {
	def := highlight.Definition{Name: "Custom", Extensions: []string{"*.custom"}, Aliases: []string{"custom"}, SourceFactory: func(*highlight.Catalog) highlight.Source {
		return highlight.Source{Highlighter: highlight.HighlighterFunc(func(line string, s highlight.State) ([]highlight.Span, highlight.State) {
			at := strings.Index(line, "token")
			if at < 0 {
				return nil, s
			}
			return []highlight.Span{{Start: at, End: at + 5, Style: highlight.Keyword}}, s
		}), Indenter: indent.PolicyFunc(func(req indent.Request) (indent.Decision, bool) {
			return indent.Decision{Prefix: "   "}, req.StateKnown
		})}
	}}
	r := runTUI(t, NewSession("unused", nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.Highlighters(def)}})
	for _, tc := range []struct {
		path, text string
		line, col  int
	}{
		{"/tmp/example.custom", "token", 0, 0},
		{"/tmp/example.md", "```custom\ntoken\n```", 1, 0},
	} {
		onLoop(r, func() bool {
			r.h.show("", tc.path, tc.text, "v1", false)
			r.h.core.SetKeyset(widget.KeysetStandard)
			return true
		})
		got := onLoop(r, func() highlight.Style {
			f := r.h.core.BeginHighlight(0)
			defer f.Close()
			return f.Styles(tc.line)[tc.col]
		})
		if got != highlight.Keyword {
			t.Fatal(tc.path, got)
		}
		onLoop(r, func() bool {
			r.h.core.SetLine(tc.line, 5)
			r.h.core.HandleKey(tuicore.KeyEvent{Code: tuicore.KeyEnter, Kind: tuicore.KeyPress})
			return true
		})
		if text := onLoop(r, func() string { return r.h.core.Value() }); !strings.Contains(text, "token\n   ") {
			t.Fatal(tc.path, text)
		}
	}
}
