package tui

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// parkedVerbs is a module whose one verb, park.wait, holds its handler until released: the
// TUI's own background work, a call the daemon takes long to answer, made deterministic.
type parkedVerbs struct {
	entered chan struct{} // one per handler that has started
	release chan struct{} // closed: every parked handler answers
	once    sync.Once
}

func newParkedVerbs() *parkedVerbs {
	return &parkedVerbs{entered: make(chan struct{}, 64), release: make(chan struct{})}
}

func (p *parkedVerbs) free() { p.once.Do(func() { close(p.release) }) }

func (*parkedVerbs) Name() string { return "park" }

func (*parkedVerbs) Errors() []module.PublicError { return nil }

func (p *parkedVerbs) Verbs() []module.Verb {
	type waitParams struct {
		Tag string `param:"tag,optional" doc:"which call"`
	}
	c := module.Contract{Verb: "park.wait", Since: 1, Scope: module.ScopeLocalPeer, Audience: module.AudienceUser,
		Effect: module.EffectRead, Result: "{}", Doc: "a cell's call held until released"}
	return []module.Verb{
		module.Handle(c, func(ctx context.Context, _ waitParams) (struct{}, error) {
			p.entered <- struct{}{}
			select {
			case <-p.release:
			case <-ctx.Done():
			}
			return struct{}{}, nil
		}),
	}
}

// TestAnOpenIsAnsweredWhileTheConnectionsOtherCallsWait: the stuck open's acceptance cell. With
// the TUI's one connection holding calls the daemon has not answered (n of them, parked in their
// handlers), opening a note is still answered: the open must not wait for those calls to end.
// 7 parked leaves a handler free; 8 is the daemon's per-connection handler bound, at which the
// daemon reads no further request until one ends. When the open is not answered, the cell
// releases the parked calls and says whether the open then lands, which tells the bound from
// another cause.
func TestAnOpenIsAnsweredWhileTheConnectionsOtherCallsWait(t *testing.T) {
	for _, n := range []int{7, 8} {
		t.Run(fmt.Sprintf("%d parked", n), func(t *testing.T) {
			t.Parallel()
			p := newParkedVerbs()
			d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "the note's body\n"}}, daemonOpts{modules: []module.Module{p}})
			r := runTUI(t, NewSession(d.sock, nil), Options{})
			t.Cleanup(p.free) // registered last, so it runs first: before the TUI and the daemon stop
			r.ready(t)
			calls := make(chan error, n)
			for i := range n { // from workers, as the TUI's own background work calls
				go func() {
					_, err := r.h.call(context.Background(), "park.wait", fmt.Sprint(i))
					calls <- err
				}()
			}
			for i := range n { // every one is in its handler: the connection holds n unanswered calls
				select {
				case <-p.entered:
				case err := <-calls: // answered before its handler parked: the call itself failed
					t.Fatalf("a park.wait call was answered before it parked: %v", err)
				case <-time.After(daemonWait):
					t.Fatalf("only %d of the %d calls reached the daemon's handlers within %s", i, n, daemonWait)
				}
			}
			r.h.p.Post(func() { r.h.openPath("a.md") })
			opened := func() bool { f := r.file(); return f.open && f.path == "a.md" && !f.dirty }
			deadline := time.Now().Add(daemonWait)
			for !opened() && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if opened() {
				if got := r.editorText(); got != "the note's body\n" {
					t.Errorf("with %d calls parked: a.md opened holding %q", n, got)
				}
				return
			}
			screen := r.s.String()
			p.free()
			after := time.Now().Add(daemonWait)
			for !opened() && time.Now().Before(after) {
				time.Sleep(5 * time.Millisecond)
			}
			t.Errorf("with %d calls parked on the connection, opening a.md was not answered within %s; once they were released it %s:\n%s",
				n, daemonWait, map[bool]string{true: "was (the open waited on the parked calls)", false: "still was not"}[opened()], screen)
		})
	}
}
