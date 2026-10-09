package tui

import (
	"testing"

	"github.com/yongjohnlee80/golib/highlight"
	"github.com/yongjohnlee80/golib/parse/yaml"
)

func TestYAMLSyntaxHighlightsKeysValuesAndBlockScalars(t *testing.T) {
	highlighter := yaml.Definition().NewSource(nil).Highlighter
	spans, state := highlighter.HighlightBlock("name: \"Puffin\" # label", 0)
	if len(spans) != 3 || spans[0].Style != highlight.Attribute || spans[1].Style != highlight.String || spans[2].Style != highlight.Comment || state != 0 {
		t.Fatalf("YAML key/value/comment spans = %+v, state %d", spans, state)
	}
	spans, state = highlighter.HighlightBlock("message: |", 0)
	if state == 0 || len(spans) == 0 {
		t.Fatalf("block scalar header spans = %+v, state %d", spans, state)
	}
	spans, state = highlighter.HighlightBlock("  literal text", state)
	if state == 0 || len(spans) != 1 || spans[0].Style != highlight.String {
		t.Fatalf("block scalar body spans = %+v, state %d", spans, state)
	}
	spans, state = highlighter.HighlightBlock("enabled: true", state)
	if state != 0 || len(spans) != 2 || spans[1].Style != highlight.Constant {
		t.Fatalf("following key spans = %+v, state %d", spans, state)
	}
}
