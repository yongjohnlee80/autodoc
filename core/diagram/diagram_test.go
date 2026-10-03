package diagram_test

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/diagram"
)

func TestFlowchartTerminalFallback(t *testing.T) {
	model, err := diagram.Parse("flowchart TD\nA[Start] -->|yes| B[Finish]\n")
	if err != nil {
		t.Fatal(err)
	}
	if model.Kind != "flowchart" || model.Direction != "TD" || model.Terminal() != "[Start] ── yes ──▶ [Finish]" {
		t.Fatalf("flowchart = %+v, %q", model, model.Terminal())
	}
}

func TestSequenceTerminalFallback(t *testing.T) {
	model, err := diagram.Parse("sequenceDiagram\nAlice->>Bob: hello\nBob-->>Alice: hi\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(model.Terminal(), "Alice ── hello ──▶ Bob") || !strings.Contains(model.Terminal(), "Bob ── hi ──▶ Alice") {
		t.Fatalf("sequence = %q", model.Terminal())
	}
}

func TestUnsupportedConstructIsExplicit(t *testing.T) {
	_, err := diagram.Parse("flowchart TD\nsubgraph Group\nA-->B\nend\n")
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "subgraph") {
		t.Fatalf("unsupported construct = %v", err)
	}
}
