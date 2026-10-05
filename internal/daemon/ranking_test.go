package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// tei is a fake TEI server serving a model of modelType ("reranker" ranks), counting its /info
// calls; each text scores its length.
type tei struct {
	*httptest.Server
	infos atomic.Int64
}

func newTEI(t *testing.T, modelType string) *tei {
	t.Helper()
	s := &tei{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			s.infos.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"model_id": "fake/" + modelType,
				"model_type": map[string]any{modelType: map[string]any{}}, "max_client_batch_size": 8})
		case "/rerank":
			var req struct{ Texts []string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			out := []map[string]any{}
			for i, x := range req.Texts {
				out = append(out, map[string]any{"index": i, "score": float64(len(x))})
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// buildRanker is a build's own ranker: failing when err is set.
type buildRanker struct{ err error }

func (b buildRanker) Rank(_ context.Context, _ string, texts []string) ([]float64, error) {
	if b.err != nil {
		return nil, b.err
	}
	return make([]float64, len(texts)), nil
}

func (buildRanker) Model() rank.Model {
	return rank.Model{Provider: "build", Name: "build-ranker", MaxBatch: 100}
}

// rankingOver is a started Ranking over db, the build supplying s; stop ends it and waits for its
// last calls to be written.
func rankingOver(t *testing.T, db *store.Store, s Supplied) (r *Ranking, h *rank.Holder, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h = &rank.Holder{}
	r = NewRanking(db, h, s, nil)
	r.Start(ctx)
	stop = sync.OnceFunc(func() { cancel(); r.Wait() })
	t.Cleanup(stop)
	return r, h, stop
}

// rankStore is a store with the TEI rankers named, at srv.
func rankStore(t *testing.T, srv *tei, names ...string) *store.Store {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, name := range names {
		if _, err := db.AddRanker(ctx, store.RankerSpec{Name: name, Kind: store.KindTEI, BaseURL: srv.URL}); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func prefsOf(t *testing.T, db *store.Store) map[string]string {
	t.Helper()
	p, err := db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestARankerUsedIsHeldAndRestored: a ranker used is set in the Holder at the default window and
// remembered; a window set reaches the Holder and the store; a daemon started on that store
// holds the same ranker at the same window; none used empties the Holder.
func TestARankerUsedIsHeldAndRestored(t *testing.T) {
	db := rankStore(t, newTEI(t, "reranker"), "t")
	r, h, stop := rankingOver(t, db, Supplied{})
	ctx := context.Background()
	if err := r.Use(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if live, w := h.Current(); live == nil || w != rank.DefaultWindow {
		t.Fatalf("held after Use: %v at %d", live, w)
	}
	if err := r.SetWindow(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if _, w := h.Current(); w != 20 {
		t.Errorf("window held after SetWindow(20): %d", w)
	}
	for _, n := range []int{rank.MinWindow - 1, rank.MaxWindow + 1} {
		if err := r.SetWindow(ctx, n); !errors.Is(err, rpc.ErrWindow) {
			t.Errorf("SetWindow(%d): %v, want ErrWindow", n, err)
		}
	}
	if p := prefsOf(t, db); p[store.PrefRanker] != "t" || p[store.PrefRankerWindow] != "20" {
		t.Errorf("stored: %v", p)
	}
	stop()

	r2, h2, _ := rankingOver(t, db, Supplied{})
	if live, w := h2.Current(); live == nil || w != 20 {
		t.Fatalf("held after a restart: %v at %d, want the ranker at 20", live, w)
	}
	if l, err := r2.ListRankers(ctx); err != nil || l.Active != "t" || l.Window != 20 || l.Err != "" || l.Supplied != "" {
		t.Errorf("listed after a restart: %+v, %v", l, err)
	}
	if err := r2.Use(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if live, _ := h2.Current(); live != nil {
		t.Error("a ranker held after Use(\"\")")
	}
}

// TestAUseThatDoesNotSetUpChangesNothing: a ranker that is no re-ranker (TEI serving an
// embedder) is refused, and one whose choice cannot be written is not held: the one in use stays
// held, the store names it, and the list carries no reason (the caller had it).
func TestAUseThatDoesNotSetUpChangesNothing(t *testing.T) {
	db := rankStore(t, newTEI(t, "reranker"), "t")
	ctx := context.Background()
	if _, err := db.AddRanker(ctx, store.RankerSpec{Name: "embedder", Kind: store.KindTEI, BaseURL: newTEI(t, "embedding").URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddRanker(ctx, store.RankerSpec{Name: "t2", Kind: store.KindTEI, BaseURL: newTEI(t, "reranker").URL}); err != nil {
		t.Fatal(err)
	}
	r, h, _ := rankingOver(t, db, Supplied{})
	if err := r.Use(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	was, _ := h.Current()
	if err := r.Use(ctx, "embedder"); !errors.Is(err, rank.ErrNotARanker) {
		t.Fatalf("Use of an embedder: %v, want ErrNotARanker", err)
	}
	r.db = unwritable{db}
	if err := r.Use(ctx, "t2"); err == nil {
		t.Fatal("Use succeeded with its preference unwritten")
	}
	r.db = db
	if now, _ := h.Current(); now != was {
		t.Error("a refused Use changed the ranker held")
	}
	if l, _ := r.ListRankers(ctx); l.Active != "t" || l.Err != "" {
		t.Errorf("listed after the refused Uses: active %q, err %q", l.Active, l.Err)
	}
	if p := prefsOf(t, db); p[store.PrefRanker] != "t" {
		t.Errorf("the store names %q, want t", p[store.PrefRanker])
	}
}

// TestAnEditOfTheRankerInUse: an edit that is no ranker the store would keep is refused before the
// ranker is asked anything; one that does not set up is refused with nothing written; one that
// sets up is written and held, the preference following its rename.
func TestAnEditOfTheRankerInUse(t *testing.T) {
	srv := newTEI(t, "reranker")
	db := rankStore(t, srv, "t")
	r, h, _ := rankingOver(t, db, Supplied{})
	ctx := context.Background()
	if err := r.Use(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	was, _ := h.Current()
	asked := srv.infos.Load()
	if err := r.UpdateRanker(ctx, "t", store.RankerSpec{Name: " ", Kind: store.KindTEI, BaseURL: srv.URL}); !errors.Is(err, store.ErrRankerInvalid) {
		t.Fatalf("an edit with no name: %v, want ErrRankerInvalid", err)
	}
	if srv.infos.Load() != asked {
		t.Error("an invalid edit was set up before it was refused")
	}
	if err := r.UpdateRanker(ctx, "t", store.RankerSpec{Name: "t", Kind: store.KindTEI, BaseURL: newTEI(t, "embedding").URL}); !errors.Is(err, rank.ErrNotARanker) {
		t.Fatalf("an edit to an embedder: %v, want ErrNotARanker", err)
	}
	if info, _, err := db.RankerWithKey(ctx, "t"); err != nil || info.BaseURL != srv.URL {
		t.Errorf("stored after the refused edit: %+v, %v", info, err)
	}
	if now, _ := h.Current(); now != was {
		t.Error("a refused edit changed the ranker held")
	}
	if err := r.UpdateRanker(ctx, "t", store.RankerSpec{Name: "t2", Kind: store.KindTEI, BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if now, _ := h.Current(); now == was || now == nil {
		t.Error("the edit that set up is not held")
	}
	if l, _ := r.ListRankers(ctx); l.Active != "t2" || prefsOf(t, db)[store.PrefRanker] != "t2" {
		t.Errorf("after the rename: active %q, preference %q", l.Active, prefsOf(t, db)[store.PrefRanker])
	}
	if err := r.RemoveRanker(ctx, "t2"); err != nil {
		t.Fatal(err)
	}
	if now, _ := h.Current(); now != nil {
		t.Error("the ranker removed is still held")
	}
}

// TestARankerThatDoesNotStartIsListedWithItsReason: the ranker the store names, not answering at
// startup, leaves the Holder empty and the reason listed, until it is removed.
func TestARankerThatDoesNotStartIsListedWithItsReason(t *testing.T) {
	srv := newTEI(t, "reranker")
	db := rankStore(t, srv, "t")
	ctx := context.Background()
	if err := db.SetPreference(ctx, store.PrefRanker, "t"); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	r, h, _ := rankingOver(t, db, Supplied{})
	if live, _ := h.Current(); live != nil {
		t.Fatal("a ranker that did not answer is held")
	}
	if l, _ := r.ListRankers(ctx); l.Active != "" || l.Err == "" {
		t.Fatalf("listed: active %q, err %q; want none, with the reason", l.Active, l.Err)
	}
	if err := r.RemoveRanker(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if l, _ := r.ListRankers(ctx); l.Err != "" {
		t.Errorf("the removed ranker's reason is still listed: %q", l.Err)
	}
}

// TestTheBuildsRankerIsFixedAndTheSelectionKept: a build's ranker is held at its window in place
// of the stored selection; the selection and its window cannot be changed or its row edited or
// removed, while the other stored rankers can; a daemon without the build's ranker restores the
// selection as it was.
func TestTheBuildsRankerIsFixedAndTheSelectionKept(t *testing.T) {
	srv := newTEI(t, "reranker")
	db := rankStore(t, srv, "t", "u")
	ctx := context.Background()
	r0, _, stop := rankingOver(t, db, Supplied{})
	if err := r0.Use(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if err := r0.SetWindow(ctx, 25); err != nil {
		t.Fatal(err)
	}
	stop()

	b := buildRanker{}
	r, h, stop := rankingOver(t, db, Supplied{Ranker: b, Window: 15})
	if live, w := h.Current(); live != rank.Ranker(b) || w != 15 {
		t.Fatalf("held under a build's ranker: %v at %d", live, w)
	}
	spec := store.RankerSpec{Name: "t", Kind: store.KindTEI, BaseURL: srv.URL}
	for what, err := range map[string]error{
		"use":           r.Use(ctx, "u"),
		"use none":      r.Use(ctx, ""),
		"window":        r.SetWindow(ctx, 30),
		"edit the kept": r.UpdateRanker(ctx, "t", spec),
		"remove kept":   r.RemoveRanker(ctx, "t"),
	} {
		if !errors.Is(err, rpc.ErrSuppliedRanker) {
			t.Errorf("%s under a build's ranker: %v, want ErrSuppliedRanker", what, err)
		}
	}
	if err := r.UpdateRanker(ctx, "u", store.RankerSpec{Name: "u2", Kind: store.KindTEI, BaseURL: srv.URL}); err != nil {
		t.Errorf("edit of another ranker: %v", err)
	}
	if err := r.RemoveRanker(ctx, "u2"); err != nil {
		t.Errorf("remove of another ranker: %v", err)
	}
	if l, err := r.ListRankers(ctx); err != nil || l.Supplied != "build-ranker" || l.Active != "" || l.Window != 15 {
		t.Errorf("listed under a build's ranker: %+v, %v", l, err)
	}
	if m, ok := r.Supplied(); !ok || m != "build-ranker" {
		t.Errorf("Supplied: %q, %v", m, ok)
	}
	if now, _ := h.Current(); now != rank.Ranker(b) {
		t.Error("the build's ranker was replaced")
	}
	stop()

	_, h3, _ := rankingOver(t, db, Supplied{})
	if live, w := h3.Current(); live == nil || w != 25 {
		t.Errorf("held without the build's ranker: %v at %d, want t at 25", live, w)
	}
}

// TestABuildsRankerThatFailsItsProbeIsHeldAnyway: never replaced by a stored ranker; the list
// says why.
func TestABuildsRankerThatFailsItsProbeIsHeldAnyway(t *testing.T) {
	db := rankStore(t, newTEI(t, "reranker"), "t")
	ctx := context.Background()
	if err := db.SetPreference(ctx, store.PrefRanker, "t"); err != nil {
		t.Fatal(err)
	}
	b := buildRanker{err: rank.ErrUnreachable}
	r, h, _ := rankingOver(t, db, Supplied{Ranker: b})
	if live, w := h.Current(); live != rank.Ranker(b) || w != rank.DefaultWindow {
		t.Fatalf("held: %v at %d, want the build's ranker at the default", live, w)
	}
	if l, _ := r.ListRankers(ctx); l.Err == "" || l.Supplied != "build-ranker" {
		t.Errorf("listed: %+v; want the build's ranker and its probe's failure", l)
	}
}

// TestTheRankersCallsAreMetered: the probe and each search's call reach the ranker's usage and
// log once the daemon stops.
func TestTheRankersCallsAreMetered(t *testing.T) {
	db := rankStore(t, newTEI(t, "reranker"), "t")
	r, h, stop := rankingOver(t, db, Supplied{})
	ctx := context.Background()
	if err := r.Use(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	live, _ := h.Current()
	for range 3 {
		if _, err := live.Rank(ctx, "q", []string{"a", "bb"}); err != nil {
			t.Fatal(err)
		}
	}
	stop()
	us, err := db.RankerUsage(ctx, "t", 1)
	if err != nil {
		t.Fatal(err)
	}
	var calls, texts int64
	for _, u := range us {
		calls, texts = calls+u.Requests, texts+u.Texts
	}
	if calls != 4 || texts != 8 { // the probe, then three searches, two texts each
		t.Errorf("usage: %d calls of %d texts, want 4 of 8", calls, texts)
	}
	if log, err := db.RankerLog(ctx, "t", 10); err != nil || len(log) != 4 {
		t.Errorf("log: %d entries, %v; want 4", len(log), err)
	}
}

// TestALiveTEIRanker (TEST_TEI_RERANK_URL, a TEI server of a re-ranker): a stored TEI ranker set
// up, probed and used ranks a query's answer above the texts that are not, and its calls are
// metered.
func TestALiveTEIRanker(t *testing.T) {
	url := os.Getenv("TEST_TEI_RERANK_URL")
	if url == "" {
		t.Skip("TEST_TEI_RERANK_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.AddRanker(ctx, store.RankerSpec{Name: "tei", Kind: store.KindTEI, BaseURL: url}); err != nil {
		t.Fatal(err)
	}
	r, h, stop := rankingOver(t, db, Supplied{})
	if models, err := r.Models(ctx, "tei", store.RankerSpec{}); err != nil || len(models) != 1 {
		t.Fatalf("models: %v, %v", models, err)
	}
	if err := r.Use(ctx, "tei"); err != nil {
		t.Fatal(err)
	}
	live, _ := h.Current()
	texts := []string{
		"Gardening\nWater tomatoes in the morning, at the roots.",
		"Release process\nTag the commit on main, then watch the release workflow publish the binaries.",
		"Cooking\nKnead the dough for ten minutes before it rises.",
	}
	scores, err := live.Rank(ctx, "how do I publish a release?", texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 3 || scores[1] <= scores[0] || scores[1] <= scores[2] {
		t.Errorf("scores %v: want the release text first", scores)
	}
	stop()
	us, err := db.RankerUsage(ctx, "tei", 1)
	if err != nil || len(us) != 1 || us[0].Requests != 2 || us[0].Failures != 0 { // the probe, then the query
		t.Errorf("usage %+v, %v", us, err)
	}
}
