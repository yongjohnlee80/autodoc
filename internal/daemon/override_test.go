package daemon

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search/embed"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// namedProvider is a fake provider whose model is its name, so a workspace's status says which one
// it embeds with.
type namedProvider struct{ name string }

func (p namedProvider) Name() string { return p.name }
func (p namedProvider) Model() embed.Model {
	return embed.Model{Provider: "fake", Name: p.name, Dims: 3}
}
func (p namedProvider) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, float32(len(texts[i])), 2}
	}
	return out, nil
}

// modelOf waits for the workspace's embedding model to be model, and fully embedded.
func modelOf(t *testing.T, m *Workspaces, name, model string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		w, ok := m.Get(name)
		if ok && w.Index != nil {
			if st, err := w.Index.Status(context.Background()); err == nil && st.Embeddings != nil &&
				embed.ModelName(st.Embeddings.Model) == model && st.Embeddings.Target == "" && st.Embeddings.Pending == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			w, _ := m.Get(name)
			st, _ := w.Index.Status(context.Background())
			t.Fatalf("%s never embedded with %s: %+v", name, model, st.Embeddings)
		}
	}
}

// A workspace's own provider: set up first (a failure changes nothing), only that workspace
// restarts, a switch of the daemon's provider leaves it alone, "" returns it to the daemon's, and a
// deleted provider does too.
func TestAWorkspacesOwnProvider(t *testing.T) {
	m, db := open(t)
	built := map[string]int{}
	m.SetProviderBuilder(func(_ context.Context, name string) (embed.Provider, error) {
		if name == "broken" {
			return nil, embed.ErrUnreachable
		}
		built[name]++
		return namedProvider{name}, nil
	})
	for _, p := range []string{"own", "broken"} {
		if _, err := db.AddProvider(context.Background(), store.ProviderSpec{Name: p, Kind: store.KindOllama, BaseURL: "http://127.0.0.1:1", Model: p}); err != nil {
			t.Fatal(err)
		}
	}
	m.SetEmbedding(namedProvider{"daemon"})
	addQueueWorkspace(t, m, "one", 3)
	addQueueWorkspace(t, m, "two", 3)
	modelOf(t, m, "one", "daemon")
	modelOf(t, m, "two", "daemon")

	if err := m.SetProvider(context.Background(), "two", "broken"); !errors.Is(err, embed.ErrUnreachable) {
		t.Fatalf("a provider that does not set up: %v", err)
	}
	if p, _ := db.WorkspaceProvider(context.Background(), mustID(t, m, "two")); p != "" {
		t.Fatalf("a refused provider was stored: %q", p)
	}

	oneBefore, _ := m.Get("one")
	if err := m.SetProvider(context.Background(), "two", "own"); err != nil {
		t.Fatal(err)
	}
	modelOf(t, m, "two", "own")
	if oneAfter, _ := m.Get("one"); oneAfter != oneBefore {
		t.Fatal("setting two's provider restarted one")
	}
	if w, _ := m.Get("two"); w.Provider.Override != "own" || w.Provider.Err != "" {
		t.Fatalf("two's provider = %+v", w.Provider)
	}

	twoBefore, _ := m.Get("two")
	m.SetEmbedding(namedProvider{"daemon2"}) // the daemon's switch
	modelOf(t, m, "one", "daemon2")
	if twoAfter, _ := m.Get("two"); twoAfter != twoBefore {
		t.Fatal("the daemon's switch restarted two, which has its own provider")
	}
	modelOf(t, m, "two", "own")

	if err := m.SetProvider(context.Background(), "two", ""); err != nil {
		t.Fatal(err)
	}
	modelOf(t, m, "two", "daemon2")

	// set again, then the provider is deleted: two returns to the daemon's
	if err := m.SetProvider(context.Background(), "two", "own"); err != nil {
		t.Fatal(err)
	}
	modelOf(t, m, "two", "own")
	if err := db.RemoveProvider(context.Background(), "own"); err != nil {
		t.Fatal(err)
	}
	m.providerChanged("own")
	modelOf(t, m, "two", "daemon2")
	if w, _ := m.Get("two"); w.Provider.Override != "" {
		t.Fatalf("after the delete two's provider = %+v", w.Provider)
	}
	if built["own"] != 2 {
		t.Errorf("own was set up %d times, want 2 (once per time it was chosen; shared otherwise)", built["own"])
	}
}

// A workspace whose own provider cannot be set up when it starts searches by words and says why;
// it is not given the daemon's model.
func TestAnOwnProviderThatFailsAtStartIsNotReplaced(t *testing.T) {
	m, db := open(t)
	fail := false
	m.SetProviderBuilder(func(_ context.Context, name string) (embed.Provider, error) {
		if fail {
			return nil, embed.ErrUnreachable
		}
		return namedProvider{name}, nil
	})
	if _, err := db.AddProvider(context.Background(), store.ProviderSpec{Name: "own", Kind: store.KindOllama, BaseURL: "http://127.0.0.1:1", Model: "own"}); err != nil {
		t.Fatal(err)
	}
	m.SetEmbedding(namedProvider{"daemon"})
	addQueueWorkspace(t, m, "two", 2)
	if err := m.SetProvider(context.Background(), "two", "own"); err != nil {
		t.Fatal(err)
	}
	modelOf(t, m, "two", "own")
	fail = true
	m.mu.Lock()
	delete(m.overrides, "own") // as after a daemon restart: nothing set up yet
	m.mu.Unlock()
	if err := m.SetPatterns(context.Background(), "two", []string{"**/*.md"}, nil); err != nil {
		t.Fatal(err)
	}
	w, _ := m.Get("two")
	if w.Provider.Override != "own" || w.Provider.Err == "" || w.Index.HasEmbeddings() {
		t.Fatalf("after a failed setup: provider %+v, embeddings %v; want own, its error, and words only", w.Provider, w.Index.HasEmbeddings())
	}
}

// Background turns keep to one provider while its workspaces have work, at most focusBurst in a
// row, so a server holding one model is not asked for another every batch, and no workspace waits
// forever.
func TestQueueKeepsToOneProviderForABoundedRun(t *testing.T) {
	q := newEmbeddingQueue(&Workspaces{})
	cands := map[string]*served{
		"a": {override: ""}, "b": {override: "own"}, "c": {override: ""}, "d": {override: "own"},
	}
	var seq []string
	for range 2*focusBurst + 4 {
		name, _ := q.choose(cands)
		seq = append(seq, cands[name].override)
		q.mu.Lock()
		delete(q.running, name)
		q.mu.Unlock()
	}
	switches := 0
	run := 1
	for i := 1; i < len(seq); i++ {
		if seq[i] != seq[i-1] {
			switches++
			if run > focusBurst {
				t.Fatalf("a run of %d turns on one provider, past the bound %d: %v", run, focusBurst, seq)
			}
			run = 1
		} else {
			run++
		}
	}
	if switches == 0 || switches > 3 {
		t.Fatalf("%d provider switches in %d turns, want a few bounded runs: %v", switches, len(seq), seq)
	}
}

// The setting verbs refuse a workspace the daemon does not have; a nil include is an explicit blank
// one; a schema set on a workspace whose root is gone is stored for when it is served; a provider
// that does not exist is refused through the Embedding's builder; and editing a provider that is not
// the daemon's reaches only the workspaces that use it.
func TestSettingVerbsEdgeCases(t *testing.T) {
	m, db := open(t)
	ctx := context.Background()
	if _, err := m.SetSchema(ctx, "nope", "s.yaml"); !errors.Is(err, store.ErrNoWorkspace) {
		t.Errorf("SetSchema on no workspace: %v", err)
	}
	if err := m.SetPatterns(ctx, "nope", []string{"**/*.md"}, nil); !errors.Is(err, store.ErrNoWorkspace) {
		t.Errorf("SetPatterns on no workspace: %v", err)
	}
	if err := m.SetProvider(ctx, "nope", ""); !errors.Is(err, store.ErrNoWorkspace) {
		t.Errorf("SetProvider on no workspace: %v", err)
	}
	if _, err := m.SetTextExtensions(ctx, "nope", nil); !errors.Is(err, store.ErrNoWorkspace) {
		t.Errorf("SetTextExtensions on no workspace: %v", err)
	}
	addQueueWorkspace(t, m, "kb", 1)
	if err := m.SetPatterns(ctx, "kb", nil, nil); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 0) // a nil include is an explicit blank one: nothing indexed
	if ws, _ := db.Workspaces(ctx); ws[0].Include == nil || len(ws[0].Include) != 0 {
		t.Fatalf("include after nil = %v", ws[0].Include)
	}

	// the Embedding's builder: a provider that does not exist is refused, nothing stored
	e := NewEmbedding(db, m, nil)
	if err := m.SetProvider(ctx, "kb", "ghost"); !errors.Is(err, store.ErrNoProvider) {
		t.Errorf("SetProvider to a missing provider: %v", err)
	}
	if p, _ := db.WorkspaceProvider(ctx, mustID(t, m, "kb")); p != "" {
		t.Fatalf("a refused provider was stored: %q", p)
	}
	// editing a provider that is not the daemon's: stored, and the workspaces using it told
	if _, err := db.AddProvider(ctx, store.ProviderSpec{Name: "side", Kind: store.KindOllama, BaseURL: "http://127.0.0.1:1", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := e.UpdateProvider(ctx, "side", store.ProviderSpec{Name: "side2", Kind: store.KindOllama, BaseURL: "http://127.0.0.1:1", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := db.Providers(ctx); len(ps) != 1 || ps[0].Name != "side2" {
		t.Fatalf("providers after the edit = %+v", ps)
	}
}

// A schema set on a workspace whose root is gone is stored, and reported back as its path alone.
func TestSchemaOnAnUnservedWorkspace(t *testing.T) {
	m, db := open(t)
	root := t.TempDir()
	if _, err := db.AddWorkspace(context.Background(), "gone", root, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := m.OpenAll(); err != nil {
		t.Fatal(err)
	}
	st, err := m.SetSchema(context.Background(), "gone", "s.yaml")
	if err != nil || st.Path != "s.yaml" || st.Active {
		t.Fatalf("SetSchema on an unserved workspace = %+v, %v", st, err)
	}
	ws, _ := db.Workspaces(context.Background())
	if ws[0].SchemaPath == nil || *ws[0].SchemaPath != "s.yaml" {
		t.Fatal("not stored")
	}
}
