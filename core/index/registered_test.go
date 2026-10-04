package index

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// funcChunker is a registered chunker for tests: a chunk for each line starting "func ", to the
// next, whose embed text is "signature " and that line. panicOn makes it panic on a file holding
// that word, as another module's code might.
type funcChunker struct {
	version string
	panicOn string
	cuts    atomic.Int64
}

func (c *funcChunker) Version() string { return c.version }

func (c *funcChunker) Chunk(d search.Doc) ([]search.Chunk, error) {
	c.cuts.Add(1)
	src := string(d.Text)
	if c.panicOn != "" && strings.Contains(src, c.panicOn) {
		panic("funcChunker: " + c.panicOn)
	}
	var out []search.Chunk
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		body := strings.TrimRight(src[start:end], "\n")
		sig, _, _ := strings.Cut(body, "\n")
		name := strings.TrimSuffix(strings.Fields(sig)[1], "() {")
		out = append(out, search.Chunk{Ord: len(out), Breadcrumb: d.Path + " > " + name, Body: body,
			Embed: "signature " + sig, ByteStart: start, ByteEnd: start + len(body)})
	}
	off := 0
	for line := range strings.SplitAfterSeq(src, "\n") {
		if strings.HasPrefix(line, "func ") {
			flush(off)
			start = off
		}
		off += len(line)
	}
	flush(len(src))
	return out, nil
}

func goAndMarkdown(p string) bool { return strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".md") }

// registered is a build registering c for .go.
func registered(t testing.TB, c search.Chunker) *registrations.Table {
	t.Helper()
	reg, err := registrations.New(map[string]search.Chunker{".go": c}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func (e *env) indexerOf(p string) string {
	e.t.Helper()
	var v string
	if err := scanOne(context.Background(), e.raw, &v, "SELECT indexer FROM document WHERE path = ?", p); err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *env) embedsOf(p string) []string {
	e.t.Helper()
	rows, err := e.raw.QueryContext(context.Background(), `SELECT c.embed FROM chunk c JOIN document d ON d.id = c.doc_id
		WHERE d.path = ? AND c.gen_to IS NULL ORDER BY c.ord`, p)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

const goSrc = "package a\n\nfunc A() {\n\treturn\n}\n\nfunc B() {\n\treturn\n}\n"

// TestARegisteredChunkerCutsItsFiles: a file of a registered extension is cut by its chunker and
// recorded under its extension and version, with the chunker's embed text; a build with no
// registrations reads the same file as Markdown, as before.
func TestARegisteredChunkerCutsItsFiles(t *testing.T) {
	e := newEnv(t, Options{Match: goAndMarkdown, Registrations: registered(t, &funcChunker{version: "fake-1"})})
	e.put("a.go", goSrc)
	if got := e.indexerOf("a.go"); got != "c.go@fake-1.s3.t512" {
		t.Errorf("a.go recorded under %q", got)
	}
	if got := e.embedsOf("a.go"); !slices.Equal(got, []string{"signature func A() {", "signature func B() {"}) {
		t.Errorf("embeds %q", got)
	}
	community := newEnv(t, Options{Match: goAndMarkdown})
	community.put("a.go", goSrc)
	if got := community.indexerOf("a.go"); got != "c"+ChunkerVersion+".s3.t512" {
		t.Errorf("the community build records a.go under %q", got)
	}
	if got := community.embedsOf("a.go"); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "" }) {
		t.Errorf("the community build's embeds %q", got)
	}
}

// TestAChunkerVersionReachesOnlyItsFiles: a registered chunker's new version re-cuts its own files
// and no other (ADR 0216 §1.5).
func TestAChunkerVersionReachesOnlyItsFiles(t *testing.T) {
	c := &funcChunker{version: "fake-1"}
	e := newEnv(t, Options{Match: goAndMarkdown, Registrations: registered(t, c)})
	e.put("a.go", goSrc, "n.md", "# N\n\nnotes\n")
	md := e.indexerOf("n.md")
	c2 := &funcChunker{version: "fake-2"}
	e.open(Options{Match: goAndMarkdown, Registrations: registered(t, c2)})
	e.eventually("a.go re-cut under fake-2", func() bool { return e.indexerOf("a.go") == "c.go@fake-2.s3.t512" })
	e.indexedAt("a.go")
	if c2.cuts.Load() != 1 {
		t.Errorf("fake-2 cut %d files, want a.go alone", c2.cuts.Load())
	}
	e.ix.mu.Lock()
	parses := e.ix.parses
	e.ix.mu.Unlock()
	if parses != 1 || e.indexerOf("n.md") != md {
		t.Errorf("the new version reached n.md: %d parses, n.md under %q", parses, e.indexerOf("n.md"))
	}
}

// TestAPanickingChunkerFailsItsDocument: a registered chunker's panic is that document's error;
// the indexer keeps indexing.
func TestAPanickingChunkerFailsItsDocument(t *testing.T) {
	e := newEnv(t, Options{Match: goAndMarkdown, Registrations: registered(t, &funcChunker{version: "fake-1", panicOn: "boom"})})
	e.write("bad.go", "func Bad() {\n\tboom()\n}\n")
	e.ix.Touch("bad.go")
	e.eventually("bad.go failing", func() bool {
		st, err := e.store.Status(context.Background())
		return err == nil && len(st.Failing) == 1
	})
	e.failing("bad.go", "the fake-1 chunker failed: funcChunker: boom")
	e.write("a.go", goSrc)
	e.ix.Touch("a.go")
	e.eventually("a.go indexed beside it", func() bool { _, ok := e.store.Version("a.go"); return ok })
}

// embedTexts is every text p was asked to embed that a funcChunker made.
func embedTexts(p *fakeProvider) []string {
	var out []string
	for _, s := range p.texts() {
		if strings.HasPrefix(s, "signature ") {
			out = append(out, s)
		}
	}
	return out
}

// TestAVectorIsMadeOfWhatItsHashNames: ADR 0216 §1.7's cells, with a registered chunker whose
// embed text is not its breadcrumb and body. The embedder receives exactly the embed text; a
// restart over unchanged files embeds nothing; an embedding cut short by a stop resumes from the
// row; a body change keeping the embed text embeds nothing, an embed text change once; one embed
// text on two chunks is one call.
func TestAVectorIsMadeOfWhatItsHashNames(t *testing.T) {
	reg := registered(t, &funcChunker{version: "fake-1"})
	held := newFake("m", "a")
	held.hold("signature")
	e := newEnv(t, Options{Match: goAndMarkdown, Registrations: reg, Provider: held})
	e.put("a.go", goSrc)
	e.eventually("the embedder asked", func() bool { return len(held.texts()) > 0 })
	// 3. stopped while the embedder holds the texts: nothing was stored, and a reopen sends them
	// again from the rows
	p := newFake("m", "a")
	e.open(Options{Match: goAndMarkdown, Registrations: reg, Provider: p})
	e.ready()
	// 1. exactly the embed texts, and nothing else
	if got := p.texts(); !slices.Equal(got, []string{"signature func A() {", "signature func B() {"}) {
		t.Fatalf("embedded %q, want the two signatures alone", got)
	}
	// 2. a restart over unchanged files makes no call
	p2 := newFake("m", "a")
	e.open(Options{Match: goAndMarkdown, Registrations: reg, Provider: p2})
	e.ready()
	e.atHead()
	if got := p2.texts(); len(got) != 0 {
		t.Fatalf("a restart over unchanged files embedded %q", got)
	}
	// 4. a body change keeping the embed text embeds nothing; an embed text change, once
	e.put("a.go", "package a\n\nfunc A() {\n\treturn // changed\n}\n\nfunc B() {\n\treturn\n}\n")
	e.ready()
	if got := embedTexts(p2); len(got) != 0 {
		t.Fatalf("a body change embedded %q", got)
	}
	e.put("a.go", "package a\n\nfunc Renamed() {\n\treturn // changed\n}\n\nfunc B() {\n\treturn\n}\n")
	e.ready()
	if got := embedTexts(p2); !slices.Equal(got, []string{"signature func Renamed() {"}) {
		t.Fatalf("an embed text change embedded %q, want it once", got)
	}
	// 5. one embed text on two chunks, of two files, is one call
	e.put("b.go", "func Twin() {\n\tone()\n}\n", "c.go", "func Twin() {\n\ttwo()\n}\n")
	e.ready()
	twins := 0
	for _, s := range embedTexts(p2) {
		if s == "signature func Twin() {" {
			twins++
		}
	}
	if twins != 1 {
		t.Fatalf("the twins' embed text was embedded %d times, want 1", twins)
	}
}
