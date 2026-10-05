package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
	"github.com/yongjohnlee80/golib/search/rank"
	tuicore "github.com/yongjohnlee80/golib/tui"

	"github.com/yongjohnlee80/autodoc/core/store"
	serving "github.com/yongjohnlee80/autodoc/internal/daemon"
)

// fakeTEI is a TEI server of a re-ranker, each text scoring its length.
func fakeTEI(t *testing.T) *httptest.Server { return fakeTEIOf(t, "reranker") }

// fakeTEIOf is a TEI server of a model of modelType ("embedding": not a re-ranker).
func fakeTEIOf(t *testing.T, modelType string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			_ = json.NewEncoder(w).Encode(map[string]any{"model_id": "BAAI/bge-reranker-v2-m3",
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
	t.Cleanup(srv.Close)
	return srv
}

func nextTab() tuicore.Event {
	return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageDown, Mods: tuicore.ModCtrl}
}

// rankerTabOpen opens AI models on its Ranker Models tab.
func rankerTabOpen(t *testing.T, r *running) {
	t.Helper()
	r.leader(t, 'a') // System › AI models: on the tab it was left on
	if onLoop(r, func() int { return r.h.aiTab }) != rankerTab {
		r.s.WaitForText(t, "embedding providers")
		r.keys(t, nextTab())
	}
	r.s.WaitFor(t, "the ranker tab", func(sc string) bool {
		return strings.Contains(sc, "rankers") && !strings.Contains(sc, "embedding providers")
	})
	r.s.WaitForText(t, "re-ranking")
}

// TestTheRankerModelsTab: the stored rankers on their own tab; Use there uses the ranker under the
// cursor, the search's hits saying by which model they are ordered; the embedding tab's own keys
// do nothing there; Don't use stops it; Remove… asks, then removes it.
func TestTheRankerModelsTab(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n\nkestrel\n", "b.md", "# B\n\nkestrel in a longer note\n")})
	ctx := context.Background()
	if _, err := d.db.AddRanker(ctx, store.RankerSpec{Name: "tei", Kind: store.KindTEI, BaseURL: fakeTEI(t).URL}); err != nil {
		t.Fatal(err)
	}
	o := newFakeOllama(t, "embedder")
	if _, err := d.db.AddProvider(ctx, store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	if _, err := r.h.session.Call(ctx, "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	rankerTabOpen(t, r)
	r.s.WaitForText(t, "not re-ranking: Add… a ranker, then Use it")
	r.s.WaitForText(t, "tei           TEI")
	r.keys(t, key('w')) // Words only: not this tab's
	r.keys(t, key('u')) // Use, the first row
	r.s.WaitForText(t, "re-ranking with tei over the top 40 candidates")
	if p, _ := d.db.Preferences(ctx); p[store.PrefRanker] != "tei" {
		t.Fatalf("the store names %q", p[store.PrefRanker])
	}
	if p, _ := d.db.Preferences(ctx); p[store.PrefProvider] != "local" {
		t.Errorf("Words only reached the embedding tab: the provider in use is %q", p[store.PrefProvider])
	}
	r.keys(t, key('q'))
	r.s.WaitFor(t, "AI models closed", func(sc string) bool { return !strings.Contains(sc, "rankers") })
	r.h.p.Post(r.h.openSearch)
	r.s.WaitForText(t, "search: words")
	r.h.p.Post(func() { r.h.searchLive("kestrel") })
	r.s.WaitForText(t, "hits (2) · re-ranked")
	if got := onLoop(r, func() string { return r.h.hitsRanked }); got != " · re-ranked by bge-reranker-v2-m3" {
		t.Errorf("the hits' order: %q", got)
	}
	r.keys(t, esc())
	r.s.WaitFor(t, "search closed", func(sc string) bool { return !strings.Contains(sc, "search: words") })
	rankerTabOpen(t, r)

	r.keys(t, key('d')) // Don't use
	r.s.WaitForText(t, "not re-ranking: Add… a ranker, then Use it")
	r.keys(t, key('r')) // Remove…
	r.s.WaitForText(t, "Remove the ranker tei?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "tei gone", func(string) bool {
		rs, err := d.db.Rankers(ctx)
		return err == nil && len(rs) == 0
	})
}

// buildRanker is a build's own ranker.
type buildRanker struct{}

func (buildRanker) Rank(_ context.Context, _ string, texts []string) ([]float64, error) {
	return make([]float64, len(texts)), nil
}
func (buildRanker) Model() rank.Model { return rank.Model{Provider: "build", Name: "slm-ranker"} }

// TestABuildsRankerIsShownReadOnly: under a build's ranker the tab says so, Use and Don't use do
// nothing, and the stored rankers can still be removed.
func TestABuildsRankerIsShownReadOnly(t *testing.T) {
	d := startManagedRanking(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n\nkestrel\n")}, serving.Options{}, serving.Supplied{Ranker: buildRanker{}})
	ctx := context.Background()
	if _, err := d.db.AddRanker(ctx, store.RankerSpec{Name: "tei", Kind: store.KindTEI, BaseURL: fakeTEI(t).URL}); err != nil {
		t.Fatal(err)
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	rankerTabOpen(t, r)
	r.s.WaitForText(t, "re-ranking with slm-ranker, supplied by the build")
	r.s.WaitForText(t, "tei           TEI")
	r.keys(t, key('u'), key('d'), key('n')) // Use, Don't use, Window…: none answers
	r.keys(t, key('r'))
	r.s.WaitForText(t, "Remove the ranker tei?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "tei gone", func(string) bool {
		rs, err := d.db.Rankers(ctx)
		return err == nil && len(rs) == 0
	})
	if p, _ := d.db.Preferences(ctx); p[store.PrefRanker] != "" {
		t.Errorf("a choice was made under the build's ranker: %q", p[store.PrefRanker])
	}
	if sc := r.s.String(); strings.Contains(sc, "not used") || strings.Contains(sc, "the ranker's window") {
		t.Errorf("a choice was asked of the daemon under the build's ranker:\n%s", sc)
	}
}

// TestTheRankerFormAndWindow: Add… opens the form on a TEI server; Check names the model it ranks
// with; a ranker the store would not keep opens the form again with the reason, and a good one is
// saved. Window… sets the window of the one in use; one outside the bounds opens it again, saying
// why.
func TestTheRankerFormAndWindow(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n\nkestrel\n")})
	ctx := context.Background()
	tei := fakeTEI(t)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	rankerTabOpen(t, r)
	r.keys(t, key('a')) // Add…
	r.s.WaitForText(t, "add a ranker")
	// the TEI playbook's address, and no key: a local TEI has none
	r.s.WaitForText(t, "http://127.0.0.1:18080")
	r.h.p.Post(func() { r.h.checkRanker(tei.URL, "") })
	r.s.WaitForText(t, "it ranks with BAAI/bge-reranker-v2-m3")
	// a TEI that answers but serves an embedder says so
	r.h.p.Post(func() { r.h.checkRanker(fakeTEIOf(t, "embedding").URL, "") })
	r.s.WaitForText(t, "not a ranker: the server's model is not a re-ranker")
	r.h.p.Post(func() { r.h.closeDialog("rankerEdit"); r.h.saveRanker("local", "ftp://nowhere", "", "") })
	r.s.WaitForText(t, "not saved: a ranker needs a name")
	// as the form's Save calls it
	save := r.h.commands()["App.saveRanker"]
	str := func(v string) qml.SpecValue { return qml.SpecValue{Kind: qml.SpecValueString, Raw: v} }
	r.h.p.Post(func() {
		r.h.closeDialog("rankerEdit")
		if err := save([]qml.SpecValue{str("local"), str(tei.URL), str(""), str("")}); err != nil {
			t.Error(err)
		}
	})
	r.s.WaitFor(t, "local stored", func(string) bool {
		rs, err := d.db.Rankers(ctx)
		return err == nil && len(rs) == 1 && rs[0].Name == "local" && rs[0].BaseURL == tei.URL
	})
	r.s.WaitForText(t, "local         TEI")
	r.keys(t, key('u'))
	r.s.WaitForText(t, "re-ranking with local over the top 40 candidates")
	r.keys(t, key('n')) // Window…
	r.s.WaitForText(t, "the ranker's window")
	r.h.p.Post(func() { r.h.closeDialog("rankerWindow"); r.h.saveRankerWindow("5") })
	r.s.WaitForText(t, `not saved: the window is 10 to 100 candidates, not "5"`)
	r.h.p.Post(func() { r.h.closeDialog("rankerWindow"); r.h.saveRankerWindow("20") })
	r.s.WaitForText(t, "re-ranking with local over the top 20 candidates")
	if p, _ := d.db.Preferences(ctx); p[store.PrefRankerWindow] != "20" {
		t.Errorf("the stored window: %q", p[store.PrefRankerWindow])
	}
}

// TestAPeersRankerChangesAreAnnounced: another client adding a ranker and using it is announced,
// and the Ranker Models tab open here lists it again, in use.
func TestAPeersRankerChangesAreAnnounced(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n\nkestrel\n")})
	one := runTUI(t, NewSession(d.sock, nil), Options{})
	one.ready(t)
	rankerTabOpen(t, one)
	one.s.WaitForText(t, "not re-ranking")
	peer := NewSession(d.sock, nil)
	ctx := context.Background()
	if err := peer.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.Close)
	if _, err := peer.Call(ctx, "ranker.add", map[string]any{"name": "tei", "kind": "tei", "base_url": fakeTEI(t).URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Call(ctx, "ranker.use", "tei"); err != nil {
		t.Fatal(err)
	}
	one.waitNoticed(t, "the rankers were changed by another client")
	one.waitNoticed(t, "the ranker in use was changed by another client")
	one.s.WaitForText(t, "re-ranking with tei over the top 40 candidates")
}
