package sqlitecrewmail_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestSQLiteConforms: the crew tables on the daemon's SQLite store keep the crew state machines,
// as the in-memory store does (crewtest.Run).
func TestSQLiteConforms(t *testing.T) {
	crewtest.Run(t, crewtest.Harness{New: func(t *testing.T, now func() time.Time) crew.Store {
		s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "autodoc.db"), store.WithClock(now))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return sqlitecrewmail.New(s)
	}})
}

// TestLostAnswersSurviveARestart (docs/decisions/crew.md#what-the-crew-guarantees): crew.claim's
// and crew.register's first answers are lost after they commit, and the daemon restarts (the store
// closed and opened again). Retries with the same run, instance and secret answer the same epoch
// and session; a replay with another codename, kind or workspace is refused against the stored
// args_digest, and so is one for the session once it ended.
func TestLostAnswersSurviveARestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	clock := crewtest.NewClock()
	open := func() (*store.Store, *crew.Manager) {
		s, err := store.Open(ctx, path, store.WithClock(clock.Now))
		if err != nil {
			t.Fatal(err)
		}
		return s, crew.New(sqlitecrewmail.New(s))
	}
	s, m := open()
	e, err := m.Claim(ctx, "autodoc:aaaa", "run-a", "")
	if err != nil {
		t.Fatal(err)
	}
	r := crew.Registration{Scope: "autodoc:aaaa", Run: "run-a", Epoch: e, Codename: "gold", Kind: "claude",
		Workspace: "w", Instance: "agent-1", Secret: strings.Repeat("s", 40)}
	first, err := m.Register(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil { // both answers lost; the daemon restarts
		t.Fatal(err)
	}
	s, m = open()
	t.Cleanup(func() { _ = s.Close() })
	if again, err := m.Claim(ctx, "autodoc:aaaa", "run-a", ""); err != nil || again != e {
		t.Fatalf("the claim's retry after the restart: epoch %d, %v; want %d", again, err, e)
	}
	if again, err := m.Register(ctx, r); err != nil || again != first {
		t.Fatalf("the register's retry after the restart: %+v, %v; want %+v", again, err, first)
	}
	for name, change := range map[string]func(*crew.Registration){
		"another codename":  func(r *crew.Registration) { r.Codename = "vision" },
		"another kind":      func(r *crew.Registration) { r.Kind = "codex" },
		"another workspace": func(r *crew.Registration) { r.Workspace = "w2" },
	} {
		bad := r
		change(&bad)
		if _, err := m.Register(ctx, bad); !errors.Is(err, crew.ErrInstanceReused) {
			t.Errorf("a replay with %s after the restart: %v, want ErrInstanceReused", name, err)
		}
	}
	if err := m.Exit(ctx, "autodoc:aaaa", "run-a", e, first.Session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Register(ctx, r); !errors.Is(err, crew.ErrInstanceReused) {
		t.Fatalf("a replay for the ended session: %v, want ErrInstanceReused", err)
	}
}
