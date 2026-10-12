package workspace_test

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/workspace"
)

func TestBracePatternsAreBoundedAndValidated(t *testing.T) {
	patterns, err := workspace.ExpandPattern("**/*.{md,txt}/{one,two}")
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 4 || patterns[0] != "**/*.md/one" || patterns[3] != "**/*.txt/two" {
		t.Fatalf("expanded patterns = %v", patterns)
	}
	for _, pattern := range []string{"**/*.{md,txt}", "**/[a-z].{md,txt}"} {
		if err := workspace.ValidPattern(pattern); err != nil {
			t.Errorf("valid pattern %q: %v", pattern, err)
		}
	}
	for _, pattern := range []string{"**/*.{md,}", "**/*.{md}", "**/*.{md,{txt,yaml}}", "**/*.{md,txt", "**/*.md}", "**/[bad.{md,txt}", "**/*.{" + strings.Repeat("a,", 64) + "z}"} {
		if err := workspace.ValidPattern(pattern); err == nil {
			t.Errorf("accepted invalid pattern %q", pattern)
		}
	}
}
