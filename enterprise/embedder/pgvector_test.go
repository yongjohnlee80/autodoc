package embedder_test

import (
	"context"
	"math"
	"slices"
	"strconv"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/vector"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
)

// pg is a tenant's pgstore on a scratch database with pgvector, and the pool it is on.
type pg struct {
	t    *testing.T
	ctx  context.Context
	conn dao.DataConn
	st   *pgstore.Store
}

func newPG(t *testing.T) *pg {
	t.Helper()
	conn := pgtest.Scratch(t, pgstore.Migrate)
	st, err := pgstore.New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	return &pg{t: t, ctx: context.Background(), conn: conn, st: st}
}

// chunk is a chunk whose embed text is crumb + "\n" + body.
func chunk(crumb, body string) search.Chunk { return search.Chunk{Breadcrumb: crumb, Body: body} }

func (p *pg) put(path string, chunks ...search.Chunk) {
	p.t.Helper()
	if _, err := p.st.Put(p.ctx, pgstore.Document{Path: path, Chunks: chunks}); err != nil {
		p.t.Fatal(err)
	}
}

func (p *pg) scalar(q string, args ...any) string {
	p.t.Helper()
	rows, err := p.conn.QueryContext(p.ctx, q, args...)
	if err != nil {
		p.t.Fatal(err)
	}
	defer rows.Close()
	var s string
	if !rows.Next() {
		p.t.Fatalf("%s: no row", q)
	}
	if err := rows.Scan(&s); err != nil {
		p.t.Fatal(err)
	}
	return s
}

// vectors is how many vectors the tenant has under model.
func (p *pg) vectors(model string) int {
	n, _ := strconv.Atoi(p.scalar(`SELECT count(*)::text FROM rag_embedding WHERE tenant = 't' AND model = $1`, model))
	return n
}

// unready are the tenant's unready documents.
func (p *pg) unready() []string {
	p.t.Helper()
	docs, _, err := p.st.Unready(p.ctx, "", 1000)
	if err != nil {
		p.t.Fatal(err)
	}
	out := []string{}
	for _, d := range docs {
		out = append(out, d.Path)
	}
	return out
}

func (p *pg) models() (string, string) {
	p.t.Helper()
	a, tg, err := p.st.Models(p.ctx)
	if err != nil {
		p.t.Fatal(err)
	}
	return a, tg
}

func (p *pg) state() search.State {
	p.t.Helper()
	var st search.State
	err := p.st.View(p.ctx, func(v *pgstore.View) error {
		var err error
		st, err = v.SemanticState(p.ctx)
		return err
	})
	if err != nil {
		p.t.Fatal(err)
	}
	return st
}

// fill embeds every text with prov until nothing is unready, then stops the worker.
func (p *pg) fill(prov *provider) {
	p.t.Helper()
	stop := run(p.t, embedder.New(built(prov), []embedder.Tenant{{Name: "t", Store: p.st}}))
	waitFor(p.t, "every document ready under "+prov.name, func() bool { return len(p.unready()) == 0 })
	stop()
}

// Texts are embedded once each into pgvector, a text two documents share once, and normalized;
// every document is then ready, and searching by meaning finds it. A document written later with a
// text already embedded is marked ready without the text being embedded again. The mutant, named
// here: no marking of unready documents when nothing is pending (the later document stays
// unready).
func TestTextsAreEmbeddedOnceIntoPgvector(t *testing.T) {
	p := newPG(t)
	shared := chunk("s", "shared words")
	p.put("a.md", chunk("a", "alpha"), shared)
	p.put("b.md", shared)
	p.put("c.md", chunk("c", "gamma"))
	prov := newProvider("m1")
	w := embedder.New(built(prov), []embedder.Tenant{{Name: "t", Store: p.st}}, embedder.WithBatch(2), embedder.WithSlots(2))
	run(t, w)
	waitFor(t, "every document ready", func() bool { return len(p.unready()) == 0 })
	fp := fingerprint("m1")
	if a, tg := p.models(); a != fp || tg != "" {
		t.Errorf("models %q %q, want %q active", a, tg, fp)
	}
	if prov.texts() != 3 || prov.times("s\nshared words") != 1 || prov.times("a\nalpha") != 1 || prov.times("c\ngamma") != 1 {
		t.Errorf("the provider embedded %d texts: shared %d times, alpha %d, gamma %d", prov.texts(),
			prov.times("s\nshared words"), prov.times("a\nalpha"), prov.times("c\ngamma"))
	}
	if n := p.vectors(fp); n != 3 {
		t.Errorf("%d vectors stored, want 3", n)
	}
	lo, _ := strconv.ParseFloat(p.scalar(`SELECT min(vector_norm(vec))::text FROM rag_embedding`), 64)
	hi, _ := strconv.ParseFloat(p.scalar(`SELECT max(vector_norm(vec))::text FROM rag_embedding`), 64)
	if math.Abs(lo-1) > 1e-4 || math.Abs(hi-1) > 1e-4 {
		t.Errorf("vector norms from %v to %v, want 1", lo, hi)
	}
	var hits []string
	err := p.st.View(p.ctx, func(v *pgstore.View) error {
		got, err := v.Semantic(p.ctx, fp, vector.Normalize(vec("c\ngamma", 4)), search.Filter{}, 1)
		for _, h := range got {
			hits = append(hits, h.Path)
		}
		return err
	})
	if err != nil || !slices.Equal(hits, []string{"c.md"}) {
		t.Errorf("by meaning: %v, %v; want c.md", hits, err)
	}
	if st := p.state(); st != search.StateReady {
		t.Errorf("semantic state %q", st)
	}

	p.put("d.md", shared)
	w.Wake()
	waitFor(t, "d.md ready", func() bool { return len(p.unready()) == 0 })
	if prov.times("s\nshared words") != 1 {
		t.Errorf("the shared text embedded %d times", prov.times("s\nshared words"))
	}
}

// A model change is a switch: the new model is the target while it fills, the old one stays
// active and searches answer by words (switching); once every text is embedded or set aside,
// every document is marked by the new model, it becomes active, and the old model's vectors are
// reclaimed. A document whose text the new model rejects is not ready under it, though it was
// under the old. The mutants, named here: the flip without marking every document (c.md keeps the
// old model's readiness), and no reclaim (the old vectors stay).
func TestAModelSwitchAnswersByWordsUntilItFlips(t *testing.T) {
	p := newPG(t)
	p.put("a.md", chunk("a", "alpha"))
	p.put("b.md", chunk("b", "beta"))
	p.put("c.md", chunk("c", "gamma"))
	p.fill(newProvider("m1"))
	old, next := fingerprint("m1"), fingerprint("m2")
	if p.vectors(old) != 3 {
		t.Fatalf("%d vectors under the first model", p.vectors(old))
	}
	prov := newProvider("m2")
	prov.reject["c\ngamma"], prov.always = true, true
	prov.hold = make(chan struct{})
	run(t, embedder.New(built(prov), []embedder.Tenant{{Name: "t", Store: p.st}}, embedder.WithBatch(1), embedder.WithSlots(1)))
	waitFor(t, "the new model the target", func() bool { a, tg := p.models(); return a == old && tg == next })
	if st := p.state(); st != search.StateSwitching {
		t.Errorf("while the new model fills: %q, want switching", st)
	}
	if p.vectors(old) != 3 {
		t.Errorf("the old vectors went while the new model fills: %d", p.vectors(old))
	}
	close(prov.hold)
	waitFor(t, "the flip", func() bool { a, tg := p.models(); return a == next && tg == "" })
	waitFor(t, "the old vectors reclaimed", func() bool { return p.vectors(old) == 0 })
	if n := p.vectors(next); n != 2 {
		t.Errorf("%d vectors under the new model, want 2 (gamma rejected)", n)
	}
	if got := p.unready(); !slices.Equal(got, []string{"c.md"}) {
		t.Errorf("unready after the flip: %v, want c.md, whose text the new model rejects", got)
	}
	if st := p.state(); st != search.StatePartial {
		t.Errorf("after the flip: %q, want partial", st)
	}
}

// A switch whose target is no longer the configured model is cancelled: the active model, which
// is the configured one, stays, and the abandoned target's vectors are reclaimed. The mutant,
// named here: an active model left with its stale target (the tenant stays switching).
func TestACancelledSwitchKeepsTheActiveModel(t *testing.T) {
	p := newPG(t)
	p.put("a.md", chunk("a", "alpha"))
	prov := newProvider("m1")
	p.fill(prov)
	active, gone := fingerprint("m1"), "fake|gone||4"
	if err := p.st.SetModels(p.ctx, active, gone); err != nil {
		t.Fatal(err)
	}
	if err := p.st.Embed(p.ctx, gone, []pgstore.Embedding{{TextHash: chunk("a", "alpha").TextHash(), Vec: []float32{1, 0, 0, 0}}}); err != nil {
		t.Fatal(err)
	}
	run(t, embedder.New(built(prov), []embedder.Tenant{{Name: "t", Store: p.st}}))
	waitFor(t, "the switch cancelled", func() bool { a, tg := p.models(); return a == active && tg == "" })
	waitFor(t, "the abandoned target's vectors reclaimed", func() bool { return p.vectors(gone) == 0 })
	if p.vectors(active) != 1 || len(p.unready()) != 0 || prov.times("a\nalpha") != 1 {
		t.Errorf("after the cancel: %d active vectors, unready %v, alpha embedded %d times", p.vectors(active), p.unready(), prov.times("a\nalpha"))
	}
}
