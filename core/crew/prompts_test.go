package crew_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestReportsPromptsIsTheKindsWithAnAdapter: Claude's hooks report its prompts whatever its
// endpoint; OpenCode's plugin and agy's bridge are their endpoints, so they report only while the
// session is reachable; Codex, and any other kind, reports none, so a window never presses Enter
// into it.
func TestReportsPromptsIsTheKindsWithAnAdapter(t *testing.T) {
	for _, c := range []struct {
		kind               string
		reachable, unreach bool
	}{
		{crew.KindClaude, true, true},
		{crew.KindOpenCode, true, false},
		{crew.KindAgy, true, false},
		{crew.KindCodex, false, false},
		{"aider", false, false},
		{"", false, false},
	} {
		if got := crew.ReportsPrompts(c.kind, true); got != c.reachable {
			t.Errorf("ReportsPrompts(%q, reachable) = %v, want %v", c.kind, got, c.reachable)
		}
		if got := crew.ReportsPrompts(c.kind, false); got != c.unreach {
			t.Errorf("ReportsPrompts(%q, unreachable) = %v, want %v", c.kind, got, c.unreach)
		}
	}
}
