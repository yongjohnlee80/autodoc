package index

import (
	"context"
	"testing"
)

// ResolveLink answers as the index resolved the same link in a file, and answers a link no file
// holds yet (an editor's unsaved one) by the same rules; what the index keeps no link for answers
// nothing.
func TestResolveLinkAnswersAsTheIndex(t *testing.T) {
	e := newEnv(t, Options{})
	e.put(
		"x.md", "root x", "d/x.md", "nested x", "d/y.md", "only y",
		"z.md", "---\naliases: [Zed]\n---\nz",
		"p/v.md", "v one", "q/v.md", "v two",
		"src.md", "[[x]] [[y]] [[zed]] [[v]] [[nothere]]\n",
	)
	ctx := context.Background()
	for _, c := range []struct{ from, raw, path, reason string }{
		{"src.md", "[[x]]", "x.md", ""},
		{"src.md", "[[y]]", "d/y.md", ""},
		{"src.md", "[[zed]]", "z.md", ""},
		{"src.md", "[[v]]", "", ReasonAmbiguous},
		{"src.md", "[[nothere]]", "", ReasonMissing},
		{"src.md", "[[Y|the y]]", "d/y.md", ""},          // in no file yet: typed, not saved
		{"d/rel.md", "[[./x]]", "d/x.md", ""},            // a relative path, from the file's folder
		{"src.md", "[m](d/y)", "d/y.md", ""},             // a Markdown link, with ".md" added
		{"src.md", "[web](https://example.com)", "", ""}, // a URL: no link the index keeps
		{"src.md", "[[#Heading]]", "", ""},               // a heading of src.md itself
	} {
		path, reason, err := e.ix.ResolveLink(ctx, c.from, c.raw)
		if err != nil || path != c.path || reason != c.reason {
			t.Errorf("%s in %s: %q %q %v, want %q %q", c.raw, c.from, path, reason, err, c.path, c.reason)
		}
	}
}
