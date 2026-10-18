package kbtype

import (
	"bytes"
	"path"
	"regexp"
	"strconv"
)

// Vars fill a tree/ file's AutoDoc placeholders, anywhere in it: {{autodoc.date}},
// {{autodoc.version}}, {{kb_type.name}}, {{kb_type.url}} and {{kb_type.commit}}. They are
// namespaced so a template's Obsidian variables ({{date}}, {{title}}) pass through untouched.
type Vars struct {
	Date           string // YYYY-MM-DD
	AutoDocVersion string
	URL            string // where the type was cloned from
	Commit         string // the commit cloned, in full
}

// Write is one file to create at the KB root.
type Write struct {
	Rel       string // slash path
	Text      []byte // rendered
	Managed   bool   // KB_OPERATIONS.md: replaced when the KB's copy declares an older autodoc_version
	Immutable string // the immutable folder whose descriptor this is, "" for none
}

// Plan is every file t creates, in tree order, each folder's descriptor before its other files,
// then AutoDoc's own KB_OPERATIONS.md (operations, rendered as the Neovim side renders it). An
// immutable folder's descriptor carries that folder: the daemon skips it unless the folder is
// absent or empty (workspace.scaffold).
func (t *Type) Plan(v Vars, operations []byte) []Write {
	immutable := map[string]bool{}
	for _, f := range t.Folders {
		if f.Immutable {
			immutable[f.Path] = true
		}
	}
	out := make([]Write, 0, len(t.Files)+1)
	for _, rel := range t.sortedFiles() {
		w := Write{Rel: rel, Text: v.render(t.Name, t.Files[rel])}
		if d := path.Dir(rel); immutable[d] && rel == Descriptor(d) {
			w.Immutable = d
		}
		out = append(out, w)
	}
	if operations != nil {
		out = append(out, Write{Rel: Operations, Text: v.renderOperations(operations), Managed: true})
	}
	return out
}

func (v Vars) render(name string, b []byte) []byte {
	return replaceAll(b,
		"{{autodoc.date}}", v.Date,
		"{{autodoc.version}}", v.AutoDocVersion,
		"{{kb_type.name}}", name,
		"{{kb_type.url}}", v.URL,
		"{{kb_type.commit}}", v.Commit)
}

// renderOperations renders KB_OPERATIONS.md as scaffold.lua's render does: {{autodoc_version}}
// anywhere, {{date}} only on the frontmatter's created line (elsewhere it is an editor's variable).
func (v Vars) renderOperations(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("{{autodoc_version}}"), []byte(v.AutoDocVersion))
	return bytes.Replace(b, []byte("\ncreated: {{date}}\n"), []byte("\ncreated: "+v.Date+"\n"), 1)
}

func replaceAll(b []byte, pairs ...string) []byte {
	for i := 0; i+1 < len(pairs); i += 2 {
		b = bytes.ReplaceAll(b, []byte(pairs[i]), []byte(pairs[i+1]))
	}
	return b
}

var (
	declaredFrontmatter = regexp.MustCompile(`\nautodoc_version:\s*"?([^"\n]*?)"?\s*\n`)
	declaredComment     = regexp.MustCompile(`^#\s*autodoc_version:\s*([^\n]*?)\s*\n`)
	semverPrefix        = regexp.MustCompile(`^\s*v?(\d+)\.(\d+)\.(\d+)`)
)

// DeclaredVersion is a managed file's autodoc_version: a frontmatter autodoc_version: line, or a
// leading "# autodoc_version:" comment; "" for none (scaffold.lua's declared_version).
func DeclaredVersion(text []byte) string {
	if m := declaredFrontmatter.FindSubmatch(text); m != nil {
		return string(m[1])
	}
	if m := declaredComment.FindSubmatch(text); m != nil {
		return string(m[1])
	}
	return ""
}

// Older reports whether version have is older than want; ok is false when either does not start
// with MAJOR.MINOR.PATCH (a leading v allowed), and then nothing is older (scaffold.lua's
// semver_cmp: an unreadable version is kept).
func Older(have, want string) (older, ok bool) {
	a, okA := semver(have)
	b, okB := semver(want)
	if !okA || !okB {
		return false, false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i], true
		}
	}
	return false, true
}

func semver(v string) ([3]int, bool) {
	var out [3]int
	m := semverPrefix.FindStringSubmatch(v)
	if m == nil {
		return out, false
	}
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
