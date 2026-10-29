package schema

import "testing"

// FuzzParse: no schema text panics Parse, and a schema that parses validates any frontmatter
// without panicking (its diagnostics are its answer).
func FuzzParse(f *testing.F) {
	f.Add([]byte(kbSchema), []byte("type: note\ntitle: x\n"))
	f.Add([]byte("version: 1\n"), []byte(""))
	f.Add([]byte("version: 1\nfrontmatter:\n  a:\n    type: list\n    item_type: date\n"), []byte("a: [2026-10-29, nope]\n"))
	f.Add([]byte("version: 2\ncommon:\n  kind:\n    type: string\n"), []byte("kind: [1, 2]\n"))
	f.Fuzz(func(t *testing.T, src, fm []byte) {
		s, err := Parse(src)
		if err != nil {
			return
		}
		_ = s.Validate(fm, true)
		_ = s.Validate(nil, false)
	})
}
