package index

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/schema"
)

// FuzzFrontmatter: no document panics the frontmatter check, with a schema or without one: its
// diagnostics are its answer.
func FuzzFrontmatter(f *testing.F) {
	kb, err := schema.Parse([]byte("version: 1\nfrontmatter:\n  type:\n    type: string\n    enum: [note, adr]\n    required: true\n  tags:\n    type: list\n    item_type: string\n  created:\n    type: date\n"))
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{
		"---\ntype: note\ntags: [a, b]\ncreated: 2026-10-29\n---\n# T\n",
		"---\ntype: [\n---\nbody\n",
		"---\n---\n",
		"no frontmatter at all\n",
		"---\n: : :\n\t- x\n---\n",
		"---\ntype: adr\ncreated: not-a-date\ntags: 7\n---\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		b := []byte(src)
		doc := kind.ParseMarkdown(b)
		_ = frontmatterOf("notes/a.md", doc, b, kb, nil)
		_ = frontmatterOf("notes/a.md", doc, b, nil, nil)
	})
}
