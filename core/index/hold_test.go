package index

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/search/chunk"
)

// TestIdentitiesParseOneWay: each form parses back to its fields, versions holding '+' and '.'
// included; distinct (extension, id, version) triples never yield one string; strings outside the
// grammar, or with '@', ':' or whitespace inside a field, are refused.
func TestIdentitiesParseOneWay(t *testing.T) {
	built := "c" + ChunkerVersion + ".s3.t512.f0123456789ab"
	for _, c := range []struct {
		s    string
		want Identity
	}{
		{"c5.s3.t512", Identity{Form: BuiltIn, Chunker: "5", Schema: 3, Tokens: 512}},
		{"c5.s3.t256.f0123456789ab", Identity{Form: BuiltIn, Chunker: "5", Schema: 3, Tokens: 256, SchemaFP: "0123456789ab"}},
		{"c.go@code-go-1+text-5.s3.t512", Identity{Form: Registered, Ext: ".go", Version: "code-go-1+text-5", Schema: 3, Tokens: 512}},
		{"c.go@1.s3.t5.s3.t512", Identity{Form: Registered, Ext: ".go", Version: "1.s3.t5", Schema: 3, Tokens: 512}},
		{"d.pdf:autorag/pdf@1+a+" + built, Identity{Form: Derived, Ext: ".pdf", Deriver: "autorag/pdf", Version: "1+a", Built: built}},
		{"d.docx:autorag/docx@1+" + built, Identity{Form: Derived, Ext: ".docx", Deriver: "autorag/docx", Version: "1", Built: built}},
	} {
		got, err := ParseIdentity(c.s)
		if err != nil || got != c.want {
			t.Errorf("%q: %+v, %v; want %+v", c.s, got, err, c.want)
		}
	}
	// the strings the indexer writes are the ones it reads
	if id, err := ParseIdentity(registeredVersion(".rs", "code-rs-1", 0)); err != nil || id.Ext != ".rs" || id.Version != "code-rs-1" {
		t.Errorf("registeredVersion reads back as %+v, %v", id, err)
	}
	for _, tok := range []int{0, 256} {
		for _, fp := range []string{"", "0123456789ab"} {
			if id, err := ParseIdentity(docVersion(indexerVersion(tok), true, fp)); err != nil || id.Form != BuiltIn || id.SchemaFP != fp {
				t.Errorf("docVersion(%d, %q) reads back as %+v, %v", tok, fp, id, err)
			}
		}
	}
	// distinct triples, versions "1+a" and "1" among them, never yield one string
	seen := map[string]string{}
	for _, ext := range []string{".pdf", ".p"} {
		for _, id := range []string{"autorag/pdf", "autorag", "a"} {
			for _, v := range []string{"1", "1+a", "a+1", "1.s3"} {
				s := derivedVersion(ext, id, v, built)
				key := ext + " " + id + " " + v
				if prev, ok := seen[s]; ok {
					t.Errorf("%s and %s are both %q", prev, key, s)
				}
				seen[s] = key
				got, err := ParseIdentity(s)
				if err != nil || got.Ext != ext || got.Deriver != id || got.Version != v {
					t.Errorf("%q parses as %+v, %v; want %s", s, got, err, key)
				}
			}
		}
	}
	for _, s := range []string{"", "x", "c", "c.go", "c.go@.s3.t512", "c.GO@1.s3.t512", "c.go@1 2.s3.t512", "c.go@1:2.s3.t512",
		"c5.s3", "c5.s3.t512.fxyz", "d.pdf:autorag/pdf@1", "d.pdf:auto rag@1+" + built, "d.pdf:a@b@1+" + built,
		"d.pdf:a@1+c.go@1.s3.t512", "c.go@1.sx.t512"} {
		if id, err := ParseIdentity(s); !errors.Is(err, ErrIdentity) {
			t.Errorf("%q parses as %+v", s, id)
		}
	}
	// a derived string splits at its last '+' because a built-in never holds one
	if strings.Contains(chunk.Version, "+") {
		t.Fatalf("golib's chunk.Version %q holds '+': a derived identity would split wrongly", chunk.Version)
	}
}

// TestADaemonHoldsWhatItCannotReadAgain: a community daemon over a store a registered one wrote
// reinterprets nothing it lacks the chunker for, but keeps it searchable, marked, with its file's
// state: unchecked before anything has seen it, current, then stale once edited. index.reindex of
// it is refused, naming the way out. Disk and the rules still delete it, and the counts drop; a
// file included again is this daemon's.
func TestADaemonHoldsWhatItCannotReadAgain(t *testing.T) {
	ctx := context.Background()
	reg := registered(t, &funcChunker{version: "fake-1"})
	e := newEnv(t, Options{Match: goAndMarkdown, Registrations: reg})
	e.put("a.go", "func Kestrel() {\n\thover()\n}\n", "b.go", "func Falcon() {\n\tstoop()\n}\n", "n.md", "# N\n\nnotes\n")
	recorded := e.indexerOf("a.go")

	e.open(Options{Match: goAndMarkdown}) // the community build, over the same store
	hold := func(q string) string {
		t.Helper()
		r := e.query(q, QueryOpts{Mode: ModeLexical})
		if len(r.Hits) == 0 {
			t.Fatalf("%q: no hit", q)
		}
		return r.Hits[0].Hold
	}
	status := func() Status {
		t.Helper()
		st, err := e.ix.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	// held from the row, before anything has seen the file
	if got := hold("kestrel"); got != HoldUnchecked {
		t.Fatalf("before any scan: hold %q, want unchecked", got)
	}
	if got := hold("notes"); got != "" {
		t.Fatalf("a Markdown note is held: %q", got)
	}
	if st := status(); st.Held != 2 || st.HeldUnchecked != 2 || st.HeldStale != 0 {
		t.Fatalf("status %+v", st)
	}
	var he *HeldError
	if err := e.ix.Reindex("a.go"); !errors.As(err, &he) || !strings.Contains(err.Error(), "no chunker for .go: start a build that has it") {
		t.Fatalf("Reindex(a.go): %v", err)
	}
	// seen unchanged, then edited: held all along, never re-cut
	e.ix.Touch("a.go")
	e.eventually("a.go checked", func() bool { return hold("kestrel") == HoldCurrent })
	e.write("a.go", "func Kestrel() {\n\thover() // edited\n}\n")
	e.ix.Touch("a.go")
	e.eventually("a.go stale", func() bool { return hold("kestrel") == HoldStale })
	_ = e.ix.Reindex("")
	e.eventually("the whole workspace checked", func() bool { return status().HeldUnchecked == 0 && e.pendingJobs() == 0 })
	if got := e.indexerOf("a.go"); got != recorded {
		t.Fatalf("a held document was reinterpreted: %q, was %q", got, recorded)
	}
	if st := status(); st.Held != 2 || st.HeldStale != 1 || st.HeldUnchecked != 0 {
		t.Fatalf("status %+v", st)
	}
	// disk deletes it
	e.remove("b.go")
	if st := status(); st.Held != 1 {
		t.Fatalf("after deleting b.go: status %+v", st)
	}
	// the rules delete it; included again, it is this daemon's
	e.open(Options{Match: testMatch})
	e.ix.Touch("a.go")
	e.eventually("a.go excluded", func() bool { _, ok := e.store.Version("a.go"); return !ok })
	if st := status(); st.Held != 0 {
		t.Fatalf("after excluding a.go: status %+v", st)
	}
	e.open(Options{Match: goAndMarkdown})
	e.ix.Touch("a.go")
	e.indexedAt("a.go")
	if got := hold("kestrel"); got != "" {
		t.Fatalf("a.go included again is held: %q", got)
	}
}

// TestADaemonWithTheChunkerUpgrades: a file a community daemon indexed as Markdown is cut again by
// a daemon with its chunker, since its kind there is Registered.
func TestADaemonWithTheChunkerUpgrades(t *testing.T) {
	e := newEnv(t, Options{Match: goAndMarkdown})
	e.put("a.go", goSrc)
	if id, _ := ParseIdentity(e.indexerOf("a.go")); id.Form != BuiltIn {
		t.Fatalf("the community build recorded %q", e.indexerOf("a.go"))
	}
	e.open(Options{Match: goAndMarkdown, Registrations: registered(t, &funcChunker{version: "fake-1"})})
	e.eventually("a.go cut by its chunker", func() bool { return e.indexerOf("a.go") == "c.go@fake-1.s3.t512" })
}

// TestADerivedDocumentIsHeldNotDeleted: a community daemon deletes a Pro format's file it never
// read, but one a deriver made text of is held, not deleted: the hold is decided before the kind.
func TestADerivedDocumentIsHeldNotDeleted(t *testing.T) {
	ctx := context.Background()
	pdfToo := func(p string) bool { return goAndMarkdown(p) || strings.HasSuffix(p, ".pdf") }
	e := newEnv(t, Options{Match: pdfToo})
	e.write("a.pdf", "%PDF-1.7")
	fi, err := e.fsys.Stat(ctx, "a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO document(workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (?, 'a.pdf', ?, 1, ?, 0)`,
		e.ws, string(fi.Version), derivedVersion(".pdf", "autorag/pdf", "1", "c"+ChunkerVersion+".s3.t512"))
	e.open(Options{Match: pdfToo}) // the held set is read from the rows the store has now
	e.ix.Touch("a.pdf")
	e.eventually("a.pdf checked", func() bool {
		st, err := e.ix.Status(ctx)
		return err == nil && st.Held == 1 && st.HeldUnchecked == 0
	})
	if _, ok := e.store.Version("a.pdf"); !ok {
		t.Fatal("the derived document was deleted")
	}
	var he *HeldError
	if err := e.ix.Reindex("a.pdf"); !errors.As(err, &he) || !strings.Contains(err.Error(), "no deriver for .pdf") {
		t.Fatalf("Reindex(a.pdf): %v", err)
	}
}
