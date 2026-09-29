package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/embed"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// ollama is a fake Ollama server with the models named.
type ollama struct{ *httptest.Server }

func newOllama(t *testing.T, models ...string) *ollama {
	o := &ollama{}
	o.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			var list []map[string]string
			for _, m := range models {
				list = append(list, map[string]string{"name": m, "model": m, "digest": "sha256:" + m})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"models": list})
		case "/api/embed":
			var req struct{ Input []string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			vecs := make([][]float32, len(req.Input))
			for i := range vecs {
				vecs[i] = []float32{1, 0, 0}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(o.Close)
	return o
}

// embedding is a daemon's Embedding over a fresh store holding providers a and b (model
// "embedder" at o) and none in use; the workspaces serve nothing, so a swap is only the provider.
func embedding(t *testing.T, o *ollama) (*Embedding, *Workspaces, *store.Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(ctx, db, Options{Poll: 20 * time.Millisecond, BatchDelay: 5 * time.Millisecond})
	e := NewEmbedding(db, m, nil)
	e.Start(ctx)
	t.Cleanup(func() {
		cancel()
		e.Wait()
		m.StopAll()
		_ = db.Close()
	})
	for _, name := range []string{"a", "b"} {
		if _, err := db.AddProvider(ctx, store.ProviderSpec{Name: name, Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder"}); err != nil {
			t.Fatal(err)
		}
	}
	return e, m, db
}

// live is the provider the workspaces were given, nil for none.
func live(m *Workspaces) embed.Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opts.Provider
}

func preference(t *testing.T, db *store.Store) string {
	t.Helper()
	prefs, err := db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return prefs[store.PrefProvider]
}

// unwritable is the store refusing to write a preference: the provider sets up, and the choice
// cannot be remembered.
type unwritable struct{ *store.Store }

func (unwritable) SetPreference(context.Context, string, string) error {
	return errors.New("store: disk full")
}

// TestAUseNotRememberedChangesNothing: a provider that sets up but whose choice cannot be written
// is not given to the workspaces: the one in use stays in use, as the store still says. Turning
// semantic search off is the same.
func TestAUseNotRememberedChangesNothing(t *testing.T) {
	o := newOllama(t, "embedder")
	e, m, db := embedding(t, o)
	ctx := context.Background()
	if err := e.Use(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	was := live(m)
	e.db = unwritable{db}
	for _, name := range []string{"b", ""} {
		if err := e.Use(ctx, name); err == nil {
			t.Fatalf("Use of %q succeeded with its preference unwritten", name)
		}
		if got := e.current(); got != "a" {
			t.Errorf("in use after the failed Use of %q: %q, want a", name, got)
		}
		if live(m) != was {
			t.Errorf("the workspaces were given %q, whose choice was not written", name)
		}
	}
	if got := preference(t, db); got != "a" {
		t.Errorf("the store names %q, want a", got)
	}
}

// TestAnEditThatDoesNotSetUpChangesNothing: the provider in use, edited to a model its server
// lacks, is refused before anything is written: the store keeps the old model, the workspaces the
// old provider.
func TestAnEditThatDoesNotSetUpChangesNothing(t *testing.T) {
	o := newOllama(t, "embedder")
	e, m, db := embedding(t, o)
	ctx := context.Background()
	if err := e.Use(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	was := live(m)
	if err := e.UpdateProvider(ctx, "a", store.ProviderSpec{Name: "a", Kind: store.KindOllama, BaseURL: o.URL, Model: "missing"}); err == nil {
		t.Fatal("an edit to a model the server lacks was taken")
	}
	info, _, err := db.ProviderWithKey(ctx, "a")
	if err != nil || info.Model != "embedder" {
		t.Errorf("stored after the refused edit: %+v, %v", info, err)
	}
	if live(m) != was || e.current() != "a" {
		t.Error("the refused edit changed the provider in use")
	}
	// an edit that sets up: written, and taken, the preference renamed with it
	if err := e.UpdateProvider(ctx, "a", store.ProviderSpec{Name: "a2", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	if e.current() != "a2" || live(m) == was || preference(t, db) != "a2" {
		t.Errorf("after the rename: in use %q, preference %q", e.current(), preference(t, db))
	}
}

// TestARemoveThatFailsChangesNothing: removing the provider in use takes it out of use only once
// it is gone; a remove that fails leaves it in use, and named.
func TestARemoveThatFailsChangesNothing(t *testing.T) {
	o := newOllama(t, "embedder")
	e, m, db := embedding(t, o)
	ctx := context.Background()
	if err := e.Use(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if err := e.RemoveProvider(gone, "a"); err == nil {
		t.Fatal("a remove with its context ended succeeded")
	}
	if e.current() != "a" || live(m) == nil || preference(t, db) != "a" {
		t.Errorf("after the failed remove: in use %q, preference %q", e.current(), preference(t, db))
	}
	if err := e.RemoveProvider(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if e.current() != "" || live(m) != nil || preference(t, db) != "" {
		t.Errorf("after the remove: in use %q, preference %q", e.current(), preference(t, db))
	}
}

// flaky is the store failing its next fails usage writes.
type flaky struct {
	*store.Store
	fails *int
}

func (f flaky) RecordCalls(ctx context.Context, id int64, calls []store.CallRecord) error {
	if *f.fails > 0 {
		*f.fails--
		return errors.New("store: database is locked")
	}
	return f.Store.RecordCalls(ctx, id, calls)
}

// requests is the calls the store counted for provider a today.
func requests(t *testing.T, db *store.Store) int64 {
	t.Helper()
	us, err := db.ProviderUsage(context.Background(), "a", 1)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, u := range us {
		n += u.Requests
	}
	return n
}

// TestEveryCallIsMetered: a burst of calls far past what a queue would hold is counted whole;
// calls whose write fails are kept and written with the next, none lost and none twice.
func TestEveryCallIsMetered(t *testing.T) {
	o := newOllama(t, "embedder")
	e, _, db := embedding(t, o)
	ctx := context.Background()
	info, _, err := db.ProviderWithKey(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	for range 1500 { // past the 1024 a queue held
		e.hear(info.ID, embed.Call{Texts: 1, Tokens: 2})
	}
	fails := 2
	e.db = flaky{db, &fails}
	e.writePending(ctx) // fails: kept
	e.hear(info.ID, embed.Call{Texts: 1})
	e.writePending(ctx) // fails again: kept, with the one heard since
	if n := requests(t, db); n != 0 {
		t.Fatalf("%d calls written by writes that failed", n)
	}
	e.writePending(ctx)
	if n := requests(t, db); n != 1501 {
		t.Fatalf("the store counted %d calls, want 1501", n)
	}
	e.writePending(ctx) // nothing left: nothing twice
	if n := requests(t, db); n != 1501 {
		t.Fatalf("after a second write the store counted %d calls, want 1501", n)
	}
}

// TestCallsHeldAreBounded: a store not written for long holds maxPending calls a provider, and
// counts the rest as not kept, rather than growing without end.
func TestCallsHeldAreBounded(t *testing.T) {
	o := newOllama(t, "embedder")
	e, _, db := embedding(t, o)
	info, _, err := db.ProviderWithKey(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	for range maxPending + 7 {
		e.hear(info.ID, embed.Call{Texts: 1})
	}
	e.pmu.Lock()
	held, unkept := len(e.pending[info.ID]), e.unkept
	e.pending, e.unkept = map[int64][]store.CallRecord{}, 0 // not for the cleanup's last write
	e.pmu.Unlock()
	if held != maxPending || unkept != 7 {
		t.Fatalf("held %d, not kept %d; want %d and 7", held, unkept, maxPending)
	}
}
