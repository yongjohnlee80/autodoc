package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc/core/crew"
)

// guideBudget is what the whole written guide may weigh, in bytes: every one is read at the start
// of every session (token item 2). AGENTS.md's verb tables alone were 59 KB of an 84 KB guide.
const guideBudget = 30_000

// verbRow is a verb's row in AGENTS.md's generated tables: | `domain.verb` | … |.
var verbRow = regexp.MustCompile("(?m)^\\| `([a-z]+)\\.([a-z_]+)` \\|")

// TestTheWrittenGuideIndexesTheVerbsInsteadOfTablingThem (token item 2): the guide an agent is
// written stays within guideBudget. It leaves AGENTS.md's verb and error tables out: every verb is
// still named, under its domain, a people's-tools verb marked, every error code with what to do
// next, and the guide says how to print a verb's whole contract with no daemon. The rest of
// AGENTS.md (search.query's own section, the todos rules) stays as written; the repo's AGENTS.md
// itself is whole.
func TestTheWrittenGuideIndexesTheVerbsInsteadOfTablingThem(t *testing.T) {
	g := agentGuide(wsInfo{name: "kb", root: "/kb"}, "/bin/autodoc", "", "/w/bin/autodoc", crew.KindClaude, crew.MailViaCLI)
	if len(g) > guideBudget {
		t.Errorf("the written guide is %d bytes, over its budget of %d", len(g), guideBudget)
	}
	if verbRow.MatchString(g) || strings.Contains(g, "| verb | params |") {
		t.Error("the written guide still holds AGENTS.md's verb tables")
	}
	if !strings.Contains(g, "`autodoc --contracts <verb or domain.*> --format md`") {
		t.Error("the written guide doesn't say how to print a verb's contract")
	}
	if strings.Contains(g, "Every verb is below with its parameters") {
		t.Error("the written guide still says every verb's parameters are below")
	}
	rows := verbRow.FindAllStringSubmatch(autodoc.AgentsGuide, -1)
	if len(rows) < 100 {
		t.Fatalf("AGENTS.md has %d verb rows: the repo's guide must stay whole", len(rows))
	}
	for _, r := range rows {
		line := ""
		for _, l := range strings.Split(g, "\n") {
			if strings.HasPrefix(l, "- `"+r[1]+"`: ") {
				line = l
			}
		}
		if !regexp.MustCompile(`[ ,]` + regexp.QuoteMeta(r[2]) + `\*?(,|$)`).MatchString(line) {
			t.Errorf("the index names no %s.%s under %s: %q", r[1], r[2], r[1], line)
		}
	}
	if !strings.Contains(g, "- `doc`: ") || !regexp.MustCompile(`(?m)^- `+"`doc`"+`: .*write\*`).MatchString(g) {
		t.Error("the index doesn't mark doc.write, a people's tool")
	}
	for _, code := range regexp.MustCompile("(?m)^\\| (-320[0-9]{2}) \\| `").FindAllStringSubmatch(autodoc.AgentsGuide, -1) {
		if !strings.Contains(g, "\n- "+code[1]+" `") {
			t.Errorf("the written guide lists no error %s", code[1])
		}
	}
	for _, kept := range []string{"## `search.query`", "## Todos: what by hand, what by verb", "## A search, step by step"} {
		if !strings.Contains(g, kept) {
			t.Errorf("the written guide lost AGENTS.md's %q", kept)
		}
	}
}
