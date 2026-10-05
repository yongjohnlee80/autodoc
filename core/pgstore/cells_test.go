package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"
)

// TestRequiredURLFailsLoudly pins the no-silent-skip contract itself: with AUTODOC_TEST_PG_REQUIRED
// set and no URL, the gate's verdict is a failure naming the variable, never a skip; without the
// requirement the same missing URL is a clean skip, so a local run with no database stays green.
// A runner that loses its service sees its cells go red with the variable's name in the log.
func TestRequiredURLFailsLoudly(t *testing.T) {
	t.Setenv(dsnEnv, "")
	t.Setenv("AUTODOC_TEST_PG_REQUIRED", "1")
	verdict, msg := gateVerdict()
	if verdict != gateFail {
		t.Errorf("a required run with no URL: %v; want a loud failure", verdict)
	}
	if !strings.Contains(msg, dsnEnv) {
		t.Errorf("the failure message = %q; want it to name %s", msg, dsnEnv)
	}
	t.Setenv("AUTODOC_TEST_PG_REQUIRED", "")
	verdict, msg = gateVerdict()
	if verdict != gateSkip || !strings.Contains(msg, dsnEnv) {
		t.Errorf("without the requirement: %v (%q); want a clean skip naming the variable", verdict, msg)
	}
}

// a gate verdict: pass with a URL, skip without one when not required, fail without one when
// required. The message names the variable in every case, so a red log says what was missing.
type gateResult int

const (
	gatePass gateResult = iota
	gateSkip
	gateFail
)

func gateVerdict() (gateResult, string) {
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		if os.Getenv("AUTODOC_TEST_PG_REQUIRED") == "1" {
			return gateFail, fmt.Sprintf("%s is not set: the Postgres cells are required here (AUTODOC_TEST_PG_REQUIRED=1) and cannot skip", dsnEnv)
		}
		return gateSkip, dsnEnv + " is not set: pgstore's cells need a PostgreSQL with pgvector"
	}
	return gatePass, dsn
}

// dsnBase is the admin connection a scratch database is made through, with the DSN the cells run
// against. When AUTODOC_TEST_PG_REQUIRED is set and the URL is missing, the cells must fail loudly
// rather than skip: a runner that names no database has lost its service, and a silent skip turns
// a required gate into a green lie.
func dsnBase(t *testing.T) string {
	t.Helper()
	verdict, msg := gateVerdict()
	switch verdict {
	case gatePass:
		return msg
	case gateSkip:
		t.Skip(msg)
	default:
		t.Fatal(msg)
	}
	return ""
}

// schemaDSN is dsn with its connection pointed at schema: the tables and the rag_english
// configuration are created and found through the connection's search_path, never by a qualified
// name, so a workspace whose destination names its own schema works with nothing else set.
func schemaDSN(t *testing.T, dsn, schema string) dao.DataConn {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("%s is not a URL DSN: %v", dsnEnv, err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	conn, err := postgres.Open(context.Background(), u.String())
	if err != nil {
		t.Fatalf("connect with search_path %s: %v", schema, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// putDoc writes one document with a title, tags and chunks carrying an embed text, ready under the
// test model unless the caller embeds it itself.
func putDoc(t testing.TB, s *Store, d Document, embedAll bool) {
	t.Helper()
	ctx := context.Background()
	id, err := s.Put(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	_ = id
	if !embedAll {
		return
	}
	var vecs []Embedding
	for _, c := range d.Chunks {
		vecs = append(vecs, Embedding{TextHash: c.TextHash(), Vec: embedVec(c.EmbedText())})
	}
	if err := s.Embed(ctx, model, vecs); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.MarkReady(ctx, d.Path, model); err != nil || !ready {
		t.Fatalf("mark %s ready: %v, %v", d.Path, ready, err)
	}
}

// lexicalPaths is the paths a query answers, in order.
func lexicalPaths(t testing.TB, s *Store, q string) []string {
	t.Helper()
	ctx := context.Background()
	var out []string
	if err := s.View(ctx, func(v *View) error {
		hits, err := v.Lexical(ctx, []search.Term{{Text: q}}, search.Filter{}, 100)
		if err != nil {
			return err
		}
		for _, h := range hits {
			if len(out) == 0 || out[len(out)-1] != h.Path {
				out = append(out, h.Path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// Cell 2 — the embed text. A chunk whose Embed differs from breadcrumb + "\n" + body: Pending
// returns exactly Embed under sha256(Embed); a body change that keeps Embed leaves Pending
// empty; a built-in chunk (Embed empty) returns the breadcrumb and body; and after Embed, MarkReady
// is true. The reverted line is Put's cEmb: c.Embed — without it every chunk embeds its breadcrumb
// and body, Pending returns the wrong text under the right hash, and a vector made from it can
// never match the chunk's text hash identity.
func TestPendingReturnsTheEmbedText(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	s, err := Open(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetModels(ctx, model, ""); err != nil {
		t.Fatal(err)
	}
	c := search.Chunk{Ord: 0, Breadcrumb: "sig.md > F", Body: "the body words", Embed: "func F() the signature words"}
	if _, err := s.Put(ctx, Document{Path: "sig.md", Chunks: []search.Chunk{c}}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(ctx, model, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].TextHash != c.TextHash() || pending[0].Text != c.Embed {
		t.Fatalf("pending = %+v; want the embed text %q under the chunk's hash", pending, c.Embed)
	}
	// a re-put with the same Embed and a changed body: the embed text is stable, so the hash is
	// and Pending keeps its answer for the same text
	c2 := search.Chunk{Ord: 0, Breadcrumb: "sig.md > F", Body: "the body words changed", Embed: "func F() the signature words"}
	if _, err := s.Put(ctx, Document{Path: "sig.md", Chunks: []search.Chunk{c2}}); err != nil {
		t.Fatal(err)
	}
	pending, err = s.Pending(ctx, model, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Text != c.Embed {
		t.Fatalf("pending after a body change that keeps Embed = %+v; want the same embed text", pending)
	}
	// embedding it empties Pending and readies the document
	if err := s.Embed(ctx, model, []Embedding{{TextHash: c.TextHash(), Vec: embedVec(c.Embed)}}); err != nil {
		t.Fatal(err)
	}
	if pending, err = s.Pending(ctx, model, nil, 10); err != nil || len(pending) != 0 {
		t.Fatalf("pending after embed = %+v, %v; want empty", pending, err)
	}
	if ready, err := s.MarkReady(ctx, "sig.md", model); err != nil || !ready {
		t.Fatalf("MarkReady after the vector: %v, %v", ready, err)
	}
	// skip names a hash the worker already holds: it is not listed again
	other := search.Chunk{Ord: 0, Breadcrumb: "b", Body: "other body", Embed: "other embed text"}
	if _, err := s.Put(ctx, Document{Path: "sig2.md", Chunks: []search.Chunk{other}}); err != nil {
		t.Fatal(err)
	}
	pending, err = s.Pending(ctx, model, map[[32]byte]bool{other.TextHash(): true}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending with the skip set = %+v; want the skipped hash gone", pending)
	}
	// a built-in chunk (Embed empty) returns breadcrumb + "\n" + body
	b := search.Chunk{Ord: 0, Breadcrumb: "builtin.md > head", Body: "plain body words"}
	if _, err := s.Put(ctx, Document{Path: "builtin.md", Chunks: []search.Chunk{b}}); err != nil {
		t.Fatal(err)
	}
	pending, err = s.Pending(ctx, model, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	// sig2.md is still pending (the skip set was one call's); builtin.md is the new hash, and
	// its text is the breadcrumb and body together
	var builtin *Text
	for i := range pending {
		if pending[i].TextHash == b.TextHash() {
			builtin = &pending[i]
		}
	}
	if builtin == nil {
		t.Fatalf("a built-in chunk's hash is not pending: %+v", pending)
	}
	if builtin.Text != b.Breadcrumb+"\n"+b.Body {
		t.Fatalf("a built-in chunk's pending text = %q; want breadcrumb + line feed + body", builtin.Text)
	}
}

// Cell 3 — the indexer identity round-trips verbatim. pgstore never parses it: every grammar form,
// including the derived-document forms with their ':', '@', '+', '.' and '-', comes back byte for
// byte, so a consumer comparing identities over a Postgres destination decides exactly what it
// wrote. The reverted line is Docs's dIndexer select — without the column the identity comes back
// as "" and a consumer would re-cut every document.
func TestIndexerRoundTripsVerbatim(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	s, err := Open(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	forms := []string{
		"c.go@code-go-1+text-5.s3.t512",
		"d.pdf:autorag/pdf@1+a+c5.s3.t512.f0123456789ab",
		"m.md",
		"+",
		"a/b:c@d+e-f.g",
	}
	for i, id := range forms {
		d := Document{Path: fmt.Sprintf("doc%d.md", i), Version: "v1", Indexer: id,
			Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "b", Body: "words"}}}
		if _, err := s.Put(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	docs, more, err := s.Docs(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if more || len(docs) != len(forms) {
		t.Fatalf("docs = %d, more %v; want %d and no more", len(docs), more, len(forms))
	}
	for i, doc := range docs {
		want := forms[i]
		if doc.Indexer != want {
			t.Errorf("%s: indexer = %q, want %q verbatim", doc.Path, doc.Indexer, want)
		}
		if doc.Version != "v1" {
			t.Errorf("%s: version = %q, want v1", doc.Path, doc.Version)
		}
	}
	// re-putting bumps the generation and keeps the identity
	if _, err := s.Put(ctx, Document{Path: "doc0.md", Version: "v2", Indexer: forms[0],
		Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "b", Body: "words2"}}}); err != nil {
		t.Fatal(err)
	}
	docs, _, err = s.Docs(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Indexer != forms[0] || docs[0].Version != "v2" || docs[0].Generation != 2 {
		t.Errorf("after a re-put: %+v; want the identity kept, v2 and generation 2", docs[0])
	}
}

// Cell 4 — per-chunker versions, as each document's own indexer. Two chunker versions in one
// tenant: re-putting one chunker's documents changes only their generations and rows, and Docs
// gives each path its own string, so a consumer's outdated check re-cuts exactly the chunker's
// paths and nothing else.
func TestEachPathCarriesItsOwnIndexer(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	s, err := Open(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	oldGo, newGo, md := "c.go@code-go-1+text-5.s3.t512", "c.go@code-go-2+text-5.s3.t512", "m.md"
	for _, d := range []Document{
		{Path: "a.go", Indexer: oldGo, Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "pkg > A", Body: "go words one"}}},
		{Path: "b.go", Indexer: oldGo, Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "pkg > B", Body: "go words two"}}},
		{Path: "n.md", Indexer: md, Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "n > head", Body: "md words"}}},
	} {
		if _, err := s.Put(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	// the version bump re-puts only the Go documents
	for _, d := range []Document{
		{Path: "a.go", Indexer: newGo, Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "pkg > A", Body: "go words one"}}},
		{Path: "b.go", Indexer: newGo, Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "pkg > B", Body: "go words two"}}},
	} {
		if _, err := s.Put(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	docs, more, err := s.Docs(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if more || len(docs) != 3 {
		t.Fatalf("docs = %d", len(docs))
	}
	by := map[string]DocInfo{}
	for _, d := range docs {
		by[d.Path] = d
	}
	if by["a.go"].Indexer != newGo || by["b.go"].Indexer != newGo {
		t.Errorf("the Go paths' indexers = %q, %q; want the bumped %q", by["a.go"].Indexer, by["b.go"].Indexer, newGo)
	}
	if by["n.md"].Indexer != md {
		t.Errorf("the markdown path's indexer = %q; want its own %q untouched", by["n.md"].Indexer, md)
	}
	if by["a.go"].Generation != 2 || by["b.go"].Generation != 2 {
		t.Errorf("the re-put paths' generations = %d, %d; want 2", by["a.go"].Generation, by["b.go"].Generation)
	}
	if by["n.md"].Generation != 1 {
		t.Errorf("the untouched path's generation = %d; want 1", by["n.md"].Generation)
	}
	// paging: after a path in path order, the rest follow, and more says whether any are left
	page, more, err := s.Docs(ctx, "a.go", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Path != "b.go" || page[1].Path != "n.md" || more {
		t.Fatalf("a page after a.go = %+v, more %v", page, more)
	}
}

// Cell 5 — stemming, stop words, tags and title.
//
// The rag_english configuration stems (a body with "dance track" answers "dancing tracks") and
// removes no stop word: "not" finds "not ready", "not ready" excludes a body with only "ready",
// and "the" finds a body containing "the". The mutant, named here: build the baseline with the
// built-in english configuration (or a StopWords dictionary) — the first query then answers
// nothing ("not" is a stop word, dropped at index and query time), and the second admits "ready",
// which the every-word contract forbids. The synthetic searchtest corpus cannot catch this: its
// words are made up to avoid stemmer and stop-list interactions.
//
// The schema-resolution half runs the same stop-word cell a second time with the migrations and
// the queries in a non-public schema reached through the connection's search_path, as a workspace
// whose destination names its own schema does: the configuration is created and resolved by name,
// never by a qualified one.
func TestRagEnglishStemsAndKeepsEveryWord(t *testing.T) {
	ragEnglishCells(t, false)
}

func TestRagEnglishResolvesThroughTheSearchPath(t *testing.T) {
	ragEnglishCells(t, true)
}

func ragEnglishCells(t *testing.T, ownSchema bool) {
	t.Helper()
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsnBase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := scratchName(t, admin, "autodoc_pgstore_")
	conn, err := postgres.OpenNamed(ctx, name, dsnOf(t, admin, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if ownSchema {
		schema := "dest_" + name
		if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
		conn = schemaDSN(t, dsnOf(t, admin, name), schema)
	}
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	s, err := Open(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetModels(ctx, model, ""); err != nil {
		t.Fatal(err)
	}
	// stemming, carried from the old destination test: "dancing tracks" finds "dance track"
	stem := Document{Path: "stem.md", Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "stem", Body: "an upbeat electronic dance track with female vocals"}}}
	putDoc(t, s, stem, false)
	if got := lexicalPaths(t, s, "dancing tracks"); strings.Join(got, ",") != "stem.md" {
		t.Errorf("stemming: %q = %v, want stem.md", "dancing tracks", got)
	}
	// a word that appears only in a document's tags finds it lexically
	tagged := Document{Path: "tagged.md", Tags: []string{"pump"}, Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "tagged", Body: "words about nothing else"}}}
	if _, err := s.Put(ctx, tagged); err != nil {
		t.Fatal(err)
	}
	if got := lexicalPaths(t, s, "pump"); strings.Join(got, ",") != "tagged.md" {
		t.Errorf("a tag-only word = %v, want tagged.md", got)
	}
	// stop words: "not" finds "not ready"; "not ready" excludes "ready"; "the" finds a body with "the"
	notReady := Document{Path: "notready.md", Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "nr", Body: "the generator is not ready"}}}
	ready := Document{Path: "ready.md", Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "r", Body: "everything is ready now"}}}
	for _, d := range []Document{notReady, ready} {
		if _, err := s.Put(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if got := lexicalPaths(t, s, "not"); strings.Join(got, ",") != "notready.md" {
		t.Errorf("query %q = %v, want notready.md and no other", "not", got)
	}
	if got := lexicalPaths(t, s, "not ready"); strings.Join(got, ",") != "notready.md" {
		t.Errorf("query %q = %v, want notready.md: the body with no \"not\" fails the every-word contract", "not ready", got)
	}
	if got := lexicalPaths(t, s, "the"); strings.Join(got, ",") != "notready.md" {
		t.Errorf("query %q = %v, want notready.md (its body contains \"the\")", "the", got)
	}
	// the title, at the top weight: a registered-style chunk whose breadcrumb never contains the
	// file name is found by the title's word alone, and ranks above a chunk with the word only in
	// its body. The mutant, named here: drop title from tsv() and the baseline's generated column —
	// the title-only query then answers nothing, and this fails with no hit.
	titled := Document{Path: "core/index/hold.go", Title: "hold",
		Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "core/index > ParseIdentity", Body: "parses the identity a document was cut under"}}}
	bodied := Document{Path: "body/word.md", Title: "other",
		Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "body > section", Body: "the word hold appears only in this body"}}}
	for _, d := range []Document{titled, bodied} {
		if _, err := s.Put(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if got := lexicalPaths(t, s, "hold"); len(got) == 0 || got[0] != "core/index/hold.go" {
		t.Errorf("a title-only word = %v, want core/index/hold.go first", got)
	}
}

// Cell 6 — Drop removes one tenant and nothing of another's: no row of it stays in any table.
func TestDropRemovesOneTenantOnly(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	a, err := Open(conn, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(conn, "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Store{a, b} {
		if err := s.SetModels(ctx, model, ""); err != nil {
			t.Fatal(err)
		}
		d := Document{Path: "shared.md", Tags: []string{"x"}, Links: []string{"shared.md"},
			Chunks: []search.Chunk{{Ord: 0, Breadcrumb: "shared", Body: "common words"}}}
		putDoc(t, s, d, true)
	}
	if err := a.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	// no row of the dropped tenant stays in any table
	for _, table := range []struct{ name, tenantCol string }{
		{"rag_document", "tenant"},
		{"rag_chunk", "tenant"},
		{"rag_link", "tenant"},
		{"rag_embedding", "tenant"},
		{"rag_meta", "tenant"},
	} {
		rows, err := conn.QueryContext(ctx, "SELECT count(*) FROM "+table.name+" WHERE "+table.tenantCol+" = 'tenant-a'")
		if err != nil {
			t.Fatal(err)
		}
		var n int64
		for rows.Next() {
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
		}
		_ = rows.Close()
		if n != 0 {
			t.Errorf("%d rows of tenant a outlived Drop in %s", n, table.name)
		}
	}
	// tenant b answers as before
	b.View(ctx, func(v *View) error {
		if got, err := v.Lexical(ctx, []search.Term{{Text: "common"}}, search.Filter{}, 10); err != nil || len(got) != 1 || got[0].Path != "shared.md" {
			t.Errorf("tenant b after a's drop: %+v, %v", got, err)
		}
		if st, err := v.SemanticState(ctx); err != nil || st != search.StateReady {
			t.Errorf("tenant b's state after a's drop: %v, %v", st, err)
		}
		return nil
	})
}

// Cell 7 — Migrate applies once from empty, then again with nothing pending, recorded in the
// autodoc_schema ledger; and a database whose ledger records the deleted destination's old
// baseline is refused with ErrDowngrade before anything runs, leaving its rows and the rag_*
// tables exactly as they were. The mutant, named here: give the new baseline the old file's name
// — the runner then warns on the digest mismatch and SKIPS the recorded baseline instead of
// applying it, so Migrate returns no error; the ErrDowngrade assertion is what kills it, not the
// rag_* check.
func TestMigrateRecordsAndRefusesAnOldLedger(t *testing.T) {
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsnBase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })

	// applied once from empty, then again with nothing pending
	name := scratchName(t, admin, "autodoc_pgstore_")
	conn, err := postgres.OpenNamed(ctx, name, dsnOf(t, admin, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err = Migrate(ctx, conn); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err = Migrate(ctx, conn); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	n := ledgerRows(t, conn)
	if n != 1 {
		t.Errorf("the ledger holds %d rows, want 1", n)
	}

	// a destination that holds the deleted scripts: the old ledger names a script this binary does
	// not have, so the refusal comes before any pending script runs, inside the transaction
	old := scratchName(t, admin, "autodoc_pgstore_")
	oldConn, err := postgres.OpenNamed(ctx, old, dsnOf(t, admin, old))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { oldConn.Close() })
	if _, err := oldConn.ExecContext(ctx, "CREATE TABLE autodoc_schema (script TEXT PRIMARY KEY, sha256 TEXT, applied_at INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := oldConn.ExecContext(ctx, "INSERT INTO autodoc_schema (script, sha256, applied_at) VALUES ('000001_update_initialize_index.sql', '0123456789abcdef', 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := oldConn.ExecContext(ctx, "CREATE TABLE sentinel (v INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := oldConn.ExecContext(ctx, "INSERT INTO sentinel VALUES (42)"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, oldConn); !errors.Is(err, deploy.ErrDowngrade) {
		t.Fatalf("migrate onto the old ledger = %v; want ErrDowngrade and nothing applied", err)
	}
	// nothing of the new schema exists afterwards, and the old rows are unchanged: the refusal
	// happens inside the migration transaction, before any pending script runs
	for _, check := range []struct{ q, want string }{
		{"SELECT count(*) FROM information_schema.tables WHERE table_name LIKE 'rag_%'", "0"},
		{"SELECT count(*) FROM pg_ts_config WHERE cfgname = 'rag_english'", "0"},
		{"SELECT count(*) FROM pg_ts_config WHERE cfgname = 'rag_english_stem'", "0"},
		{"SELECT count(*) FROM pg_ts_dict WHERE dictname = 'rag_english_stem'", "0"},
		{"SELECT script FROM autodoc_schema", "000001_update_initialize_index.sql"},
		{"SELECT v::text FROM sentinel", "42"},
	} {
		rows, err := oldConn.QueryContext(ctx, check.q)
		if err != nil {
			t.Fatalf("%s: %v", check.q, err)
		}
		var got string
		for rows.Next() {
			if err := rows.Scan(&got); err != nil {
				t.Fatal(err)
			}
		}
		_ = rows.Close()
		if got != check.want {
			t.Errorf("after the refusal: %s = %q, want %q", check.q, got, check.want)
		}
	}
}

func ledgerRows(t *testing.T, conn dao.DataConn) int {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), "SELECT script FROM autodoc_schema")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for rows.Next() {
		n++
	}
	_ = rows.Close()
	return n
}

// scratchName creates a uniquely named scratch database on admin and registers its drop.
func scratchName(t *testing.T, admin dao.DataConn, prefix string) string {
	t.Helper()
	name := prefix + randHex(t)
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create the scratch database: %v", err)
	}
	t.Cleanup(func() { admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	return name
}

// dsnOf is base's DSN with its database replaced by name.
func dsnOf(t *testing.T, base dao.DataConn, name string) string {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

func randHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// embedVec is the deterministic test vector of a text, as searchtest.Embed.
func embedVec(text string) []float32 {
	return searchtest.Embed(text)
}

func hashOf(text string) [32]byte {
	return search.Chunk{Embed: text}.TextHash()
}
