package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// puller is an Ollama whose /api/pull streams a layer's progress, then holds the stream open until
// released (or the request ends), then says success, or the error line when failing is set.
type puller struct {
	*httptest.Server
	mu      sync.Mutex
	release chan struct{}
	ended   chan struct{} // closed when a pull's request ends
	failing string
}

func newPuller(t *testing.T) *puller {
	p := &puller{release: make(chan struct{}), ended: make(chan struct{}, 8)}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			http.NotFound(w, r)
			return
		}
		defer func() { p.ended <- struct{}{} }()
		fmt.Fprintln(w, `{"status":"pulling a","digest":"sha256:a","total":1000,"completed":100}`)
		w.(http.Flusher).Flush()
		p.mu.Lock()
		release, failing := p.release, p.failing
		p.mu.Unlock()
		if failing != "" {
			fmt.Fprintf(w, "{\"error\":%q}\n", failing)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprintln(w, `{"status":"pulling a","digest":"sha256:a","total":1000,"completed":1000}`)
		fmt.Fprintln(w, `{"status":"success"}`)
	}))
	t.Cleanup(p.Close)
	return p
}

// started is a daemon's Embedding, started under a context the test ends: cancel is the daemon's
// shutdown, and wait joins it.
func started(t *testing.T) (e *Embedding, cancel func(), wait func()) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(ctx, db, Options{Poll: 20 * time.Millisecond, BatchDelay: 5 * time.Millisecond})
	e = NewEmbedding(db, m, nil)
	e.Start(ctx)
	var once sync.Once
	wait = func() {
		once.Do(func() {
			stop()
			e.Wait()
			m.StopAll()
			_ = db.Close()
		})
	}
	t.Cleanup(wait)
	return e, stop, wait
}

func state(t *testing.T, e *Embedding, job string) rpc.PullState {
	t.Helper()
	s, err := e.PullStatus(job)
	if err != nil {
		t.Fatalf("PullStatus(%s): %v", job, err)
	}
	return s
}

// A pull runs on after the request that started it, a second is refused while it runs, and it ends
// done when the stream says success.
func TestAPullIsADaemonJob(t *testing.T) {
	p := newPuller(t)
	e, _, _ := started(t)
	job, err := e.Pull(p.URL, "m")
	if err != nil || !strings.HasPrefix(job, "pull-") {
		t.Fatalf("Pull: %q, %v", job, err)
	}
	eventually(t, "its progress", func() bool { return state(t, e, job).Completed == 100 })
	if s := state(t, e, job); s.State != PullRunning || s.Total != 1000 || s.Model != "m" {
		t.Fatalf("while it runs: %+v", s)
	}
	if _, err := e.Pull(p.URL, "other"); !errors.Is(err, rpc.ErrPulling) {
		t.Fatalf("a second pull while one runs: %v", err)
	}
	close(p.release)
	eventually(t, "done", func() bool { return state(t, e, job).State == PullDone })
	if s := state(t, e, job); s.Completed != 1000 || s.ETA != nil {
		t.Fatalf("done: %+v", s)
	}
}

// A cancel ends the pull and closes its request; the next pull has another id, and a status or a
// cancel naming the old one is not found, so it never touches the new one.
func TestAStaleJobNeverTouchesTheNextPull(t *testing.T) {
	p := newPuller(t)
	e, _, _ := started(t)
	old, err := e.Pull(p.URL, "m")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first pull's progress", func() bool { return state(t, e, old).Completed == 100 })
	s, err := e.CancelPull(old)
	if err != nil || s.State != PullCancelled {
		t.Fatalf("CancelPull: %+v, %v", s, err)
	}
	select {
	case <-p.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled pull's request never ended")
	}
	if again, err := e.CancelPull(old); err != nil || again.State != PullCancelled {
		t.Fatalf("cancelling an ended job again: %+v, %v", again, err)
	}
	next, err := e.Pull(p.URL, "m")
	if err != nil || next == old {
		t.Fatalf("the next pull: %q, %v; want a new id", next, err)
	}
	if _, err := e.PullStatus(old); !errors.Is(err, rpc.ErrNoPull) {
		t.Errorf("the old job's status: %v", err)
	}
	if _, err := e.CancelPull(old); !errors.Is(err, rpc.ErrNoPull) {
		t.Errorf("a stale cancel: %v", err)
	}
	if s := state(t, e, next); s.State != PullRunning {
		t.Fatalf("a stale cancel touched the next pull: %+v", s)
	}
	close(p.release)
	eventually(t, "the next pull done", func() bool { return state(t, e, next).State == PullDone })
}

// The daemon's shutdown cancels a running pull, and Wait returns only once it has ended.
func TestShutdownEndsAPull(t *testing.T) {
	p := newPuller(t)
	e, stop, wait := started(t)
	job, err := e.Pull(p.URL, "m")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "its progress", func() bool { return state(t, e, job).Completed == 100 })
	stop()
	waited := make(chan struct{})
	go func() { wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		close(p.release) // so the pull ends and the cleanup's Wait returns: the failure must not hang
		t.Fatal("Wait did not return: the pull outlived the daemon")
	}
	if s := e.job.stateOf(); s.State != PullCancelled {
		t.Fatalf("after shutdown: %+v", s)
	}
}

// A pull the server fails says failed, with why.
func TestAFailedPullSaysWhy(t *testing.T) {
	p := newPuller(t)
	p.failing = "pull model manifest: file does not exist"
	e, _, _ := started(t)
	job, err := e.Pull(p.URL, "nope")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "failed", func() bool { return state(t, e, job).State == PullFailed })
	if s := state(t, e, job); !strings.Contains(s.Error, "file does not exist") {
		t.Fatalf("failed: %+v", s)
	}
}

// Two daemons' first pulls have different ids: a restart never reuses one.
func TestAPullsIDIsNeverReusedAcrossRestarts(t *testing.T) {
	p := newPuller(t)
	close(p.release)
	a, _, _ := started(t)
	b, _, _ := started(t)
	ja, err := a.Pull(p.URL, "m")
	if err != nil {
		t.Fatal(err)
	}
	jb, err := b.Pull(p.URL, "m")
	if err != nil || ja == jb {
		t.Fatalf("two daemons' first pulls: %q and %q, %v", ja, jb, err)
	}
}

// fullSlots holds every shared embedding slot, as a fill using them all does, until release.
func fullSlots(e *Embedding) (release func()) {
	e.ws.chg.Lock()
	slots := e.ws.slots
	e.ws.chg.Unlock()
	for range cap(slots) {
		slots <- struct{}{}
	}
	return sync.OnceFunc(func() {
		for range cap(slots) {
			<-slots
		}
	})
}

// The setup loads a model only in a shared slot: with every slot held, neither the identity probe
// nor the sample reaches Ollama, and a model not installed is answered from the server's list
// without waiting for one. Once a slot frees, both load the model at the setup's context.
func TestTheSetupLoadsAModelOnlyInASlot(t *testing.T) {
	o := newOllama(t, "m")
	e, _, _ := started(t)
	release := fullSlots(e)
	defer release()
	bounded := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 300*time.Millisecond)
	}
	ctx, cancel := bounded()
	_, err := e.Sample(ctx, o.URL, "m", 2048, []string{"a"})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a sample with every slot held: %v; want it to wait for one", err)
	}
	ctx, cancel = bounded()
	_, _, err = e.Installed(ctx, o.URL, "m", 2048)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an identity probe with every slot held: %v; want it to wait for one", err)
	}
	if got := o.contexts(); len(got) != 0 {
		t.Fatalf("Ollama embedded %v while every slot was held", got)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	_, ok, err := e.Installed(ctx, o.URL, "absent", 2048)
	cancel()
	if ok || err != nil {
		t.Fatalf("a model not installed, every slot held: %v, %v; want not installed, at once", ok, err)
	}

	release()
	m, ok, err := e.Installed(context.Background(), o.URL, "m", 2048)
	if !ok || err != nil || m.Dims != 3 || m.Digest != "sha256:m" {
		t.Fatalf("Installed: %+v, %v, %v", m, ok, err)
	}
	if _, err := e.Sample(context.Background(), o.URL, "m", 2048, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	got := o.contexts()
	if len(got) < 3 || slices.ContainsFunc(got, func(n int) bool { return n != 2048 }) {
		t.Fatalf("num_ctx of each embed %v; want the setup's 2048 on every one, the identity probe's too", got)
	}
}
