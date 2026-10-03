package tui

import (
	"strings"
	"testing"
)

func TestMermaidBlockUsesTheOneUnderTheCursor(t *testing.T) {
	source := "# Note\n\n```mermaid\nflowchart TD\nA-->B\n```\n\n```mermaid\nsequenceDiagram\nAlice->>Bob: hi\n```\n"
	block, ok := mermaidBlock([]byte(source), strings.Index(source, "Alice"))
	if !ok || !strings.Contains(block, "sequenceDiagram") {
		t.Fatalf("selected block = %q, %v", block, ok)
	}
	block, ok = mermaidBlock([]byte(source), 0)
	if !ok || !strings.Contains(block, "flowchart TD") {
		t.Fatalf("default block = %q, %v", block, ok)
	}
}
