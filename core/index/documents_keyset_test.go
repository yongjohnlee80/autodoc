package index

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// ADR 1793312720 §5: index.documents in SQL, paged by key, against the oracle (the Go listing it
// replaced, documents_oracle_test.go), on both of its paths: the SQL one, once every document has
// its keys, and the Go fallback, while one has none.

// keysetPaths are the generated workspace's files: several depths, prefixes that overlap by
// characters but not by segments (a/b and a/bc), and names that run against their folders' order,
// so ids, paths and keys disagree.
func keysetPaths(n int) []string {
	dirs := []string{"", "a/", "a/b/", "a/b/c/", "a/bc/", "notes/", "ab/"}
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%sd%02d.md", dirs[i%len(dirs)], (i*7)%n) // 7 and n coprime: a permutation
	}
	return out
}

// keysetDoc is document i of the generated workspace: a type (one the schema refuses, or none:
// diagnosed), a status (or its default), tags with case or inline, an abstract that is there, blank,
// whitespace, null, an empty list or absent, and an updated that is fractional within one second,
// the same instant at two offsets, outside 1678-2262, unparseable, or absent.
func keysetDoc(i int) string {
	var fm []string
	if t := []string{"adr", "note", "memo", ""}[i%4]; t != "" {
		fm = append(fm, "type: "+t)
	}
	if st := []string{"draft", "active", ""}[i%3]; st != "" {
		fm = append(fm, "status: "+st)
	}
	body := fmt.Sprintf("# Doc %d\n", i)
	switch i % 5 {
	case 0:
		fm = append(fm, "tags: [Kb, Search]")
	case 1:
		fm = append(fm, "tags: [kb]")
	case 3:
		body += "about #Search\n"
	case 4:
		fm = append(fm, "tags: [Other]")
	}
	if a := []string{`abstract: "Yes."`, `abstract: ""`, `abstract: "  "`, "abstract: null", "abstract: []", ""}[i%6]; a != "" {
		fm = append(fm, a)
	}
	if u := []string{`"2026-01-02T10:00:00.900Z"`, `"2026-01-02T10:00:00.100Z"`, `"2026-01-02T12:00:00+02:00"`,
		`"2026-01-02T10:00:00Z"`, `"0500-01-01"`, `"9000-12-31"`, `"1600-06-01"`, `"not a date"`, "2026-03-04", ""}[i%10]; u != "" {
		fm = append(fm, "updated: "+u)
	}
	if len(fm) == 0 {
		return body
	}
	return "---\n" + strings.Join(fm, "\n") + "\n---\n" + body
}

// keysetEnv is an env holding the generated workspace of n documents, indexed, each with an
// indexed_at of 1000 × (i mod 4) (ties) and its keys written by the backfill from those, so the
// SQL path and the oracle read the same instants.
func keysetEnv(t *testing.T, n int) (*env, []string, options) {
	t.Helper()
	sv := &schemaVar{}
	sv.set(t, facetSchema, "s1")
	opts := options{Schema: sv.get}
	e := newEnv(t, opts)
	ps := keysetPaths(n)
	var pc []string
	for i := len(ps) - 1; i >= 0; i-- { // written last first: ids against path order
		pc = append(pc, ps[i], keysetDoc(i))
	}
	e.put(pc...)
	for i, p := range ps {
		e.exec("UPDATE document SET indexed_at = ? WHERE path = ?", int64(1000*(i%4)), p)
	}
	e.unsetKeys()
	e.backfill()
	return e, ps, opts
}

// unsetKeys is every document as it was before 000027: no stored keys.
func (e *env) unsetKeys() {
	e.t.Helper()
	e.exec("UPDATE document SET k_upd_s = NULL, k_upd_ns = NULL, k_indexed = NULL")
}

// unset counts the documents with no stored keys.
func (e *env) unset() int {
	e.t.Helper()
	rows, err := e.raw.QueryContext(context.Background(), "SELECT count(*) FROM document WHERE k_upd_s IS NULL")
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var n int
	if !rows.Next() {
		e.t.Fatal("no count")
	}
	if err := rows.Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// backfill keys every document that has none, through the store.
func (e *env) backfill() {
	e.t.Helper()
	if _, err := e.store.backfillKeys(context.Background(), e.store.storeWrite); err != nil {
		e.t.Fatal(err)
	}
	if n := e.unset(); n != 0 {
		e.t.Fatalf("the backfill left %d documents without keys", n)
	}
}

// traverse pages index.documents to its end at o.Limit, failing on a page that is short while more
// follow or a next that does not agree with more. It answers every page's entries in order.
func traverse(t *testing.T, s *Store, o DocumentsOpts) []DocumentEntry {
	t.Helper()
	var all []DocumentEntry
	o.After = ""
	for pages := 0; ; pages++ {
		if pages > 1000 {
			t.Fatalf("%+v: no end after 1000 pages", o)
		}
		p, err := s.Documents(context.Background(), o)
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		all = append(all, p.Docs...)
		if p.More != (p.Next != "") {
			t.Fatalf("%+v: more %v with next %q", o, p.More, p.Next)
		}
		if !p.More {
			return all
		}
		if lim := min(max(o.Limit, 1), MaxDocumentsPage); len(p.Docs) != lim {
			t.Fatalf("%+v: a page of %d with more to follow, want %d", o, len(p.Docs), lim)
		}
		o.After = p.Next
	}
}

// oracleAll is the oracle's whole listing for o's filters and sort.
func oracleAll(t *testing.T, s *Store, o DocumentsOpts) []DocumentEntry {
	t.Helper()
	o.Limit = MaxDocumentsPage
	all, more, err := s.oracleDocuments(context.Background(), o, 0)
	if err != nil || more {
		t.Fatalf("the oracle: more %v, %v", more, err)
	}
	return all
}

// keysetFilters are the filter combinations the parity cell crosses with every sort: each filter
// alone, the root written four ways, an uncleaned path, an exact file, and combinations.
func keysetFilters(file string) map[string]DocumentsOpts {
	return map[string]DocumentsOpts{
		"none":               {},
		"root .":             {Paths: []string{"."}},
		"root ./":            {Paths: []string{"./"}},
		"root empty":         {Paths: []string{""}},
		"root among others":  {Paths: []string{"a/b", "."}},
		"a folder/":          {Paths: []string{"a/"}},
		"a/./b uncleaned":    {Paths: []string{"a/./b"}},
		"two folders":        {Paths: []string{"a/b", "notes"}},
		"an exact file":      {Paths: []string{file}},
		"tags with case":     {Tags: []string{"KB", "search"}},
		"an inline tag":      {Tags: []string{"search"}},
		"facets":             {Facets: map[string][]string{"status": {"draft"}}},
		"two facets":         {Facets: map[string][]string{"type": {"adr", "note"}, "status": {"active"}}},
		"a facet, no values": {Facets: map[string][]string{"status": {}}},
		"missing":            {Missing: []string{"abstract"}},
		"missing two":        {Missing: []string{"abstract", "tags"}},
		"diagnosed":          {Diagnosed: true},
		"paths+tags+missing": {Paths: []string{"a"}, Tags: []string{"kb"}, Missing: []string{"abstract"}},
		"facets+diagnosed":   {Facets: map[string][]string{"status": {"active"}}, Diagnosed: true},
		"everything": {Paths: []string{"a/b/", "notes", "ab"}, Tags: []string{"kb"}, Facets: map[string][]string{"status": {"draft", "active"}},
			Missing: []string{"abstract"}, Diagnosed: true, Fields: []string{"type", "updated", "abstract"}},
	}
}

// TestDocumentsMatchTheOracle (§5.1, §5.2): every sort crossed with every filter combination, paged
// to the end at limits 1, 2, 7 and 500, lists exactly the oracle's documents, in its order, with
// the same entries: on the SQL path, and on the Go fallback while no document has keys.
func TestDocumentsMatchTheOracle(t *testing.T) {
	e, ps, _ := keysetEnv(t, 36)
	file := ps[3] // under a/b/c/
	for _, mode := range []string{"sql", "fallback"} {
		if mode == "fallback" {
			e.unsetKeys()
		}
		for name, f := range keysetFilters(file) {
			for _, sort := range []string{SortUpdated, SortPath, SortIndexed} {
				f.Sort = sort
				want := oracleAll(t, e.store, f)
				for _, limit := range []int{1, 2, 7, 500} {
					f.Limit = limit
					if got := traverse(t, e.store, f); !sameEntries(got, want) {
						t.Errorf("%s, %s, sort %s, limit %d: %d documents\n%v\nwant %d\n%v", mode, name, sort, limit,
							len(got), entryPaths(got), len(want), entryPaths(want))
					}
				}
			}
		}
	}
	// the workspace exercises each filter: it admits some documents and refuses others, so a filter
	// dropped from the SQL is seen
	for name, f := range keysetFilters(file) {
		if name == "a facet, no values" || strings.HasPrefix(name, "root") || name == "none" {
			continue
		}
		if n := len(oracleAll(t, e.store, f)); n == 0 || n == len(ps) {
			t.Errorf("%s admits %d of %d documents: it exercises nothing", name, n, len(ps))
		}
	}
}

// sameEntries is a and b equal, none and an empty list alike.
func sameEntries(a, b []DocumentEntry) bool {
	return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(a, b)
}

func entryPaths(es []DocumentEntry) []string {
	out := make([]string, len(es))
	for i, d := range es {
		out[i] = d.Path
	}
	return out
}

// TestTiesAtPageBoundaries (§5.2): at limit 1, documents with an equal indexed_at, or an equal
// updated instant, each appear once, in path order within the tie, and the ties sit in their
// order's place: newest first.
func TestTiesAtPageBoundaries(t *testing.T) {
	e := newEnv(t, options{})
	e.put("z.md", "# z", "m.md", "# m", "a.md", "# a", "q.md", "# q",
		"u2.md", "---\nupdated: \"2026-01-02T12:00:00+02:00\"\n---\n# u2",
		"u1.md", "---\nupdated: \"2026-01-02T10:00:00Z\"\n---\n# u1",
		"u0.md", "---\nupdated: \"2026-01-02T09:00:00Z\"\n---\n# u0")
	for p, at := range map[string]int64{"z.md": 500, "m.md": 500, "a.md": 500, "q.md": 900, "u2.md": 100, "u1.md": 100, "u0.md": 100} {
		e.exec("UPDATE document SET indexed_at = ? WHERE path = ?", at, p)
	}
	e.unsetKeys()
	e.backfill()
	for sort, want := range map[string][]string{
		SortIndexed: {"q.md", "a.md", "m.md", "z.md", "u0.md", "u1.md", "u2.md"},
		// u1 and u2 are one instant; the undated ones by their indexed_at (1970), after them
		SortUpdated: {"u1.md", "u2.md", "u0.md", "q.md", "a.md", "m.md", "z.md"},
	} {
		if got := entryPaths(traverse(t, e.store, DocumentsOpts{Sort: sort, Limit: 1})); !reflect.DeepEqual(got, want) {
			t.Errorf("sort %s at limit 1: %v, want %v", sort, got, want)
		}
	}
}

// TestDocumentsSeekByKey (§5.3): under no ANALYZE statistics, as in every store, each page's
// statement carries the row-value key predicate and no OFFSET, and is planned as a search of the
// order's index (a range on its key), with no sort of its own; each filter's EXISTS is a keyed
// search of its table, never a scan.
func TestDocumentsSeekByKey(t *testing.T) {
	e, ps, _ := keysetEnv(t, 36)
	capture := &sqlCapture{}
	s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(capture)}
	wantIndex := map[string]string{SortUpdated: "document_k_updated", SortIndexed: "document_k_indexed", SortPath: "sqlite_autoindex_document"}
	resumed := map[string]int{}
	seekCols := map[string]string{SortUpdated: `("document"."k_upd_s", "document"."k_upd_ns", "document"."path") > (`,
		SortIndexed: `("document"."k_indexed", "document"."path") > (`, SortPath: `"document"."path" > `}
	for name, f := range keysetFilters(ps[3]) {
		if strings.HasPrefix(name, "a facet, no values") {
			continue // answered without a statement
		}
		for _, sort := range []string{SortUpdated, SortPath, SortIndexed} {
			f.Sort, f.Limit, f.After = sort, 2, ""
			capture.stmts = nil
			first, err := s.Documents(context.Background(), f)
			if err != nil {
				t.Fatalf("%s, %s: a first page: %v", name, sort, err)
			}
			if first.Next == "" {
				continue // one page holds the answer: no page after a cursor to plan
			}
			resumed[sort]++
			f.After = first.Next
			capture.stmts = nil
			if _, err := s.Documents(context.Background(), f); err != nil {
				t.Fatal(err)
			}
			paged := false
			for _, q := range capture.stmts {
				if strings.Contains(strings.ToUpper(q.SQL), "OFFSET") {
					t.Errorf("%s, %s: a statement skips by OFFSET: %s", name, sort, q.SQL)
				}
				if q.Table != "document" || !strings.Contains(q.SQL, "ORDER BY") {
					continue
				}
				paged = true
				if !strings.Contains(q.SQL, seekCols[sort]) {
					t.Errorf("%s, %s: the page's statement has no key predicate: %s", name, sort, q.SQL)
				}
				plan := e.plan(q.SQL, q.Args...)
				switch {
				case !strings.Contains(plan, "SEARCH document USING ") || !strings.Contains(plan, wantIndex[sort]):
					t.Errorf("%s, %s: not a search of %s:\n%s\n%s", name, sort, wantIndex[sort], q.SQL, plan)
				case strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY"):
					t.Errorf("%s, %s: the page is sorted, not read in its index's order:\n%s\n%s", name, sort, q.SQL, plan)
				}
				for _, tbl := range []string{"doc_tag", "doc_facet", "doc_diagnostic"} {
					if strings.Contains(plan, "SCAN "+tbl) {
						t.Errorf("%s, %s: %s is scanned, not searched by the document:\n%s", name, sort, tbl, plan)
					}
				}
			}
			if !paged {
				t.Errorf("%s, %s: no page statement captured", name, sort)
			}
		}
	}
	for _, sort := range []string{SortUpdated, SortPath, SortIndexed} {
		if resumed[sort] < 10 {
			t.Errorf("sort %s: only %d filter combinations had a second page to plan", sort, resumed[sort])
		}
	}
	// a full listing reads each document once: O(N) rows, not N per page
	capture.stmts = nil
	if got := traverse(t, s, DocumentsOpts{Sort: SortUpdated, Limit: 5}); len(got) != len(ps) {
		t.Fatalf("listed %d of %d", len(got), len(ps))
	}
	if n := len(capture.stmts); n > 2*(len(ps)/5+1)+2 {
		t.Errorf("a full listing at limit 5 ran %d statements", n)
	}
}

// pageRows sums the rows the page statements (document, ordered) return, as dao reports them after
// each one runs.
type pageRows struct {
	dao.NopHook
	rows, pages int
}

func (c *pageRows) AfterExec(_ context.Context, q *dao.QueryInfo, out dao.Outcome) error {
	if q.Table == "document" && strings.Contains(q.SQL, "ORDER BY") {
		c.rows += out.Rows
		c.pages++
	}
	return out.Err // the hook contract: AfterExec passes the statement's error on
}

// TestAListingReadsEachDocumentOnce (§5.3, §4's cost): a whole listing materialises O(N + pages ×
// window) rows, a window a read: limit+1 a page without missing, missingWindow with it. Rows, not
// statements: a page that read every document after its cursor would keep the plan, the order and
// the statement count, and still be quadratic.
func TestAListingReadsEachDocumentOnce(t *testing.T) {
	was := missingWindow
	missingWindow = 4
	t.Cleanup(func() { missingWindow = was })
	e, ps, _ := keysetEnv(t, 36)
	n := len(ps)
	for _, o := range []DocumentsOpts{
		{Sort: SortUpdated, Limit: 2},
		{Sort: SortPath, Limit: 3},
		{Sort: SortIndexed, Limit: 2, Tags: []string{"kb"}},
		{Sort: SortPath, Limit: 2, Missing: []string{"abstract"}},
	} {
		counter := &pageRows{}
		s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(counter)}
		got := traverse(t, s, o)
		window := o.Limit + 1
		if len(o.Missing) > 0 {
			window = missingWindow
		}
		// the counter saw the reads: at least one row a document listed, so a zero budget can't pass
		if counter.pages == 0 || counter.rows < len(got) {
			t.Fatalf("%+v: the counter saw %d reads of %d rows for %d documents listed", o, counter.pages, counter.rows, len(got))
		}
		if budget := n + counter.pages*window; counter.rows > budget || len(got) == 0 {
			t.Errorf("%+v: %d documents listed in %d reads of %d rows; the budget is %d (N %d + reads × window %d)",
				o, len(got), counter.pages, counter.rows, budget, n, window)
		}
	}
}

// plan is SQLite's EXPLAIN QUERY PLAN of q, its details one a line.
func (e *env) plan(q string, args ...any) string {
	e.t.Helper()
	rows, err := e.raw.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, "\n")
}

// TestMissingLooksAheadAcrossWindows (§5.4): documents that lack the field are separated by more
// than one fetch window of documents that have it, and the last window holds none: each page is full
// when it can be, more is right, and each next resumes with no repeat or skip.
func TestMissingLooksAheadAcrossWindows(t *testing.T) {
	was := missingWindow
	missingWindow = 3
	t.Cleanup(func() { missingWindow = was })
	e := newEnv(t, options{})
	var pc []string
	for i := range 30 {
		content := "---\nabstract: Has one.\n---\n# x"
		if i%7 == 0 { // 0, 7, 14, 21, 28: six or more apart, two windows of 3
			content = "# no abstract"
		}
		pc = append(pc, fmt.Sprintf("p%02d.md", i), content)
	}
	pc = append(pc, "q.md", "---\nabstract: Has one.\n---\n# last, after every match")
	e.put(pc...)
	want := []string{"p00.md", "p07.md", "p14.md", "p21.md", "p28.md"}
	for _, limit := range []int{1, 2, 4, 5, 6} {
		o := DocumentsOpts{Sort: SortPath, Missing: []string{"abstract"}, Limit: limit}
		if got := entryPaths(traverse(t, e.store, o)); !reflect.DeepEqual(got, want) {
			t.Errorf("limit %d: %v, want %v", limit, got, want)
		}
	}
	// a page that takes the last match says no more, though documents follow it
	p, err := e.store.Documents(context.Background(), DocumentsOpts{Sort: SortPath, Missing: []string{"abstract"}, Limit: 5})
	if err != nil || len(p.Docs) != 5 || p.More || p.Next != "" {
		t.Errorf("every match in one page: %d docs, more %v, next %q, %v", len(p.Docs), p.More, p.Next, err)
	}
}

// TestACursorIsForItsOwnListing (§5.5): a cursor from another sort, from other filters, or not a
// cursor at all is refused (ErrBadCursor, invalid params on the wire); "" starts the listing.
func TestACursorIsForItsOwnListing(t *testing.T) {
	e := newEnv(t, options{})
	e.put("a.md", "---\ntags: [x]\n---\n# a", "b.md", "---\ntags: [x]\n---\n# b", "c.md", "# c")
	o := DocumentsOpts{Sort: SortPath, Tags: []string{"x"}, Limit: 1}
	p, err := e.store.Documents(context.Background(), o)
	if err != nil || p.Next == "" {
		t.Fatalf("a first page: %+v, %v", p, err)
	}
	for name, bad := range map[string]DocumentsOpts{
		"another sort":       {Sort: SortUpdated, Tags: []string{"x"}, After: p.Next},
		"another tag":        {Sort: SortPath, Tags: []string{"y"}, After: p.Next},
		"a tag fewer":        {Sort: SortPath, After: p.Next},
		"a path filter":      {Sort: SortPath, Tags: []string{"x"}, Paths: []string{"a.md"}, After: p.Next},
		"missing":            {Sort: SortPath, Tags: []string{"x"}, Missing: []string{"title"}, After: p.Next},
		"diagnosed":          {Sort: SortPath, Tags: []string{"x"}, Diagnosed: true, After: p.Next},
		"an offset as text":  {Sort: SortPath, Tags: []string{"x"}, After: "1"},
		"a broken cursor":    {Sort: SortPath, Tags: []string{"x"}, After: cursorPrefix + "!!"},
		"a cursor's garbage": {Sort: SortPath, Tags: []string{"x"}, After: cursorPrefix + "e30"},
	} {
		if _, err := e.store.Documents(context.Background(), bad); !errors.Is(err, ErrBadCursor) {
			t.Errorf("%s: %v, want ErrBadCursor", name, err)
		}
	}
	// the same filters written otherwise are the same listing: its cursor is good for them
	same := DocumentsOpts{Sort: SortPath, Tags: []string{"X", "x"}, Limit: 1, After: p.Next}
	if q, err := e.store.Documents(context.Background(), same); err != nil || len(q.Docs) != 1 || q.Docs[0].Path != "b.md" {
		t.Errorf("the same filters written otherwise: %+v, %v", q, err)
	}
	if q, err := e.store.Documents(context.Background(), DocumentsOpts{Sort: SortPath, Limit: 1}); err != nil || q.Docs[0].Path != "a.md" {
		t.Errorf("no after: %+v, %v", q, err)
	}
}

// TestPagingIsStable (§5.6): between two pages, documents are added and removed before the cursor;
// of the documents that stayed, none is skipped or repeated.
func TestPagingIsStable(t *testing.T) {
	e := newEnv(t, options{})
	var pc []string
	for i := range 12 {
		pc = append(pc, fmt.Sprintf("d%02d.md", i), "# d")
	}
	e.put(pc...)
	for _, sort := range []string{SortPath, SortIndexed} {
		o := DocumentsOpts{Sort: sort, Limit: 4}
		p, err := e.store.Documents(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		seen := entryPaths(p.Docs)
		// before the cursor in path order: a new first document, and one listed already gone
		e.put("a-new-"+sort+".md", "# new")
		e.remove(seen[0])
		e.put(seen[0], "# back") // re-added: its key may move, it is not among the ones that stayed
		gone := seen[0]
		for o.After = p.Next; o.After != ""; o.After = p.Next {
			if p, err = e.store.Documents(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			seen = append(seen, entryPaths(p.Docs)...)
		}
		for i := range 12 {
			name := fmt.Sprintf("d%02d.md", i)
			if name == gone {
				continue
			}
			if n := slices.Index(seen, name); n < 0 || slices.Contains(seen[n+1:], name) {
				t.Errorf("sort %s: %s seen %d times: %v", sort, name, strings.Count(strings.Join(seen, " ")+" ", name+" "), seen)
			}
		}
	}
}

// TestAListingCrossesTheBackfill (§5.7): a traversal begins while no document has keys (the Go
// fallback), and its next is carried across partial backfill batches, a restart mid-backfill, a
// document updated during the backfill (one already listed, its key unmoved) and the backfill's
// end, into the SQL path. The traversal is the oracle's. The backfill keys at most its batch a
// commit, resumes by its gate, and gives every document the key docKeys gives it.
func TestAListingCrossesTheBackfill(t *testing.T) {
	was := keyBatch
	keyBatch = 5
	t.Cleanup(func() { keyBatch = was })
	e, ps, opts := keysetEnv(t, 36)
	o := DocumentsOpts{Sort: SortUpdated, Fields: []string{"type"}, Limit: 4}
	want := entryPaths(oracleAll(t, e.store, o))
	e.unsetKeys()

	var got []string
	page := func() {
		t.Helper()
		p, err := e.store.Documents(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, entryPaths(p.Docs)...)
		o.After = p.Next
	}
	step := func() {
		t.Helper()
		before := e.unset()
		n, err := e.store.backfillKeysOnce(context.Background(), e.store.storeWrite)
		if err != nil {
			t.Fatal(err)
		}
		if uint64(n) > keyBatch || e.unset() != before-n || (before > 0 && n == 0) {
			t.Fatalf("a backfill batch keyed %d (batch %d): %d unset before, %d after", n, keyBatch, before, e.unset())
		}
	}
	page() // the fallback
	step()
	page() // still the fallback: most have no keys
	step()
	e.open(opts) // a restart mid-backfill, the same schema: the gate resumes it
	page()
	// a document already listed changes, its key unmoved: its updated is its frontmatter's
	listed := got[0]
	i := slices.Index(ps, listed)
	e.put(listed, strings.Replace(keysetDoc(i), "# Doc", "# Edited doc", 1))
	page()
	for e.unset() > 0 {
		step()
	}
	for o.After != "" {
		page() // the SQL path
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the traversal:\n%v\nwant\n%v", got, want)
	}
	// every stored key is the one docKeys gives the document now
	rows, err := e.raw.QueryContext(context.Background(), "SELECT path, frontmatter_json, indexed_at, k_upd_s, k_upd_ns, k_indexed FROM document")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var front *string
		var at, s, ns, idx int64
		if err := rows.Scan(&p, &front, &at, &s, &ns, &idx); err != nil {
			t.Fatal(err)
		}
		if ws, wns, widx := docKeysOf(front, at); s != ws || ns != wns || idx != widx {
			t.Errorf("%s: stored (%d, %d, %d), docKeys (%d, %d, %d)", p, s, ns, idx, ws, wns, widx)
		}
	}
}

// TestABackfillBatchKeysOnlyTheUnkeyed (§5.7): one backfill batch keys at most keyBatch documents,
// all of them ones that had no keys, so the count without keys falls by exactly what it keyed; a
// batch over documents all keyed keys none. The documents keyed already come first in the table, so
// a batch that read them too would spend itself on them. It runs single batches only: a backfill
// whose gate is gone never ends.
func TestABackfillBatchKeysOnlyTheUnkeyed(t *testing.T) {
	was := keyBatch
	keyBatch = 2
	t.Cleanup(func() { keyBatch = was })
	e := newEnv(t, options{})
	for i := range 8 {
		e.put(fmt.Sprintf("d%d.md", i), "# d") // one at a time: ids in this order
	}
	e.exec("UPDATE document SET k_upd_s = NULL, k_upd_ns = NULL, k_indexed = NULL WHERE path IN ('d5.md', 'd6.md', 'd7.md')")
	for _, want := range []int{2, 1, 0} {
		before := e.unset()
		n, err := e.store.backfillKeysOnce(context.Background(), e.store.storeWrite)
		if err != nil {
			t.Fatal(err)
		}
		if n != want || e.unset() != before-n {
			t.Fatalf("a batch keyed %d (want %d): %d without keys before, %d after", n, want, before, e.unset())
		}
	}
}

// TestTheWriterKeysWhatItWrites: a document written from 000027 on has its keys from its own write,
// so the listing needs no backfill for it: the SQL path serves it at once.
func TestTheWriterKeysWhatItWrites(t *testing.T) {
	e := newEnv(t, options{})
	e.put("a.md", "---\nupdated: 2026-05-06\n---\n# a", "b.md", "# b")
	if n := e.unset(); n != 0 {
		t.Fatalf("%d documents written without keys", n)
	}
	var unset bool
	if err := e.store.read(context.Background(), func(tx *store.Tx) (err error) {
		unset, err = e.store.keysUnset(tx)
		return err
	}); err != nil || unset {
		t.Errorf("the gate: %v, %v", unset, err)
	}
}

// TestADocumentsReadThatFailsSaysSo: each read a page makes (the gate, the page, the fallback's
// documents, tags, facets and diagnostics, a page's diagnostics) failing fails the call, and a
// backfill whose read or write fails says so and keys nothing.
func TestADocumentsReadThatFailsSaysSo(t *testing.T) {
	e, _, _ := keysetEnv(t, 12)
	all := DocumentsOpts{Tags: []string{"kb"}, Facets: map[string][]string{"status": {"active"}}, Diagnosed: true}
	for _, c := range []struct {
		name, match string
		unset       bool
	}{
		{"the gate", `"k_upd_s" IS NULL`, false},
		{"the page", `ORDER BY`, false},
		{"a page's diagnostics", `"doc_diagnostic"."message"`, false},
		{"the fallback's documents", `"k_indexed" FROM "document"`, true},
		{"the fallback's tags", `FROM "doc_tag"`, true},
		{"the fallback's facets", `FROM "doc_facet"`, true},
		{"the fallback's diagnostics", `"doc_diagnostic"."doc_id" FROM "doc_diagnostic"`, true},
	} {
		if c.unset {
			e.unsetKeys()
		} else {
			e.backfill()
		}
		s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(failingSQL{match: c.match})}
		if _, err := s.Documents(context.Background(), all); !errors.Is(err, errGraphRead) {
			t.Errorf("%s failing: %v", c.name, err)
		}
	}
	e.unsetKeys()
	for _, match := range []string{`"k_upd_s" IS NULL`, `UPDATE "document"`} {
		s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(failingSQL{match: match})}
		if _, err := s.backfillKeys(context.Background(), s.storeWrite); !errors.Is(err, errGraphRead) {
			t.Errorf("a backfill whose %s fails: %v", match, err)
		}
		if n := e.unset(); n != 12 {
			t.Errorf("a failed backfill batch keyed %d documents", 12-n)
		}
	}
}
