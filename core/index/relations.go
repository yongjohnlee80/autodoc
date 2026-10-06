package index

import (
	"encoding/json"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/parse/markdown"
	"github.com/yongjohnlee80/golib/search/chunk"
)

// RelationFields are the frontmatter fields whose values name other documents. Each value that
// names one becomes a link whose kind is its field: "supersedes", "amends", and so on.
var RelationFields = []string{"supersedes", "superseded_by", "amends", "related", "sources", "adr"}

// BodyKinds are the kinds of link a document's body makes, the only ones before relations.
var BodyKinds = []string{LinkWikilink, LinkEmbed, LinkMarkdown}

// isRelation reports whether a link of kind came from a frontmatter relation field.
func isRelation(kind string) bool { return slices.Contains(RelationFields, kind) }

// ReasonCycle is why a superseded document is not moved below its successor: the supersession
// relations among its documents go round in a loop, which has no order to follow.
const ReasonCycle = "cycle"

// adrPrefix starts the name a decision record answers to by its number.
const adrPrefix = "#adr:"

// adrKey is the name of decision record number n: its digits without leading zeros, so "0021"
// and "21" name the same record.
func adrKey(n string) string {
	n = strings.TrimLeft(strings.TrimSpace(n), "0")
	if n == "" {
		n = "0"
	}
	return adrPrefix + n
}

// adrNumber is the number a document of type adr carries in its frontmatter, read from the
// evaluated frontmatter so a workspace with no schema has it too; "" for anything else.
func adrNumber(frontmatterJSON string) string {
	if frontmatterJSON == "" {
		return ""
	}
	var fm struct {
		Type   any `json:"type"`
		Number any `json:"number"`
	}
	if json.Unmarshal([]byte(frontmatterJSON), &fm) != nil || fm.Type != "adr" {
		return ""
	}
	switch n := fm.Number.(type) {
	case string:
		if allDigits(n) {
			return n
		}
	case float64:
		if n >= 0 && n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
	}
	return ""
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// extractRelations takes the relation values of file p's frontmatter that name a document, in
// source order. A value's names are tried in order when it is resolved: a path from the workspace
// root first, then from p's own folder; a wikilink or bare slug by the wikilink names; a number as
// a decision record's. URLs and prose name no document and are left out.
func extractRelations(doc *markdown.Document, p string) []linkT {
	var out []linkT
	for _, r := range chunk.Relations(doc, RelationFields) {
		ref := chunk.ParseRef(r.Raw)
		var keys []string
		switch ref.Form {
		case chunk.RefPath:
			keys = pathKeys(p, ref.Target)
		case chunk.RefWikilink, chunk.RefSlug:
			if k := wikiKey(p, ref.Target); k != "" {
				keys = []string{k}
			}
		case chunk.RefNumber:
			keys = []string{adrKey(ref.Target)}
		}
		if len(keys) == 0 {
			continue
		}
		out = append(out, linkT{raw: r.Raw, name: keys[0], anchor: ref.Anchor, kind: r.Field, keys: keys})
	}
	return out
}

// pathKeys are the names a relation path is looked up under: the path from the workspace root, then
// from p's folder, each as the exact name of that path. An absolute path, or one that leaves the
// workspace, names nothing in it.
func pathKeys(p, target string) []string {
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, "~") {
		return nil
	}
	var keys []string
	add := func(q string) {
		q = path.Clean(q)
		if q == "." || q == ".." || strings.HasPrefix(q, "../") {
			return
		}
		if k := exactName(wikiName(q)); !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	add(target)
	add(path.Join(path.Dir(p), target))
	return keys
}

// wikiKey is the name a relation's wikilink or slug is looked up under, as a body wikilink's is: a
// relative page names one path, anything else a file name, path suffix or alias.
func wikiKey(p, page string) string {
	page = strings.TrimSpace(page)
	if page == "" {
		return ""
	}
	if strings.HasPrefix(page, "./") || strings.HasPrefix(page, "../") {
		target := path.Join(path.Dir(p), page)
		if target == ".." || strings.HasPrefix(target, "../") {
			return ""
		}
		return exactName(wikiName(target))
	}
	return wikiName(page)
}
