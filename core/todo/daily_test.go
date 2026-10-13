package todo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// refreshedAt is when the daily refresh last ran on the kb store, as the journal keeps it.
func (e *env) refreshedAt() time.Time {
	e.t.Helper()
	st, err := e.j.Settings(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return st.Stores[e.storeID()].RefreshedAt
}

func (e *env) clock() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }

// The daemon refreshes each claimed store once a day by itself (ADR 1791532020 §2.3), with no
// client asking: at once when its last run is a day old or it never ran, then a day after each run.
// The run is kept in the journal, so a daemon restarted within the day does not run it again. A
// store not claimed is never refreshed.
func TestTheDailyRefresh(t *testing.T) {
	e := claimed(t)
	e.disk.WriteFile(kbStore+"/completed/old.md", []byte(taskFile("old", "completed"))) // completed 2026-10-01
	e.disk.WriteFile("/proj/.todo-list/completed/p.md", []byte(taskFile("p", "completed")))
	if _, err := e.m.Register(ctx, "/proj"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Resolve(ctx, todo.At{Root: "/proj"}); err != nil { // its record: a store, not claimed
		t.Fatal(err)
	}

	e.m.RefreshDue(ctx) // it never ran: it runs, and 10 days is not 28
	if got := e.refreshedAt(); !got.Equal(e.clock()) {
		t.Fatalf("the first run was not recorded: %v", got)
	}
	if e.get("old").Status != "completed" {
		t.Fatal("archived after 10 days")
	}

	e.advance(19 * 24 * time.Hour) // 29 days since its completion
	e.restart()
	e.m.RefreshDue(ctx)
	if e.get("old").Status != "archived" {
		t.Fatal("the daily refresh did not archive a task completed 29 days ago")
	}
	if !e.exists("/proj/.todo-list/completed/p.md") {
		t.Error("a store not claimed was refreshed")
	}
	var changed []todo.Event
	for _, ev := range e.events {
		if ev.Kind == "todos.changed" {
			changed = append(changed, ev)
		}
	}
	if len(changed) != 1 || changed[0].Workspace != "kb" {
		t.Errorf("the daily refresh's todos.changed, as the verb logs it: %+v", e.events)
	}
	ran := e.refreshedAt()

	e.disk.WriteFile(kbStore+"/completed/late.md", []byte(taskFile("late", "completed")))
	e.advance(23 * time.Hour)
	e.restart()
	e.m.RefreshDue(ctx)
	if e.get("late").Status != "completed" || !e.refreshedAt().Equal(ran) {
		t.Fatal("a restart within the day ran the daily refresh again")
	}
	e.advance(time.Hour)
	e.m.RefreshDue(ctx)
	if e.get("late").Status != "archived" || !e.refreshedAt().Equal(e.clock()) {
		t.Fatal("the daily refresh did not run a day after the last")
	}
}

// A daily refresh that fails is reported, in the log and sys.events, and counts as the day's run:
// it is not tried, nor reported, again until a day later. A claimed store with no directory yet
// has nothing to refresh, and is no failure.
func TestADailyRefreshFailureIsReported(t *testing.T) {
	e := newEnv(t)
	e.must(e.m.Claim(ctx, kb)) // claimed, not scaffolded
	e.m.RefreshDue(ctx)
	if kinds := e.eventKinds(); len(kinds) != 0 {
		t.Errorf("a store with no directory: events %v", kinds)
	}
	if _, err := e.m.Scaffold(ctx, kb); err != nil {
		t.Fatal(err)
	}
	e.advance(24 * time.Hour)
	delete(e.roots, "kb") // the workspace is gone: its store is no longer writable
	e.m.RefreshDue(ctx)
	e.m.RefreshDue(ctx)
	if kinds := e.eventKinds(); len(kinds) != 1 || kinds[0] != "todos.refresh_failed" || e.events[0].Store == "" || e.events[0].Detail == "" {
		t.Fatalf("events %+v", e.events)
	}
	e.advance(24 * time.Hour)
	e.m.RefreshDue(ctx)
	if kinds := e.eventKinds(); len(kinds) != 2 {
		t.Errorf("a day later, the failure was not tried and reported again: %v", kinds)
	}
	// a journal it cannot read is reported too
	e.j.Fail = func(op string) error {
		if op == "Settings" {
			return errors.New("the journal is unreadable")
		}
		return nil
	}
	e.m.RefreshDue(ctx)
	e.j.Fail = nil
	if kinds := e.eventKinds(); len(kinds) != 3 || kinds[2] != "todos.refresh_failed" || e.events[2].Detail != "the journal is unreadable" {
		t.Errorf("an unreadable journal: %+v", e.events)
	}
}

// A project root's claimed store is refreshed daily too, and its todos.changed names the root, as
// the verb's does for a {root}.
func TestTheDailyRefreshOfAProjectRoot(t *testing.T) {
	e := newEnv(t)
	proj := todo.At{Root: "/proj"}
	e.disk.WriteFile("/proj/.todo-list/completed/p.md", []byte(taskFile("p", "completed")))
	if _, err := e.m.Register(ctx, "/proj"); err != nil {
		t.Fatal(err)
	}
	e.must(e.m.Claim(ctx, proj))
	e.advance(29 * 24 * time.Hour)
	e.m.RefreshDue(ctx)
	if task, err := e.m.Get(ctx, proj, "p"); err != nil || task.Status != "archived" {
		t.Fatalf("%+v %v", task, err)
	}
	if last := e.events[len(e.events)-1]; last.Kind != "todos.changed" || last.Path != "/proj" || last.Workspace != "" {
		t.Errorf("events %+v", e.events)
	}
}

// RefreshDaily runs the daily refresh at once, then at each check a store is due, until its context
// ends; then it returns.
func TestRefreshDailyRunsUntilItsContextEnds(t *testing.T) {
	e := claimed(t)
	changed := make(chan todo.Event, 16)
	m := todo.New(e.roots, e.disk, e.j, todo.WithClock(e.clock), todo.WithHome("/home/u"),
		todo.WithRefreshCheck(time.Millisecond), todo.WithEvents(func(ev todo.Event) { changed <- ev }))
	wait := func(what string) {
		t.Helper()
		select {
		case ev := <-changed:
			if ev.Kind != "todos.changed" {
				t.Fatalf("%s: %+v", what, ev)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: no daily refresh", what)
		}
	}
	e.disk.WriteFile(kbStore+"/completed/a.md", []byte(taskFile("a", "completed")))
	e.advance(29 * 24 * time.Hour)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.RefreshDaily(runCtx)
	}()
	wait("at its start")
	e.disk.WriteFile(kbStore+"/completed/b.md", []byte(taskFile("b", "completed")))
	e.advance(25 * time.Hour)
	wait("at a check a day later")
	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RefreshDaily did not return when its context ended")
	}
	for _, id := range []string{"a", "b"} {
		if task, err := m.Get(ctx, kb, id); err != nil || task.Status != "archived" {
			t.Errorf("%s: %+v %v", id, task, err)
		}
	}
}
