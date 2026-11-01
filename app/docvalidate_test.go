package app

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"testing"
)

// TestDocValidateDiagnosesADiagramTheViewerCantDraw
// (docs/decisions/search.md#diagrams-the-viewer-cant-draw): through the daemon, doc.validate of a
// draft whose mermaid label holds HTML other than <br> (an ADR's <job id>) answers the diagram's
// diagnostic, field empty, rule mermaid, at the label's line in the file, with the parser's reason;
// the same diagram with <br/> answers none.
func TestDocValidateDiagnosesADiagramTheViewerCantDraw(t *testing.T) {
	dir := short(t)
	state, sock := filepath.Join(dir, "state"), filepath.Join(dir, "s.sock")
	kb := mustEval(t, t.TempDir())
	cfg := writeConfig(t, sock, state, "", "kb="+kb)
	start(t, cfg, sock)
	validate := func(content string) []any {
		t.Helper()
		b, _ := json.Marshal([]any{"kb", "adrs/jobs.md", content})
		var out bytes.Buffer
		if err := runCall(context.Background(), cfg, "doc.validate", string(b), &out); err != nil {
			t.Fatalf("doc.validate: %v", err)
		}
		var r map[string]any
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("doc.validate: %v: %s", err, out.String())
		}
		return asSlice(r["diagnostics"])
	}
	draft := "---\ntitle: Jobs\n---\n# Jobs\n\n```mermaid\nflowchart LR\n  A[queue] --> B[<job id>]\n```\n"
	d := validate(draft)
	want := map[string]any{"field": "", "line": float64(8), "rule": "mermaid", "message": "HTML in a label, diagram not drawn natively"}
	if len(d) != 1 || !maps.Equal(d[0].(map[string]any), want) {
		t.Errorf("doc.validate of the draft: %v, want [%v]", d, want)
	}
	if d := validate("# Jobs\n\n```mermaid\nflowchart LR\n  A[queue<br/>in] --> B\n```\n"); len(d) != 0 {
		t.Errorf("doc.validate of a <br/> label: %v, want none", d)
	}
}
