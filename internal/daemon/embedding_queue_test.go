package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search/embed"
)

type queueProvider struct {
	mu              sync.Mutex
	active, maximum int
	calls           []string
	entered         chan struct{}
	release         chan struct{}
	delay           time.Duration
}

func (p *queueProvider) Name() string { return "fake" }
func (p *queueProvider) Model() embed.Model {
	return embed.Model{Provider: "fake", Name: "queue", Dims: 3}
}
func (p *queueProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	p.mu.Lock()
	p.active++
	p.maximum = max(p.maximum, p.active)
	p.calls = append(p.calls, texts[0])
	first := len(p.calls) == 1
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.active--; p.mu.Unlock() }()
	if first && p.entered != nil {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = []float32{1, 0, 0}
	}
	return vecs, nil
}

func (p *queueProvider) snapshot() ([]string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...), p.maximum
}

func addQueueWorkspace(t *testing.T, m *Workspaces, name string, sections int) {
	t.Helper()
	root := t.TempDir()
	var body strings.Builder
	for i := range sections {
		fmt.Fprintf(&body, "# %s section %d\n\n%s-%d body text\n\n", name, i, name, i)
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(context.Background(), config.Workspace{Name: name, Root: root}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, name, 1)
}

func queueReady(t *testing.T, m *Workspaces, names ...string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		all := true
		for _, name := range names {
			w, ok := m.Get(name)
			if !ok || w.Index == nil {
				all = false
				break
			}
			st, err := w.Index.Status(context.Background())
			if err != nil || st.Embeddings == nil || st.Embeddings.Semantic != "ready" || st.Embeddings.Pending != 0 {
				all = false
				break
			}
		}
		if all {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue did not fill %v", names)
		}
	}
}

func TestOneProviderCallAtATimeAcrossTenWorkspaces(t *testing.T) {
	m, _ := open(t)
	m.opts.Log = logger.Nop{}
	p := &queueProvider{delay: 2 * time.Millisecond}
	m.SetEmbedding(p)
	var names []string
	for i := range 10 {
		name := fmt.Sprintf("workspace-%02d", i)
		addQueueWorkspace(t, m, name, 3)
		names = append(names, name)
	}
	queueReady(t, m, names...)
	_, maximum := p.snapshot()
	if maximum != 1 {
		t.Errorf("%d concurrent provider calls across 10 workspaces, want 1", maximum)
	}
}

func TestConfiguredTwoRequestLimitIsSharedAcrossWorkspaces(t *testing.T) {
	m, _ := open(t)
	m.opts.Log = logger.Nop{}
	m.opts.MaxEmbedRequests = 2
	p := &queueProvider{delay: 20 * time.Millisecond, entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	}()
	m.SetEmbedding(p)
	var names []string
	for i := range 10 {
		name := fmt.Sprintf("workspace-%02d", i)
		addQueueWorkspace(t, m, name, 70)
		names = append(names, name)
		if i == 0 {
			select {
			case <-p.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("first batch did not start")
			}
		}
		if i == 1 {
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				_, maximum := p.snapshot()
				if maximum == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the configured second provider slot was never used")
				}
			}
			close(p.release)
		}
	}
	queueReady(t, m, names...)
	_, maximum := p.snapshot()
	if maximum != 2 {
		t.Errorf("maximum concurrent calls = %d, want 2", maximum)
	}
}

func TestTwoSlotsNeverEmbedTheSameWorkspaceTwice(t *testing.T) {
	m, _ := open(t)
	m.opts.Log = logger.Nop{}
	m.opts.MaxEmbedRequests = 2
	p := &queueProvider{entered: make(chan struct{}), release: make(chan struct{})}
	m.SetEmbedding(p)
	addQueueWorkspace(t, m, "alpha", 150)
	if err := m.Focus("alpha"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first batch not started")
	}
	time.Sleep(50 * time.Millisecond)
	if calls, maximum := p.snapshot(); len(calls) != 1 || maximum != 1 {
		t.Errorf("one focused workspace made %d calls (%d concurrent) before first completed", len(calls), maximum)
	}
	close(p.release)
	queueReady(t, m, "alpha")
}

func TestProviderLimitIncludesAQueryWaitingBehindABatch(t *testing.T) {
	p := &queueProvider{entered: make(chan struct{}), release: make(chan struct{})}
	shared := withProviderSlots(p, 1)
	first := make(chan error, 1)
	go func() { _, err := shared.Embed(context.Background(), []string{"document batch"}); first <- err }()
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("batch never entered provider")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := shared.Embed(ctx, []string{"query"}); err != context.Canceled {
		t.Fatalf("canceled query waited on the batch: %v", err)
	}
	if calls, maximum := p.snapshot(); len(calls) != 1 || maximum != 1 {
		t.Errorf("query reached busy provider: %d calls, maximum %d", len(calls), maximum)
	}
	close(p.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestFocusSwitchTakesTheNextBatch(t *testing.T) {
	m, _ := open(t)
	m.opts.Log = logger.Nop{}
	p := &queueProvider{entered: make(chan struct{}), release: make(chan struct{})}
	m.SetEmbedding(p)
	addQueueWorkspace(t, m, "alpha", 150)
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first alpha batch not started")
	}
	addQueueWorkspace(t, m, "bravo", 150)
	w, _ := m.Get("bravo")
	if state, behind := w.EmbeddingQueue(); state != "waiting" || behind != "alpha" {
		t.Errorf("bravo while alpha is in flight: %q behind %q", state, behind)
	}
	if err := m.Focus("bravo"); err != nil {
		t.Fatal(err)
	}
	close(p.release)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		calls, maximum := p.snapshot()
		if len(calls) >= 2 {
			if !strings.Contains(calls[0], "alpha") || !strings.Contains(calls[1], "bravo") {
				t.Errorf("first two batches: %q", calls[:2])
			}
			if maximum != 1 {
				t.Errorf("%d concurrent requests", maximum)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second batch not started")
		}
	}
}

func TestSearchPromotesAndContinuousFocusDoesNotStarve(t *testing.T) {
	m, _ := open(t)
	q := m.queue
	candidates := map[string]*served{"alpha": {}, "bravo": {}, "charlie": {}}
	q.searchWorkspace("charlie")
	if name, _ := q.choose(candidates); name != "charlie" {
		t.Fatalf("searched workspace got %q", name)
	}
	q.focusWorkspace("alpha")
	seenBackground := false
	for range 18 {
		name, _ := q.choose(candidates)
		if name != "alpha" {
			seenBackground = true
		}
	}
	if !seenBackground {
		t.Fatal("continuous focus starved the other workspaces")
	}
}

func TestBackgroundAdvancesBeforeFocusedBacklogIsDone(t *testing.T) {
	m, _ := open(t)
	m.opts.Log = logger.Nop{}
	p := &queueProvider{entered: make(chan struct{}), release: make(chan struct{}), delay: time.Millisecond}
	m.SetEmbedding(p)
	addQueueWorkspace(t, m, "alpha", 1200)
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("focused backlog not started")
	}
	addQueueWorkspace(t, m, "bravo", 2)
	if err := m.Focus("alpha"); err != nil {
		t.Fatal(err)
	}
	close(p.release)
	queueReady(t, m, "bravo")
	w, _ := m.Get("alpha")
	st, err := w.Index.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Embeddings == nil || st.Embeddings.Pending == 0 {
		t.Fatal("background did not advance until focused workspace finished")
	}
}

func TestNeverAndWhenOpenedPolicies(t *testing.T) {
	m, db := open(t)
	m.opts.Log = logger.Nop{}
	p := &queueProvider{}
	m.SetEmbedding(p)
	addQueueWorkspace(t, m, "archive", 3)
	addQueueWorkspace(t, m, "seldom", 3)
	if err := m.SetEmbeddingPolicy(context.Background(), "archive", store.EmbeddingNever); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEmbeddingPolicy(context.Background(), "seldom", store.EmbeddingWhenOpened); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"archive", "seldom"} {
		w, _ := m.Get(name)
		if state, _ := w.EmbeddingQueue(); state != "paused" {
			t.Errorf("%s state = %q", name, state)
		}
		if res, err := w.Index.Search(context.Background(), "body", index.QueryOpts{}); err != nil || res.Semantic != "off" {
			t.Errorf("%s lexical search = %+v, %v", name, res, err)
		}
	}
	if err := m.Focus("seldom"); err != nil {
		t.Fatal(err)
	}
	queueReady(t, m, "seldom")
	m.queue.mu.Lock()
	m.queue.focusedAt = time.Now().Add(-focusTTL - time.Second)
	m.queue.mu.Unlock()
	w, _ := m.Get("seldom")
	if err := os.WriteFile(filepath.Join(w.Root, "later.md"), []byte("# Later\n\nnew text after focus expired\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.Index.Touch("later.md")
	indexed(t, m, "seldom", 2)
	queueReady(t, m, "seldom")
	if policy, err := db.EmbeddingPolicy(context.Background(), 9999); err == nil || policy != "" {
		t.Errorf("missing workspace policy: %q, %v", policy, err)
	}
}

func TestInFlightBatchStopsBeforeProviderReplacementOrWorkspaceRemoval(t *testing.T) {
	for _, action := range []string{"replace", "remove", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			m, _ := open(t)
			m.opts.Log = logger.Nop{}
			old := &queueProvider{entered: make(chan struct{}), release: make(chan struct{})}
			m.SetEmbedding(old)
			addQueueWorkspace(t, m, "kb", 90)
			select {
			case <-old.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("provider batch never started")
			}
			done := make(chan error, 1)
			go func() {
				switch action {
				case "replace":
					m.SetEmbedding(&queueProvider{})
					done <- nil
				case "remove":
					done <- m.Remove(context.Background(), "kb")
				default:
					m.StopAll()
					done <- nil
				}
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("transition waited on the held provider call")
			}
			old.mu.Lock()
			inFlight := old.active
			old.mu.Unlock()
			if inFlight != 0 {
				t.Errorf("%d old provider calls survived %s", inFlight, action)
			}
			if action == "replace" {
				queueReady(t, m, "kb")
			}
		})
	}
}
