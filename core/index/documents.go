package index

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// The orders index.documents lists in.
const (
	SortUpdated = "updated" // most recently updated first: the frontmatter's updated, else when the index last read a change
	SortPath    = "path"
	SortIndexed = "indexed" // most recently indexed first
)

// MaxDocumentsPage is the most documents one index.documents call returns.
const MaxDocumentsPage = 500

// DefaultDocumentFields are the frontmatter fields index.documents returns when it is not asked
// for others: what an index entry needs.
var DefaultDocumentFields = []string{"title", "type", "status", "updated", "tags", "abstract"}

// ErrBadSort is an order index.documents does not know.
var ErrBadSort = errors.New("index: sort is updated, path or indexed")

// DocumentsOpts narrows and orders index.documents.
type DocumentsOpts struct {
	Sort    string              // SortUpdated when empty
	Fields  []string            // the frontmatter fields to return; DefaultDocumentFields when nil
	Tags    []string            // a document has every one (its indexed tags: frontmatter and inline)
	Paths   []string            // a document is under one of these folders, or is one of these files
	Facets  map[string][]string // a document has one of the values of each field (the schema's facets)
	Missing []string            // a document lacks one of these frontmatter fields: the backfill's scan
	After   int                 // how many of the ordered documents to skip: the previous page's next
	Limit   int                 // MaxDocumentsPage at most; 100 when zero
}

// DocumentEntry is one document as index.documents lists it.
type DocumentEntry struct {
	Path       string
	Generation int64
	Title      string         // the frontmatter's title, else the first H1, else the file name
	Updated    string         // the frontmatter's updated as written, else IndexedAt as an RFC 3339 time
	IndexedAt  int64          // when the index last read a change to it, in Unix seconds
	Fields     map[string]any // the asked-for frontmatter fields it has
}

// listed is one document while index.documents filters and orders it.
type listed struct {
	id      int64
	entry   DocumentEntry
	front   map[string]any
	updated time.Time
}

// Documents lists the workspace's documents with their frontmatter: the index a person or an agent
// reads instead of a hand-kept one, in the order asked for, narrowed by tags, paths, facets and
// missing fields, one page at a time. The second result says whether more follow.
func (s *Store) Documents(ctx context.Context, o DocumentsOpts) ([]DocumentEntry, bool, error) {
	switch o.Sort {
	case "":
		o.Sort = SortUpdated
	case SortUpdated, SortPath, SortIndexed:
	default:
		return nil, false, ErrBadSort
	}
	if o.Limit <= 0 {
		o.Limit = 100
	}
	o.Limit = min(o.Limit, MaxDocumentsPage)
	o.After = max(o.After, 0)
	fields := o.Fields
	if fields == nil {
		fields = DefaultDocumentFields
	}

	var all []listed
	tags := map[int64]map[string]bool{}
	facets := map[int64]map[string]map[string]bool{}
	err := s.read(ctx, func(tx *store.Tx) error {
		docs, err := s.sc.Documents(tx).Select(store.DocID, store.DocPath, store.DocActiveGen, store.DocTitle,
			store.DocFrontmatterJSON, store.DocIndexedAt)
		if err != nil {
			return err
		}
		for _, d := range docs {
			l := listed{id: d.ID, entry: DocumentEntry{Path: d.Path, Generation: d.ActiveGen, IndexedAt: d.IndexedAt}}
			if d.Title != nil {
				l.entry.Title = *d.Title
			}
			if d.FrontmatterJSON != nil && *d.FrontmatterJSON != "" {
				_ = json.Unmarshal([]byte(*d.FrontmatterJSON), &l.front) // unparsed frontmatter lists as none
			}
			all = append(all, l)
		}
		if len(o.Tags) > 0 {
			rows, err := s.sc.Tags(tx).Select(store.DocValueDoc, store.DocValueValue)
			if err != nil {
				return err
			}
			for _, t := range rows {
				if tags[t.DocID] == nil {
					tags[t.DocID] = map[string]bool{}
				}
				tags[t.DocID][strings.ToLower(t.Value)] = true
			}
		}
		if len(o.Facets) > 0 {
			rows, err := s.sc.Facets(tx).Select(store.FacetDoc, store.FacetName, store.FacetValue)
			if err != nil {
				return err
			}
			for _, f := range rows {
				if facets[f.DocID] == nil {
					facets[f.DocID] = map[string]map[string]bool{}
				}
				if facets[f.DocID][f.Field] == nil {
					facets[f.DocID][f.Field] = map[string]bool{}
				}
				facets[f.DocID][f.Field][f.Value] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}

	kept := all[:0]
	for _, l := range all {
		if !underAny(l.entry.Path, o.Paths) || !hasEvery(tags[l.id], o.Tags) || !matchesFacets(facets[l.id], o.Facets) ||
			!missesOne(l.front, o.Missing) {
			continue
		}
		l.updated = time.Unix(l.entry.IndexedAt, 0).UTC()
		l.entry.Updated = l.updated.Format(time.RFC3339)
		if v, ok := l.front["updated"]; ok {
			if t, ok := frontDate(v); ok {
				l.updated = t
				l.entry.Updated = strings.TrimSpace(toString(v))
			}
		}
		kept = append(kept, l)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		switch o.Sort {
		case SortUpdated:
			if !a.updated.Equal(b.updated) {
				return a.updated.After(b.updated)
			}
		case SortIndexed:
			if a.entry.IndexedAt != b.entry.IndexedAt {
				return a.entry.IndexedAt > b.entry.IndexedAt
			}
		}
		return a.entry.Path < b.entry.Path
	})
	if o.After >= len(kept) {
		return []DocumentEntry{}, false, nil
	}
	page := kept[o.After:]
	more := len(page) > o.Limit
	if more {
		page = page[:o.Limit]
	}
	out := make([]DocumentEntry, len(page))
	for i, l := range page {
		e := l.entry
		e.Fields = map[string]any{}
		for _, f := range fields {
			if v, ok := l.front[f]; ok {
				e.Fields[f] = v
			}
		}
		out[i] = e
	}
	return out, more, nil
}

// underAny reports whether p is under one of the folders, or is one of the files, in prefixes; no
// prefixes admit every path.
func underAny(p string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, pre := range prefixes {
		pre = strings.TrimSuffix(path.Clean(pre), "/")
		if pre == "." || p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// hasEvery reports whether have holds every one of want, compared lowercased as tags are indexed.
func hasEvery(have map[string]bool, want []string) bool {
	for _, w := range want {
		if !have[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

// matchesFacets reports whether a document's facets hold one of the values of each asked field.
func matchesFacets(have map[string]map[string]bool, want map[string][]string) bool {
	for field, values := range want {
		found := false
		for _, v := range values {
			if have[field][v] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// missesOne reports whether front lacks one of the fields (an empty value counts as missing); no
// fields admit every document.
func missesOne(front map[string]any, fields []string) bool {
	if len(fields) == 0 {
		return true
	}
	for _, f := range fields {
		v, ok := front[f]
		if list, isList := v.([]any); isList && len(list) == 0 {
			return true
		}
		if !ok || v == nil || strings.TrimSpace(toString(v)) == "" {
			return true
		}
	}
	return false
}

// frontDate reads a frontmatter date: YYYY-MM-DD, or an RFC 3339 time.
func frontDate(v any) (time.Time, bool) {
	s := strings.TrimSpace(toString(v))
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// toString is a frontmatter scalar as text; a list or a map is its JSON.
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
