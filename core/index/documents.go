package index

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// INDEX.DOCUMENTS (docs/decisions/search.md#document-listing-in-the-store) — the order and the
// cheap filters run in SQL, a page resumes after the last document it returned by that document's
// sort key, and no statement skips rows by an offset:
//
//   - every order is the ascending order of a key stored on the document (D1, D2): path; (k_upd_s,
//     k_upd_ns, path), the updated instant's seconds and nanoseconds negated; (k_indexed, path),
//     indexed_at negated. docKeys computes them, for the writer, the backfill and the fallback alike;
//   - paths, tags, facets and diagnosed are SQL predicates; missing stays in Go (its blank rule is
//     Go's), over candidates read in windows, and limit+1 counts accepted documents;
//   - next is an opaque cursor: the sort, the last returned document's key and a hash of the
//     normalised filters (D3). Another sort's or other filters' cursor, or an integer, is refused;
//   - until every document of the workspace has its keys (rows from before 000027, backfilled at
//     start), a sort that needs them takes the Go fallback, which orders by the same key and resumes
//     after the same cursor, in the same read transaction as the gate that chose it.

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
var ErrBadSort = errs.Sentinel(errs.ErrInvalidArgument, "index: sort is updated, path or indexed")

// ErrBadCursor is an after that is not a cursor of this listing: not one at all, or one from
// another sort or other filters.
var ErrBadCursor = errs.Sentinel(errs.ErrInvalidArgument, "index: after is not a cursor of this listing")

// DocumentsOpts narrows and orders index.documents.
type DocumentsOpts struct {
	Sort    string              // SortUpdated when empty
	Fields  []string            // the frontmatter fields to return; DefaultDocumentFields when nil
	Tags    []string            // a document has every one (its indexed tags: frontmatter and inline)
	Paths   []string            // a document is under one of these folders, or is one of these files
	Facets  map[string][]string // a document has one of the values of each field (the schema's facets)
	Missing []string            // a document lacks one of these frontmatter fields: the backfill's scan
	// Diagnosed keeps the documents whose frontmatter the schema diagnoses (malformed YAML, a missing
	// or refused field), each with its diagnostics: the scan for every problem, not only absences.
	Diagnosed bool
	After     string // the previous page's Next; "" from the start
	Limit     int    // MaxDocumentsPage at most; 100 when zero
}

// DocumentEntry is one document as index.documents lists it.
type DocumentEntry struct {
	Path       string
	Generation int64
	Title      string         // the frontmatter's title, else the first H1, else the file name
	Updated    string         // the frontmatter's updated as written, else IndexedAt as an RFC 3339 time
	IndexedAt  int64          // when the index last read a change to it, in Unix seconds
	Fields     map[string]any // the asked-for frontmatter fields it has
	// Diagnostics is what the schema found wrong with its frontmatter, in source order; filled only
	// when DocumentsOpts.Diagnosed asked for them.
	Diagnostics []schema.Diagnostic
}

// DocumentsPage is one page of index.documents.
type DocumentsPage struct {
	Docs []DocumentEntry
	More bool   // more documents follow
	Next string // the cursor the next page starts after; "" when none follow
}

// docKey is a document's place in one order: the order's integers, then its path. Every order is
// this key ascending; an order with fewer integers leaves the rest 0.
type docKey struct {
	a, b int64
	path string
}

// less is k before o.
func (k docKey) less(o docKey) bool {
	if k.a != o.a {
		return k.a < o.a
	}
	if k.b != o.b {
		return k.b < o.b
	}
	return k.path < o.path
}

// docKeys are a document's stored ascending sort keys (D2): the updated instant's Unix seconds and
// nanoseconds, negated (seconds and nanoseconds are exact over schema.ParseDate's years 0000–9999,
// where UnixNano overflows), and indexed_at, negated.
func docKeys(front map[string]any, indexedAt int64) (updS, updNs, indexed int64) {
	t, _ := updatedOf(front, indexedAt)
	return -t.Unix(), -int64(t.Nanosecond()), -indexedAt
}

// docKeysOf is docKeys of a document's stored frontmatter JSON.
func docKeysOf(frontJSON *string, indexedAt int64) (updS, updNs, indexed int64) {
	return docKeys(frontOf(frontJSON), indexedAt)
}

// updatedOf is a document's updated instant (UTC) and its text: the frontmatter's updated when
// frontDate reads it, as written; else indexed_at, as RFC 3339.
func updatedOf(front map[string]any, indexedAt int64) (time.Time, string) {
	if v, ok := front["updated"]; ok {
		if t, ok := frontDate(v); ok {
			return t, strings.TrimSpace(toString(v))
		}
	}
	t := time.Unix(indexedAt, 0).UTC()
	return t, t.Format(time.RFC3339)
}

// frontOf is a stored frontmatter JSON as a map; nil or unparsed frontmatter is none.
func frontOf(frontJSON *string) map[string]any {
	var front map[string]any
	if frontJSON != nil && *frontJSON != "" {
		_ = json.Unmarshal([]byte(*frontJSON), &front) // unparsed frontmatter lists as none
	}
	return front
}

// keyOf is a document's key in order sort, from its stored or computed keys.
func keyOf(sort, p string, updS, updNs, indexed int64) docKey {
	switch sort {
	case SortUpdated:
		return docKey{a: updS, b: updNs, path: p}
	case SortIndexed:
		return docKey{a: indexed, path: p}
	}
	return docKey{path: p}
}

// filters are index.documents' filters, normalised: the form the SQL, the fallback and the cursor's
// hash read.
type filters struct {
	All       bool                `json:"all"` // no paths, or one that is the root: every path
	Dirs      []string            `json:"dirs,omitempty"`
	Tags      []string            `json:"tags,omitempty"`
	Facets    map[string][]string `json:"facets,omitempty"`
	Missing   []string            `json:"missing,omitempty"`
	Diagnosed bool                `json:"diagnosed,omitempty"`
}

// normPath is a paths entry as underAny reads it: cleaned, its trailing slash trimmed. "." (from ".",
// "./" or "") is the root.
func normPath(p string) string { return strings.TrimSuffix(path.Clean(p), "/") }

// sortedSet is vs sorted, without repeats.
func sortedSet(vs []string) []string {
	out := slices.Clone(vs)
	slices.Sort(out)
	return slices.Compact(out)
}

// normFilters reads o's filters into their one normal form.
func normFilters(o DocumentsOpts) filters {
	f := filters{All: len(o.Paths) == 0, Missing: sortedSet(o.Missing), Diagnosed: o.Diagnosed}
	for _, p := range o.Paths {
		n := normPath(p)
		if n == "." { // the root admits every path, so the whole filter goes
			f.All, f.Dirs = true, nil
			break
		}
		f.Dirs = append(f.Dirs, n)
	}
	f.Dirs = sortedSet(f.Dirs)
	for _, t := range o.Tags {
		f.Tags = append(f.Tags, strings.ToLower(t))
	}
	f.Tags = sortedSet(f.Tags)
	if len(o.Facets) > 0 {
		f.Facets = map[string][]string{}
		for field, values := range o.Facets {
			f.Facets[field] = sortedSet(values)
		}
	}
	return f
}

// hash is a fingerprint of the filters for a cursor: a cursor is good only for the filters it was
// made under.
func (f filters) hash() string {
	b, _ := json.Marshal(f) // maps marshal with sorted keys
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// cursor is index.documents' next, decoded: the sort, the last returned document's key and the
// filters' hash.
type cursor struct {
	Sort   string `json:"s"`
	A      int64  `json:"a,omitempty"`
	B      int64  `json:"b,omitempty"`
	Path   string `json:"p"`
	Filter string `json:"f"`
}

// cursorPrefix marks a cursor's encoding, so another can follow.
const cursorPrefix = "c1."

func encodeCursor(sort string, k docKey, f string) string {
	b, _ := json.Marshal(cursor{Sort: sort, A: k.a, B: k.b, Path: k.path, Filter: f})
	return cursorPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor is the key after reads from, for a listing of sort under filters with hash f.
func decodeCursor(after, sort, f string) (docKey, error) {
	raw, ok := strings.CutPrefix(after, cursorPrefix)
	if !ok {
		return docKey{}, fmt.Errorf("%w: not a next index.documents answered", ErrBadCursor)
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	var c cursor
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	switch {
	case err != nil:
		return docKey{}, fmt.Errorf("%w: not a next index.documents answered", ErrBadCursor)
	case c.Sort != sort:
		return docKey{}, fmt.Errorf("%w: it is from sort %q, not %q", ErrBadCursor, c.Sort, sort)
	case c.Filter != f:
		return docKey{}, fmt.Errorf("%w: it is from other filters (tags, paths, facets, missing or diagnosed)", ErrBadCursor)
	}
	return docKey{a: c.A, b: c.B, path: c.Path}, nil
}

// candidate is one document a page considers.
type candidate struct {
	id        int64
	path      string
	gen       int64
	title     *string
	front     map[string]any
	indexedAt int64
	key       docKey
}

// documentColumns are the columns a page reads of each candidate.
var documentColumns = []DocumentField{DocID, DocPath, DocActiveGen, DocTitle, DocFrontmatterJSON, DocIndexedAt,
	DocKUpdS, DocKUpdNs, DocKIndexed}

// Documents lists the workspace's documents with their frontmatter: the index a person or an agent
// reads instead of a hand-kept one, in the order asked for, narrowed by tags, paths, facets and
// missing fields, one page at a time, each page after the last one's Next.
func (s *Store) Documents(ctx context.Context, o DocumentsOpts) (DocumentsPage, error) {
	switch o.Sort {
	case "":
		o.Sort = SortUpdated
	case SortUpdated, SortPath, SortIndexed:
	default:
		return DocumentsPage{}, ErrBadSort
	}
	if o.Limit <= 0 {
		o.Limit = 100
	}
	o.Limit = min(o.Limit, MaxDocumentsPage)
	fields := o.Fields
	if fields == nil {
		fields = DefaultDocumentFields
	}
	f := normFilters(o)
	fh := f.hash()
	var from *docKey
	if o.After != "" {
		k, err := decodeCursor(o.After, o.Sort, fh)
		if err != nil {
			return DocumentsPage{}, err
		}
		from = &k
	}

	var page []candidate
	var more bool
	diags := map[int64][]Diagnostic{}
	err := s.read(ctx, func(tx *store.Tx) error {
		var err error
		fallback := false
		if o.Sort != SortPath {
			// the gate and the page in one read transaction: a page is never half of each path
			if fallback, err = s.keysUnset(tx); err != nil {
				return err
			}
		}
		if fallback {
			page, more, err = s.documentsGo(tx, o.Sort, f, from, o.Limit)
		} else {
			page, more, err = s.documentsSQL(tx, o.Sort, f, from, o.Limit)
		}
		if err != nil || !o.Diagnosed || len(page) == 0 {
			return err
		}
		ids := make([]int64, len(page))
		for i, c := range page {
			ids[i] = c.id
		}
		for part := range inParts(ids) {
			rows, err := s.diagnostics(tx).With(DiagDoc, part...).Select(DiagDoc, DiagOrd, DiagField, DiagLine,
				DiagRule, DiagMessage)
			if err != nil {
				return err
			}
			for _, d := range rows {
				diags[d.DocID] = append(diags[d.DocID], *d)
			}
		}
		return nil
	})
	if err != nil {
		return DocumentsPage{}, err
	}

	out := DocumentsPage{Docs: make([]DocumentEntry, len(page)), More: more}
	for i, c := range page {
		e := DocumentEntry{Path: c.path, Generation: c.gen, IndexedAt: c.indexedAt}
		if c.title != nil {
			e.Title = *c.title
		}
		_, e.Updated = updatedOf(c.front, c.indexedAt)
		if o.Diagnosed {
			ds := diags[c.id]
			sort.Slice(ds, func(i, j int) bool { return ds[i].Ord < ds[j].Ord })
			e.Diagnostics = make([]schema.Diagnostic, len(ds))
			for k, d := range ds {
				e.Diagnostics[k] = schema.Diagnostic{Field: d.Field, Line: int(d.Line), Rule: d.Rule, Message: d.Message}
			}
		}
		e.Fields = map[string]any{}
		for _, fl := range fields {
			if v, ok := c.front[fl]; ok {
				e.Fields[fl] = v
			}
		}
		out.Docs[i] = e
	}
	if more {
		out.Next = encodeCursor(o.Sort, page[len(page)-1].key, fh)
	}
	return out, nil
}

// keysUnset reports whether a document of the workspace has no stored sort keys yet (written before
// 000027 and not backfilled): the gate of the Go fallback, answered by document_k_unset.
func (s *Store) keysUnset(tx *store.Tx) (bool, error) {
	rows, err := s.documents(tx).WithPredicate(dao.IsNull(`"document"."k_upd_s"`)).Limit(1).Select(DocID)
	return len(rows) > 0, err
}

// The SQL of each order: its ORDER BY, and the row-value predicate of a key after a cursor's.
var (
	orderOf = map[string]noSort{SortPath: byPath, SortUpdated: byUpdatedKey, SortIndexed: byIndexedKey}
	seekOf  = map[string]func(k docKey) dao.Predicate{
		SortPath: func(k docKey) dao.Predicate { return store.RowAfter([]string{`"document"."path"`}, k.path) },
		SortUpdated: func(k docKey) dao.Predicate {
			return store.RowAfter([]string{`"document"."k_upd_s"`, `"document"."k_upd_ns"`, `"document"."path"`}, k.a, k.b, k.path)
		},
		SortIndexed: func(k docKey) dao.Predicate {
			return store.RowAfter([]string{`"document"."k_indexed"`, `"document"."path"`}, k.a, k.path)
		},
	}
)

// filtersSQL are f's filters but missing as predicates on document: path ranges, and an EXISTS on
// doc_tag, doc_facet and doc_diagnostic, each keyed by the document. none is a filter nothing can
// pass (a facet asked with no values).
func filtersSQL(f filters) (ps []dao.Predicate, none bool) {
	if !f.All {
		ors := make([]dao.Predicate, len(f.Dirs))
		for i, d := range f.Dirs {
			ors[i] = under(`"document"."path"`, d)
		}
		ps = append(ps, dao.Or(ors...))
	}
	for _, t := range f.Tags {
		ps = append(ps, store.MatchIn("doc_tag", "doc_id", "document", "id", dao.Eq(`"doc_tag"."tag"`, t)))
	}
	fieldNames := make([]string, 0, len(f.Facets))
	for field := range f.Facets {
		fieldNames = append(fieldNames, field)
	}
	slices.Sort(fieldNames)
	for _, field := range fieldNames {
		values := f.Facets[field]
		if len(values) == 0 {
			return nil, true
		}
		vals := make([]any, len(values))
		for i, v := range values {
			vals[i] = v
		}
		ps = append(ps, store.MatchIn("doc_facet", "doc_id", "document", "id", dao.Eq(`"doc_facet"."field"`, field),
			dao.In(`"doc_facet"."value"`, vals)))
	}
	if f.Diagnosed {
		ps = append(ps, store.MatchIn("doc_diagnostic", "doc_id", "document", "id"))
	}
	return ps, false
}

// missingWindow is how many candidates one read takes while missing filters in Go: inPart (a test
// makes it small, to separate matches by more than one window with few documents).
var missingWindow = inPart

// windowOf is how many candidates one read of the SQL path takes: a page and its look-ahead, or,
// when missing filters in Go, missingWindow, read again while too few pass.
func windowOf(f filters, limit int) int {
	if len(f.Missing) > 0 {
		return missingWindow
	}
	return limit + 1
}

// documentsSQL is a page in SQL: the filters and the order by an index, after from, read a window
// at a time while missing rejects candidates, until limit+1 are accepted or none are left.
func (s *Store) documentsSQL(tx *store.Tx, sort string, f filters, from *docKey, limit int) ([]candidate, bool, error) {
	ps, none := filtersSQL(f)
	if none {
		return nil, false, nil
	}
	window := windowOf(f, limit)
	var out []candidate
	for {
		d := s.documents(tx)
		for _, p := range ps {
			d = d.WithPredicate(p)
		}
		if from != nil {
			d = d.WithPredicate(seekOf[sort](*from))
		}
		rows, err := d.OrderBy(dao.Asc(orderOf[sort])).Limit(uint64(window)).Select(documentColumns...)
		if err != nil {
			return nil, false, err
		}
		for _, r := range rows {
			c := candidate{id: r.ID, path: r.Path, gen: r.ActiveGen, title: r.Title, front: frontOf(r.FrontmatterJSON),
				indexedAt: r.IndexedAt, key: keyOf(sort, r.Path, derefInt(r.KUpdS), derefInt(r.KUpdNs), derefInt(r.KIndexed))}
			from = &c.key
			if !missesOne(c.front, f.Missing) {
				continue
			}
			if out = append(out, c); len(out) > limit {
				return out[:limit], true, nil
			}
		}
		if len(rows) < window {
			return out, false, nil
		}
	}
}

// documentsGo is a page while some document has no stored keys: every document read, filtered and
// keyed in Go (docKeys, the writer's own function), ordered by key, and taken from the first key
// after from.
func (s *Store) documentsGo(tx *store.Tx, sort string, f filters, from *docKey, limit int) ([]candidate, bool, error) {
	docs, err := s.documents(tx).Select(documentColumns...)
	if err != nil {
		return nil, false, err
	}
	tags := map[int64]map[string]bool{}
	if len(f.Tags) > 0 {
		rows, err := s.tags(tx).Select(DocValueDoc, DocValueValue)
		if err != nil {
			return nil, false, err
		}
		for _, t := range rows {
			if tags[t.DocID] == nil {
				tags[t.DocID] = map[string]bool{}
			}
			tags[t.DocID][t.Value] = true
		}
	}
	facets := map[int64]map[string]map[string]bool{}
	if len(f.Facets) > 0 {
		rows, err := s.facets(tx).Select(FacetDoc, FacetName, FacetValue)
		if err != nil {
			return nil, false, err
		}
		for _, fc := range rows {
			if facets[fc.DocID] == nil {
				facets[fc.DocID] = map[string]map[string]bool{}
			}
			if facets[fc.DocID][fc.Field] == nil {
				facets[fc.DocID][fc.Field] = map[string]bool{}
			}
			facets[fc.DocID][fc.Field][fc.Value] = true
		}
	}
	diagnosed := map[int64]bool{}
	if f.Diagnosed {
		rows, err := s.diagnostics(tx).Select(DiagDoc)
		if err != nil {
			return nil, false, err
		}
		for _, d := range rows {
			diagnosed[d.DocID] = true
		}
	}
	var kept []candidate
	for _, r := range docs {
		c := candidate{id: r.ID, path: r.Path, gen: r.ActiveGen, title: r.Title, front: frontOf(r.FrontmatterJSON),
			indexedAt: r.IndexedAt}
		if !underAny(c.path, f) || !hasEvery(tags[c.id], f.Tags) || !matchesFacets(facets[c.id], f.Facets) ||
			!missesOne(c.front, f.Missing) || (f.Diagnosed && !diagnosed[c.id]) {
			continue
		}
		updS, updNs, indexed := docKeys(c.front, c.indexedAt)
		c.key = keyOf(sort, c.path, updS, updNs, indexed)
		kept = append(kept, c)
	}
	slices.SortFunc(kept, func(x, y candidate) int {
		switch {
		case x.key.less(y.key):
			return -1
		case y.key.less(x.key):
			return 1
		}
		return 0
	})
	start := 0
	if from != nil {
		start, _ = slices.BinarySearchFunc(kept, *from, func(c candidate, k docKey) int {
			if !k.less(c.key) {
				return -1 // at or before the cursor
			}
			return 1
		})
	}
	page := kept[start:]
	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

// underAny reports whether p passes f's path filter: under one of its folders, or one of its files.
func underAny(p string, f filters) bool {
	if f.All {
		return true
	}
	for _, d := range f.Dirs {
		if paths.Under(p, d) {
			return true
		}
	}
	return false
}

// hasEvery reports whether have holds every one of want; both are lowercased, as tags are indexed.
func hasEvery(have map[string]bool, want []string) bool {
	for _, w := range want {
		if !have[w] {
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
	t, ok := schema.ParseDate(strings.TrimSpace(toString(v)))
	return t.UTC(), ok
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
