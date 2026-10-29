package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// keySpy is a server an edit points at: it records every Authorization header it is sent.
type keySpy struct {
	*httptest.Server
	mu   sync.Mutex
	auth []string
}

func newKeySpy(t *testing.T) *keySpy {
	t.Helper()
	s := &keySpy{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// saw reports whether any request carried key.
func (s *keySpy) saw(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.auth {
		if strings.Contains(a, key) {
			return true
		}
	}
	return false
}

// TestAProvidersStoredKeyIsNeverSentToAnEditsNewServer: every path that calls an edit's endpoint
// with a provider's stored key (the models listing, the context-window probe, the update's own
// check, and the rewrite of the provider in use) refuses when the edit moves to another server and
// types no key, and that server is never sent the key. The control: the same spy is sent a key
// that is typed, so it is reachable and the client does send one.
func TestAProvidersStoredKeyIsNeverSentToAnEditsNewServer(t *testing.T) {
	o := newOllama(t, "embedder")
	e, _, db := embedding(t, o)
	ctx := context.Background()
	stored := "sk-stored-secret"
	if _, err := db.AddProvider(ctx, store.ProviderSpec{Name: "k", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder", Key: &stored}); err != nil {
		t.Fatal(err)
	}
	spy := newKeySpy(t)
	moved := store.ProviderSpec{Name: "k", Kind: store.KindOllama, BaseURL: spy.URL, Model: "embedder"}

	if _, err := e.Models(ctx, "k", moved); !errors.Is(err, store.ErrKeyForNewServer) {
		t.Errorf("Models of an edit to another server: %v, want ErrKeyForNewServer", err)
	}
	if _, err := e.ModelContext(ctx, "k", moved); !errors.Is(err, store.ErrKeyForNewServer) {
		t.Errorf("ModelContext of an edit to another server: %v, want ErrKeyForNewServer", err)
	}
	if err := e.UpdateProvider(ctx, "k", moved); !errors.Is(err, store.ErrKeyForNewServer) {
		t.Errorf("UpdateProvider to another server: %v, want ErrKeyForNewServer", err)
	}
	if spy.saw(stored) {
		t.Fatal("the stored key was sent to the edit's new server")
	}
	if err := e.Use(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	inUse := moved
	inUse.Kind = store.KindOpenAI // no context probe: the rewrite of the provider in use is what calls
	if err := e.UpdateProvider(ctx, "k", inUse); !errors.Is(err, store.ErrKeyForNewServer) {
		t.Errorf("UpdateProvider of the provider in use to another server: %v, want ErrKeyForNewServer", err)
	}
	if spy.saw(stored) {
		t.Fatal("the stored key was sent to the edit's new server")
	}
	if info, key, _ := db.ProviderWithKey(ctx, "k"); info.BaseURL != o.URL || key != stored {
		t.Errorf("after the refused edits: %q, key kept %v", info.BaseURL, key == stored)
	}

	typed := "sk-typed"
	moved.Key = &typed
	_, _ = e.Models(ctx, "k", moved)
	if !spy.saw(typed) {
		t.Fatal("control: the spy was not sent the typed key, so its silence above proves nothing")
	}
}

// TestARankersStoredKeyIsNeverSentToAnEditsNewServer: a ranker's models listing and the rewrite
// of the ranker in use refuse an edit to another server with no key typed, and send it nothing.
func TestARankersStoredKeyIsNeverSentToAnEditsNewServer(t *testing.T) {
	srv := newTEI(t, "reranker")
	db := rankStore(t, srv)
	ctx := context.Background()
	stored := "sk-stored-secret"
	if _, err := db.AddRanker(ctx, store.RankerSpec{Name: "k", Kind: store.KindTEI, BaseURL: srv.URL, Key: &stored}); err != nil {
		t.Fatal(err)
	}
	r, _, _ := rankingOver(t, db, Supplied{})
	spy := newKeySpy(t)
	moved := store.RankerSpec{Name: "k", Kind: store.KindTEI, BaseURL: spy.URL}

	if _, err := r.Models(ctx, "k", moved); !errors.Is(err, store.ErrKeyForNewServer) {
		t.Errorf("Models of an edit to another server: %v, want ErrKeyForNewServer", err)
	}
	if err := r.Use(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRanker(ctx, "k", moved); !errors.Is(err, store.ErrKeyForNewServer) {
		t.Errorf("UpdateRanker of the ranker in use to another server: %v, want ErrKeyForNewServer", err)
	}
	if spy.saw(stored) {
		t.Fatal("the stored key was sent to the edit's new server")
	}

	typed := "sk-typed"
	moved.Key = &typed
	_, _ = r.Models(ctx, "k", moved)
	if !spy.saw(typed) {
		t.Fatal("control: the spy was not sent the typed key, so its silence above proves nothing")
	}
}
