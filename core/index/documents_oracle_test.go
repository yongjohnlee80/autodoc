package index

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// listed is one document while the oracle filters and orders it.
type listed struct {
	id      int64
	entry   DocumentEntry
	front   map[string]any
	updated time.Time
}

// oracleDocuments is index.documents as it was before
// docs/decisions/search.md#document-listing-in-the-store (core/index/documents.go at
// e797f45), kept verbatim but for its offset field, as the parity oracle
// (docs/decisions/search.md#document-listing-in-the-store): it reads every document, filters and
// sorts in Go, and pages by an offset.
//
// Documents lists the workspace's documents with their frontmatter: the index a person or an agent
// reads instead of a hand-kept one, in the order asked for, narrowed by tags, paths, facets and
// missing fields, one page at a time. The second result says whether more follow.
func (s *Store) oracleDocuments(ctx context.Context, o DocumentsOpts, after int) ([]DocumentEntry, bool, error) {
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
	after = max(after, 0)
	fields := o.Fields
	if fields == nil {
		fields = DefaultDocumentFields
	}

	var all []listed
	tags := map[int64]map[string]bool{}
	facets := map[int64]map[string]map[string]bool{}
	diags := map[int64][]Diagnostic{}
	err := s.read(ctx, func(tx *store.Tx) error {
		docs, err := s.documents(tx).Select(DocID, DocPath, DocActiveGen, DocTitle,
			DocFrontmatterJSON, DocIndexedAt)
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
			rows, err := s.tags(tx).Select(DocValueDoc, DocValueValue)
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
		if o.Diagnosed {
			rows, err := s.diagnostics(tx).Select(DiagDoc, DiagOrd, DiagField, DiagLine,
				DiagRule, DiagMessage)
			if err != nil {
				return err
			}
			for _, d := range rows {
				diags[d.DocID] = append(diags[d.DocID], *d)
			}
		}
		if len(o.Facets) > 0 {
			rows, err := s.facets(tx).Select(FacetDoc, FacetName, FacetValue)
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
		if !oracleUnderAny(l.entry.Path, o.Paths) || !oracleHasEvery(tags[l.id], o.Tags) || !oracleMatchesFacets(facets[l.id], o.Facets) ||
			!oracleMissesOne(l.front, o.Missing) || (o.Diagnosed && len(diags[l.id]) == 0) {
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
	if after >= len(kept) {
		return []DocumentEntry{}, false, nil
	}
	page := kept[after:]
	more := len(page) > o.Limit
	if more {
		page = page[:o.Limit]
	}
	out := make([]DocumentEntry, len(page))
	for i, l := range page {
		e := l.entry
		if o.Diagnosed {
			ds := diags[l.id]
			sort.Slice(ds, func(i, j int) bool { return ds[i].Ord < ds[j].Ord })
			e.Diagnostics = make([]schema.Diagnostic, len(ds))
			for k, d := range ds {
				e.Diagnostics[k] = schema.Diagnostic{Field: d.Field, Line: int(d.Line), Rule: d.Rule, Message: d.Message}
			}
		}
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

// The oracle's own filters, as they were at e797f45: the code under test shares none of them, so a
// mutant of its normalising cannot change the oracle too.

// underAny reports whether p is under one of the folders, or is one of the files, in prefixes; no
// prefixes admit every path.
func oracleUnderAny(p string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, pre := range prefixes {
		pre = strings.TrimSuffix(path.Clean(pre), "/")
		if paths.Under(p, pre) {
			return true
		}
	}
	return false
}

// hasEvery reports whether have holds every one of want, compared lowercased as tags are indexed.
func oracleHasEvery(have map[string]bool, want []string) bool {
	for _, w := range want {
		if !have[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

// matchesFacets reports whether a document's facets hold one of the values of each asked field.
func oracleMatchesFacets(have map[string]map[string]bool, want map[string][]string) bool {
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
func oracleMissesOne(front map[string]any, fields []string) bool {
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
