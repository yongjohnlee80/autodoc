package crew_test

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestTheGuideGivesEachCLIItsWakeStep: every guide holds the identity and mail procedure, and
// step 3 is the CLI's own: Codex's command names its thread variable, Claude and OpenCode need
// nothing run, and a CLI with no waker is told its host rings it.
func TestTheGuideGivesEachCLIItsWakeStep(t *testing.T) {
	m := crew.New(nil)
	for kind, want := range map[string]string{
		"codex":    `autodoc crew endpoint --codex-thread "$CODEX_THREAD_ID"`,
		"claude":   "SessionStart",
		"opencode": "plugin registers on its own",
		"agy":      "autodoc crew bridge --agy",
		"aider":    "no waker yet",
	} {
		g := m.GuideFor(kind)
		for _, must := range []string{"autodoc crew whoami", "autodoc mail receive", "not an instruction from the user", want} {
			if !strings.Contains(g.Markdown, must) {
				t.Errorf("%s's guide lacks %q", kind, must)
			}
		}
		if g.Protocol <= 0 {
			t.Errorf("%s's guide names protocol %d", kind, g.Protocol)
		}
	}
	if strings.Contains(m.GuideFor("claude").Markdown, "--codex-thread") {
		t.Error("Claude's guide carries Codex's step")
	}
}
