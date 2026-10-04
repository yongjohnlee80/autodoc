package workspace_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/workspace"
)

func TestBraceAlternativesApplyToIncludeAndExclude(t *testing.T) {
	matcher := workspace.NewMatcher([]string{"**/*.{md,txt}"}, []string{"**/{drafts,private}/**"})
	for path, want := range map[string]bool{
		"readme.md":          true,
		"docs/notes.txt":     true,
		"docs/notes.yaml":    false,
		"drafts/notes.md":    false,
		"docs/private/a.txt": false,
	} {
		if got := matcher.Match(path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
	if !matcher.Excluded("docs/private") || !matcher.Excluded("drafts") {
		t.Fatal("brace-expanded excludes did not prune their directories")
	}
}

// A pattern that does not expand (an unclosed brace) matches nothing rather than everything; the
// valid patterns beside it still match.
func TestAMalformedPatternIsSkipped(t *testing.T) {
	m := workspace.NewMatcher([]string{"**/*.{md", "**/*.txt"}, nil)
	if m.Match("a.md") || !m.Match("a.txt") {
		t.Fatalf("match a.md=%v a.txt=%v", m.Match("a.md"), m.Match("a.txt"))
	}
}
