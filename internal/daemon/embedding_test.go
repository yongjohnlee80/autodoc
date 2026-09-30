package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/embed"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// ollama is a fake Ollama server with the models named, recording each embed request's num_ctx.
type ollama struct {
	*httptest.Server
	mu       sync.Mutex
	numCtx   []int
	unloaded []string // the models asked to be let go (keep_alive 0, no input)
}

func (o *ollama) unloads() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.unloaded...)
}

func (o *ollama) contexts() []int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]int(nil), o.numCtx...)
}

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
			var req struct {
				Model     string
				Input     []string
				KeepAlive *int `json:"keep_alive"`
				Options   struct {
					NumCtx int `json:"num_ctx"`
				}
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			o.mu.Lock()
			if req.KeepAlive != nil && *req.KeepAlive == 0 && len(req.Input) == 0 {
				o.unloaded = append(o.unloaded, req.Model)
			} else {
				o.numCtx = append(o.numCtx, req.Options.NumCtx)
			}
			o.mu.Unlock()
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

// TestTheProviderSendsItsContextWindow: a provider set up asks its server for the context window
// stored with it (the default for one given none), and an edit to the window in use sets up at the
// new size, so the server loads the model small enough to share the GPU during a switch.
func TestTheProviderSendsItsContextWindow(t *testing.T) {
	o := newOllama(t, "embedder")
	e, _, _ := embedding(t, o)
	ctx := context.Background()
	if err := e.Use(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got := o.contexts(); len(got) == 0 || got[len(got)-1] != store.DefaultContext {
		t.Fatalf("num_ctx sent %v, want the default %d", got, store.DefaultContext)
	}
	n := len(o.contexts())
	if err := e.UpdateProvider(ctx, "a", store.ProviderSpec{Name: "a", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder", Context: 2048}); err != nil {
		t.Fatal(err)
	}
	got := o.contexts()[n:]
	if len(got) == 0 {
		t.Fatal("the edit set nothing up")
	}
	for _, c := range got {
		if c != 2048 {
			t.Errorf("num_ctx after the edit %v, want 2048 on each", got)
			break
		}
	}
}

// TestTheModelReplacedGoesOffline: the provider in use, edited to another model, has its server
// told to let the old model go, so the two are never loaded together for the switch; an edit to
// the same model (its context window, say) unloads nothing; turning semantic search off unloads
// the model in use.
func TestTheModelReplacedGoesOffline(t *testing.T) {
	o := newOllama(t, "embedder", "bigger")
	e, _, _ := embedding(t, o)
	ctx := context.Background()
	if err := e.Use(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got := o.unloads(); len(got) != 0 {
		t.Fatalf("unloaded %q with nothing replaced", got)
	}
	sp := store.ProviderSpec{Name: "a", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder", Context: 4096}
	if err := e.UpdateProvider(ctx, "a", sp); err != nil {
		t.Fatal(err)
	}
	e.unloads.Wait()
	if got := o.unloads(); len(got) != 0 {
		t.Fatalf("an edit keeping the model unloaded %q: the model in use", got)
	}
	sp.Model = "bigger"
	if err := e.UpdateProvider(ctx, "a", sp); err != nil {
		t.Fatal(err)
	}
	e.unloads.Wait()
	if got := o.unloads(); !reflect.DeepEqual(got, []string{"embedder"}) {
		t.Fatalf("after the edit to another model: unloaded %q, want the old model", got)
	}
	if err := e.Use(ctx, ""); err != nil {
		t.Fatal(err)
	}
	e.unloads.Wait()
	if got := o.unloads(); !reflect.DeepEqual(got, []string{"embedder", "bigger"}) {
		t.Errorf("after Words only: unloaded %q, want the model in use too", got)
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

// faulty is the store failing, while a switch is on, its preference writes or its removes.
type faulty struct {
	*store.Store
	prefs, removes *atomic.Bool
}

func (f faulty) SetPreference(ctx context.Context, name, value string) error {
	if f.prefs.Load() {
		return errors.New("store: disk full")
	}
	return f.Store.SetPreference(ctx, name, value)
}

func (f faulty) RemoveProvider(ctx context.Context, name string) error {
	if f.removes.Load() {
		return errors.New("store: disk full")
	}
	return f.Store.RemoveProvider(ctx, name)
}

// TestTheProviderVerbsChangeNothingWhenTheyFail: over the API, as a client asks, a use whose
// choice is not written, an edit that does not set up, and a remove that fails each answer an
// error, and embedding.providers still names the provider in use, its model as it was.
func TestTheProviderVerbsChangeNothingWhenTheyFail(t *testing.T) {
	o := newOllama(t, "embedder")
	e, m, db := embedding(t, o)
	var prefs, removes atomic.Bool
	e.db = faulty{db, &prefs, &removes} // before the server runs: its handlers read it
	ctx, cancel := context.WithCancel(context.Background())
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := rpc.New(m, "v-test", rpc.WithListener(ln), rpc.WithPreferences(db), rpc.WithEmbeddings(e))
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	cli, err := golibrpc.Dial(ctx, sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err := cli.Call(ctx, "sys.hello", map[string]any{"protocol": rpc.Protocol, "name": "test"}); err != nil {
		t.Fatal(err)
	}
	inUse := func() (string, string) {
		t.Helper()
		res, err := cli.Call(ctx, "embedding.providers")
		if err != nil {
			t.Fatal(err)
		}
		m, _ := res.(map[string]any)
		active, _ := m["active"].(string)
		model := ""
		for _, p := range m["providers"].([]any) {
			if p := p.(map[string]any); p["name"] == active {
				model, _ = p["model"].(string)
			}
		}
		return active, model
	}
	if _, err := cli.Call(ctx, "embedding.use", "a"); err != nil {
		t.Fatal(err)
	}
	prefs.Store(true)
	if _, err := cli.Call(ctx, "embedding.use", "b"); err == nil {
		t.Error("embedding.use b answered ok with its choice unwritten")
	}
	prefs.Store(false)
	if _, err := cli.Call(ctx, "embedding.update", "a", map[string]any{"name": "a", "kind": "ollama", "base_url": o.URL, "model": "missing"}); err == nil {
		t.Error("embedding.update to a model the server lacks answered ok")
	}
	removes.Store(true)
	if _, err := cli.Call(ctx, "embedding.remove", "a"); err == nil {
		t.Error("embedding.remove answered ok with the delete failed")
	}
	if active, model := inUse(); active != "a" || model != "embedder" {
		t.Fatalf("after three failed changes: in use %q with model %q, want a with embedder", active, model)
	}
	if got := preference(t, db); got != "a" {
		t.Fatalf("the store names %q, want a", got)
	}
}
