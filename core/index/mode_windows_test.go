package index

import (
	"reflect"
	"testing"

	"github.com/yongjohnlee80/golib/parse/markdown"
)

// TestADrivePathRefIsAbsolute: on Windows an absolute ref may name a drive, in the slash form or
// the OS's own, and so may a variable's value. A relation to a path under the workspace root is
// that document; one elsewhere on the drive, under a sibling sharing the root's prefix or on
// another drive is an external link, its path in the slash form. A drive's current directory
// ("C:x.md") is not absolute: it is read as any relative path is.
func TestADrivePathRefIsAbsolute(t *testing.T) {
	src := `---
related:
  - C:/ws/kb/a.md
  - 'C:\ws\kb\b.md'
  - $KB/c.md
  - C:/elsewhere/q.md
  - C:/wsx/a.md
  - 'D:\ws\kb\a.md'
  - $OUT/x.md
---
t
`
	doc := markdown.Parse([]byte(src), markdown.WithGFM(), markdown.WithObsidian())
	vars := NewVars(map[string]string{"KB": `C:\ws\kb`, "OUT": `C:\out`})
	in := func(raw, rel string) DocLink {
		k := exactName(wikiName(rel))
		return DocLink{Raw: raw, Name: k, Kind: "related", Keys: []string{k}}
	}
	out := func(raw, abs string) DocLink {
		return DocLink{Raw: raw, Name: externalPrefix + abs, Kind: "related", Target: abs}
	}
	want := []DocLink{
		in("C:/ws/kb/a.md", "kb/a.md"),
		in(`C:\ws\kb\b.md`, "kb/b.md"),
		in("$KB/c.md", "kb/c.md"),
		out("C:/elsewhere/q.md", "C:/elsewhere/q.md"),
		out("C:/wsx/a.md", "C:/wsx/a.md"),
		out(`D:\ws\kb\a.md`, "D:/ws/kb/a.md"),
		out("$OUT/x.md", "C:/out/x.md"),
	}
	if got := varRelations(doc, "notes/n.md", vars, `C:\ws`); !reflect.DeepEqual(got, want) {
		t.Errorf("relations:\n got %+v\nwant %+v", got, want)
	}
	if rel, ok := inside(`C:\ws`, "C:x.md"); ok {
		t.Errorf("a drive's current directory is inside the root, as %q", rel)
	}
}
