package crew_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestReportsPromptsIsTheKindsWithAnAdapter: a kind reports its permission prompts only once its
// adapter does; any other kind, and an unknown one, reports none, so a window never presses Enter
// into it.
func TestReportsPromptsIsTheKindsWithAnAdapter(t *testing.T) {
	want := map[string]bool{crew.KindClaude: true, crew.KindCodex: false, crew.KindAgy: false, crew.KindOpenCode: true, "aider": false, "": false}
	for kind, reports := range want {
		if crew.ReportsPrompts(kind) != reports {
			t.Errorf("ReportsPrompts(%q) = %v, want %v", kind, !reports, reports)
		}
	}
}
