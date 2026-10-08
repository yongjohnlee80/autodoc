package tui

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestTheStageBoxes: a box shows only while its stage can run, and one that does not show is off;
// every box left checked is auto; Lexical stands in when Semantic is not checked, so a search
// always has a retriever, and the last one checked cannot be unchecked; a box that comes back comes
// back as it was left.
func TestTheStageBoxes(t *testing.T) {
	all := allStagesChecked()
	for _, c := range []struct {
		name               string
		choice             stageChoice
		semantic, rerank   bool
		want               []any
		semShown, rrkShown bool
	}{
		{"every box checked, nothing but words: auto", all, false, false, nil, false, false},
		{"every box checked, a model: auto", all, true, false, nil, true, false},
		{"every box checked, a ranker: auto", all, false, true, nil, false, true},
		{"every box checked, everything: auto", all, true, true, nil, true, true},
		{"unranked, nothing but words", stageChoice{true, true, false}, false, false, []any{"lexical"}, false, false},
		{"words alone, everything", stageChoice{true, false, true}, true, true, []any{"lexical", "rerank"}, true, true},
		{"meaning only, the model gone: words stand in", stageChoice{false, true, true}, false, true, []any{"lexical", "rerank"}, false, true},
		{"meaning only, the model back", stageChoice{false, true, true}, true, true, []any{"semantic", "rerank"}, true, true},
		{"unranked, the ranker gone and back", stageChoice{true, true, false}, true, true, []any{"lexical", "semantic"}, true, true},
	} {
		b := boxesOf(c.choice, c.semantic, c.rerank)
		if got := b.stages(); !reflect.DeepEqual(got, c.want) || b.semanticShown != c.semShown || b.rerankShown != c.rrkShown {
			t.Errorf("%s: %v, shown semantic %v rerank %v; want %v", c.name, got, b.semanticShown, b.rerankShown, c.want)
		}
	}

	words := boxesOf(all, false, true)
	if _, ok := words.toggle(all, stageLexical); ok {
		t.Error("Lexical unchecked while Semantic does not show")
	}
	if _, ok := words.toggle(all, stageSemantic); ok {
		t.Error("Semantic toggled while it does not show")
	}
	if c, ok := words.toggle(all, stageRerank); !ok || c.rerank {
		t.Errorf("Rerank unchecked: %+v, %v", c, ok)
	}
	if _, ok := boxesOf(all, true, false).toggle(all, stageRerank); ok {
		t.Error("Rerank toggled while it does not show")
	}
	meaning := stageChoice{false, true, true}
	if _, ok := boxesOf(meaning, true, true).toggle(meaning, stageSemantic); ok {
		t.Error("Semantic unchecked while Lexical is not")
	}
	if c, ok := boxesOf(all, true, true).toggle(all, stageLexical); !ok || c != meaning {
		t.Errorf("Lexical unchecked beside Semantic: %+v, %v", c, ok)
	}
	if c, ok := boxesOf(meaning, true, true).toggle(meaning, stageLexical); !ok || c != all {
		t.Errorf("Lexical checked again: %+v, %v", c, ok)
	}

	for _, c := range []stageChoice{all, meaning, {true, false, false}, {true, true, false}} {
		if got := stageChoiceOf(c.String()); got != c {
			t.Errorf("%+v kept as %q reads back %+v", c, c.String(), got)
		}
	}
	if got := prefsOf(map[string]any{}).searchStages; got != all {
		t.Errorf("no preference: %+v, want every box checked", got)
	}
}

// TestTheRerankBoxNamesTheRanker: the ranker in use, a build's own included, by its model; a stored
// one whose model the list does not know (a TEI server's) by its name; none, no box.
func TestTheRerankBoxNamesTheRanker(t *testing.T) {
	for _, c := range []struct {
		list      map[string]any
		id, model string
	}{
		{map[string]any{}, "", ""},
		{map[string]any{"supplied": "acme/slm-ranker"}, "acme/slm-ranker", "slm-ranker"},
		{map[string]any{"active": "tei", "rankers": []any{map[string]any{"name": "tei", "kind": "tei"}}}, "tei", ""},
		{map[string]any{"active": "cohere", "rankers": []any{map[string]any{"name": "tei", "kind": "tei"},
			map[string]any{"name": "cohere", "kind": "rerank-api", "model": "rerank-v3.5"}}}, "cohere", "rerank-v3.5"},
		{map[string]any{"error": "the server did not answer"}, "", ""},
	} {
		if id, model := rankerInUse(c.list); id != c.id || model != c.model {
			t.Errorf("%v: %q %q, want %q %q", c.list, id, model, c.id, c.model)
		}
	}
}

// stagesRecorder records the stages of each search.query the TUI sends.
type stagesRecorder struct {
	mu   sync.Mutex
	sent [][]any
}

func (s *stagesRecorder) record(method string, params []any) {
	if method != "search.query" || len(params) < 3 {
		return
	}
	stages, _ := asMap(params[2])["stages"].([]any)
	s.mu.Lock()
	s.sent = append(s.sent, stages)
	s.mu.Unlock()
}

func (s *stagesRecorder) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func (s *stagesRecorder) last() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return nil
	}
	return s.sent[len(s.sent)-1]
}

// waitSent waits for a search asking for want, after the first n.
func (s *stagesRecorder) waitSent(t *testing.T, r *running, n int, want ...any) {
	t.Helper()
	r.s.WaitFor(t, "a search asking for "+strings.Join(append(strs(want), "(auto when none)"), "+"), func(string) bool {
		return s.count() > n && reflect.DeepEqual(s.last(), want)
	})
}

// TestTheSearchStagesBoxes: the boxes end to end: Lexical alone with no model and no ranker, and it
// cannot be unchecked; Rerank once a ranker is in use, named before a search by the ranker and
// after it by the model that ranked; each change searching again with the stages it asks for, and
// kept as a preference; Semantic once a model is in use; and a new TUI starting as the last left.
func TestTheSearchStagesBoxes(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n\nkestrel\n", "b.md", "# B\n\nkestrel in a longer note\n")})
	ctx := context.Background()
	rec := &stagesRecorder{}
	sess := NewSession(d.sock, nil)
	sess.beforeCall = rec.record
	r := runTUI(t, sess, Options{})
	r.ready(t)
	// the first progress poll's answer searches again (showProgress), so the counts below start after
	// it: a slow machine's late answer was a search the cells did not ask for
	deadline := time.Now().Add(10 * time.Second)
	for !onLoop(r, func() bool { return r.h.prog.polled }) {
		if time.Now().After(deadline) {
			t.Fatal("the first progress poll never answered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.h.p.Post(r.h.openSearch)
	r.s.WaitForText(t, "search: words")
	n := rec.count()
	r.h.p.Post(func() { r.h.searchLive("kestrel") })
	rec.waitSent(t, r, n) // every box checked: auto
	r.s.WaitForText(t, "[x] Lexical")
	if sc := r.s.String(); strings.Contains(sc, "Semantic") || strings.Contains(sc, "Rerank") {
		t.Errorf("no model and no ranker, yet a box for one:\n%s", sc)
	}

	n = rec.count()
	r.h.p.Post(func() { r.h.toggleSearchStage(stageLexical) })
	r.s.WaitForText(t, "search needs Lexical or Semantic: one stays checked")
	r.s.WaitForText(t, "[x] Lexical")
	if p, _ := d.db.Preferences(ctx); p[prefSearchStages] != "" || rec.count() != n {
		t.Errorf("the last retriever unchecked: kept %q, %d searches sent", p[prefSearchStages], rec.count()-n)
	}
	// clicked, as a user does: the CheckBox flips itself, the refusal flips it back
	r.h.p.Post(func() { r.h.say("") })
	r.s.WaitFor(t, "the status cleared", func(sc string) bool { return !strings.Contains(sc, "one stays checked") })
	x, y := screenCell(t, r, "[x] Lexical")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x + 1, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x + 1, Y: y})
	r.s.WaitForText(t, "search needs Lexical or Semantic: one stays checked") // the click was refused
	r.s.WaitFor(t, "the clicked box back to checked", func(sc string) bool {
		return strings.Contains(sc, "[x] Lexical") && !strings.Contains(sc, "[ ] Lexical")
	})
	if strings.Contains(r.s.String(), "[ [x]") {
		t.Errorf("a box is drawn in a button's brackets:\n%s", r.s)
	}

	if _, err := d.db.AddRanker(ctx, store.RankerSpec{Name: "tei", Kind: store.KindTEI, BaseURL: fakeTEI(t).URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.h.session.Call(ctx, "ranker.use", "tei"); err != nil {
		t.Fatal(err)
	}
	r.keys(t, esc())
	r.s.WaitFor(t, "search closed", func(sc string) bool { return !strings.Contains(sc, "search: words") })
	r.h.p.Post(func() { r.h.searchQuery = "" }) // the box named before any search answers
	r.h.p.Post(r.h.openSearch)
	r.s.WaitForText(t, "[x] Rerank (tei)")
	n = rec.count()
	r.h.p.Post(func() { r.h.searchLive("kestrel") })
	rec.waitSent(t, r, n)
	r.s.WaitFor(t, "the hits re-ranked", func(string) bool {
		return onLoop(r, func() string { return r.h.hitsRanked }) == " · re-ranked by bge-reranker-v2-m3"
	})
	r.s.WaitForText(t, "[x] Rerank (bge-reranker-v2-m3)")
	if got := strings.Count(r.s.String(), "re-ranked"); got != 1 {
		t.Errorf("\"re-ranked\" shows %d times, want once:\n%s", got, r.s.String())
	}

	n = rec.count()
	r.h.p.Post(func() { r.h.toggleSearchStage(stageRerank) })
	rec.waitSent(t, r, n, "lexical")
	r.s.WaitForText(t, "[ ] Rerank (bge-reranker-v2-m3)")
	r.s.WaitFor(t, "the hits in recall order", func(sc string) bool {
		return strings.Contains(sc, "hits (2)") && !strings.Contains(sc, "re-ranked by")
	})
	r.s.WaitFor(t, "the choice kept", func(string) bool {
		p, _ := d.db.Preferences(ctx)
		return p[prefSearchStages] == "lexical,semantic"
	})

	o := newFakeOllama(t, "embedder")
	if _, err := d.db.AddProvider(ctx, store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.h.session.Call(ctx, "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "[x] Semantic")
	n = rec.count()
	r.h.p.Post(func() { r.h.searchLive("kestrel") })
	rec.waitSent(t, r, n, "lexical", "semantic")
	n = rec.count()
	r.h.p.Post(func() { r.h.toggleSearchStage(stageLexical) })
	rec.waitSent(t, r, n, "semantic")
	r.s.WaitForText(t, "[ ] Lexical")
	n = rec.count()
	r.h.p.Post(func() { r.h.toggleSearchStage(stageSemantic) })
	r.s.WaitForText(t, "search needs Lexical or Semantic: one stays checked")
	if rec.count() != n {
		t.Errorf("unchecking the last retriever searched again: %v", rec.last())
	}
	r.s.WaitFor(t, "the choice kept", func(string) bool {
		p, _ := d.db.Preferences(ctx)
		return p[prefSearchStages] == "semantic"
	})

	again := runTUI(t, NewSession(d.sock, nil), Options{})
	again.ready(t)
	again.h.p.Post(again.h.openSearch)
	again.s.WaitForText(t, "[ ] Lexical")
	again.s.WaitForText(t, "[x] Semantic")
	again.s.WaitForText(t, "[ ] Rerank (tei)")
}

// screenCell is the column and row text starts at on the screen.
func screenCell(t *testing.T, r *running, text string) (int, int) {
	t.Helper()
	for y, row := range r.s.Backend.Snapshot() {
		var line strings.Builder
		cols := []int{}
		for x, c := range row {
			line.WriteString(c.Content)
			for range len(c.Content) {
				cols = append(cols, x)
			}
		}
		if i := strings.Index(line.String(), text); i >= 0 {
			return cols[i], y
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", text, r.s)
	return 0, 0
}

// The GUI keeps its own theme (gui.theme, sepia until chosen) and the terminal its own
// (tui.theme, dark): choosing one in a window leaves the terminal's as it was.
func TestTheGUIKeepsItsOwnTheme(t *testing.T) {
	if got := prefsFor(map[string]any{"tui.theme": "retro"}, true).theme; got != "sepia" {
		t.Errorf("the GUI's theme with only the terminal's set: %q, want sepia", got)
	}
	if got := prefsFor(map[string]any{"tui.theme": "retro", "gui.theme": "light"}, true).theme; got != "light" {
		t.Errorf("the GUI's theme: %q, want its own, light", got)
	}
	if got := prefsFor(map[string]any{"gui.theme": "light"}, false).theme; got != "dark" {
		t.Errorf("the terminal's theme with only the GUI's set: %q, want dark", got)
	}

	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n"}})
	g := runTUI(t, NewSession(d.sock, nil), Options{GUI: true})
	g.s.WaitForText(t, "connected — autodoc v-test")
	if th := onLoop(g, func() string { return g.h.theme }); th != "sepia" {
		t.Fatalf("a window starts in %q, want sepia", th)
	}
	onLoop(g, func() bool { g.h.useTheme("mono"); return true })
	g.s.WaitFor(t, "the GUI's theme kept", func(string) bool {
		return onLoop(g, func() string { return g.h.theme }) == "mono"
	})

	// the store has the window's choice: the next window starts in it
	g2 := runTUI(t, NewSession(d.sock, nil), Options{GUI: true})
	g2.s.WaitForText(t, "connected — autodoc v-test")
	g2.s.WaitFor(t, "the next window in the kept mono", func(string) bool {
		return onLoop(g2, func() string { return g2.h.theme }) == "mono"
	})
	// and the terminal, which reads the same store, keeps its own: still dark once it has read it
	tr := runTUI(t, NewSession(d.sock, nil), Options{})
	tr.s.WaitForText(t, "connected — autodoc v-test")
	time.Sleep(300 * time.Millisecond) // a wrong read would switch it by now; g2 read in less
	if th := onLoop(tr, func() string { return tr.h.theme }); th != "dark" {
		t.Fatalf("the terminal after the window chose mono: %q, want its own dark", th)
	}
}
