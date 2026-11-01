package tui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// todoFeedRig is messagingRig (agent one running in the window's scope) whose daemon serves a
// todo store at /kb, workspace kb, with one open task; every inbox.send is kept, and answered sent.
func todoFeedRig(t *testing.T) (a *agents, c *identityHost, w *crewWindow, task todo.Task, sends *[][]any) {
	t.Helper()
	a, c, w = messagingRig(t)
	disk := todotest.NewDisk()
	disk.MkdirAll("/kb")
	w.d.todos = todo.New(todotest.Roots{"kb": "/kb"}, disk, todotest.NewJournal())
	var err error
	task, err = w.d.todos.Add(context.Background(), todo.At{Workspace: "kb"}, todo.Spec{Title: "Re-sync the mirrors"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var kept [][]any
	w.inboxSend = func(p []any) (any, error) { kept = append(kept, p); return map[string]any{"message": "m1"}, nil }
	return a, c, w, task, &kept
}

// storeOf is the daemon's id of the store at /kb, and the seq of its last change.
func storeOf(t *testing.T, w *crewWindow) (id string, head int64) {
	t.Helper()
	stores, err := w.d.todos.Stores(context.Background())
	if err != nil || len(stores) != 1 {
		t.Fatalf("stores %+v, %v", stores, err)
	}
	page, err := w.d.todos.Changes(context.Background(), todo.At{Root: "/kb"}, "a-reader", 0)
	if err != nil || len(page.Changes) == 0 {
		t.Fatalf("changes %+v, %v", page, err)
	}
	return stores[0].ID, page.Changes[len(page.Changes)-1].Seq
}

// An assignment made outside the window (a bare `autodoc --call todos.assign`) to one of its own
// agents, autodoc:<codename>, reaches that agent: the window, catching up as it reconnects, mails it
// once through the crew as a notice from the user (kind message, never a task the user typed, since
// a peer may have made it), queued rather than fired, so a stopped agent finds it when it starts.
// The mail names the task (id, title, its file) and the assigner's reason, and is keyed by the
// change (todo-<store>-<seq>), so asking again after a lost answer never mails it twice. Nothing is
// typed into the agent's tab. Reconnecting again sends nothing more: the change was handled.
func TestABareAssignmentReachesTheWindowsAgent(t *testing.T) {
	a, c, w, task, sends := todoFeedRig(t)
	ctx := context.Background()
	assigned, err := w.d.todos.Assign(ctx, todo.At{Workspace: "kb"}, task.ID, task.Rev, "autodoc:one", "Mirror them all.", "")
	if err != nil {
		t.Fatal(err)
	}
	store, seq := storeOf(t, w)
	a.crew.Connected() // the window reconnects, and catches up
	settleMessaging(c, w)
	if len(*sends) != 1 {
		t.Fatalf("inbox.send %d times (%v), want once, to one", len(*sends), *sends)
	}
	scope, _, _ := a.crew.held()
	p := (*sends)[0]
	if p[0] != scope || !slices.Equal(p[3].([]any), []any{"one"}) {
		t.Fatalf("sent %v, want to one in the window's scope %s", p, scope)
	}
	body, _ := p[4].(string)
	stores, _ := w.d.todos.Stores(ctx)
	path := filepath.Join(stores[0].Path, filepath.FromSlash(assigned.Path))
	for _, want := range []string{task.ID, "Re-sync the mirrors", path, "Reason: Mirror them all."} {
		if !strings.Contains(body, want) {
			t.Errorf("the body lacks %q:\n%s", want, body)
		}
	}
	if key := fmt.Sprintf("todo-%s-%d", store, seq); p[5] != key {
		t.Errorf("key %v, want %s: the change's", p[5], key)
	}
	if p[6] != MessagePlain || p[7] != "[todos] task assigned to you: Re-sync the mirrors" || p[8] != false {
		t.Errorf("kind %v, subject %v, fire %v; want a plain message, the notice's subject, queued", p[6], p[7], p[8])
	}
	if len(c.panel.pastes)+len(c.panel.keys) != 0 {
		t.Errorf("typed %q and pressed %q into the agent, want nothing", c.panel.pastes, c.panel.keys)
	}
	a.crew.Connected()
	settleMessaging(c, w)
	if len(*sends) != 1 {
		t.Errorf("after another reconnect: %d sends, want the one", len(*sends))
	}
}

// assignAndRead assigns the rig's task again (as read now) to assignee from scope, then the window
// reads its feed as a todos.changed event makes it.
func assignAndRead(t *testing.T, a *agents, c *identityHost, w *crewWindow, id, assignee, scope string) {
	t.Helper()
	ctx := context.Background()
	cur, err := w.d.todos.Get(ctx, todo.At{Workspace: "kb"}, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.todos.Assign(ctx, todo.At{Workspace: "kb"}, id, cur.Rev, assignee, "", scope); err != nil {
		t.Fatal(err)
	}
	a.crew.Event("todos.changed", "kb", id)
	settleMessaging(c, w)
}

// Only an assignment the window's own panel didn't make, to an agent of its own, is mailed, as it
// happens (a todos.changed event): one made in the window's scope (its panel hands it over itself)
// is not, nor one to an agent with no crew profile in the window's scope (two never started here;
// ghost is no one), nor another host's form. One made in another window's scope is: that window's
// panel, or a caller passing its scope, has no crew profile of this agent to deliver to, so the
// assignment would otherwise fall between the two windows. One made from Neovim's scope, or with
// none, is mailed too. Each is mailed once, whatever reads follow.
func TestOnlyTheWindowsOwnAgentsGetAnotherHostsAssignments(t *testing.T) {
	a, c, w, task, sends := todoFeedRig(t)
	scope, _, _ := a.crew.held()
	for _, x := range []struct{ assignee, scope string }{
		{"autodoc:one", scope},
		{"autodoc:two", ""},
		{"autodoc:ghost", ""},
		{"agent:one", ""},
		{"one", ""},
	} {
		assignAndRead(t, a, c, w, task.ID, x.assignee, x.scope)
		if len(*sends) != 0 {
			t.Fatalf("%s assigned from scope %q: mailed %v, want nothing", x.assignee, x.scope, *sends)
		}
	}
	for _, from := range []string{"autodoc:fedcba9876543210", "autovim:plugins", ""} {
		assignAndRead(t, a, c, w, task.ID, "autodoc:one", from)
	}
	if len(*sends) != 3 || (*sends)[0][5] == (*sends)[1][5] || (*sends)[1][5] == (*sends)[2][5] {
		t.Fatalf("mailed %v, want three (from another window's scope, Neovim's, none), each keyed by its own change", *sends)
	}
	for _, p := range *sends {
		if !slices.Equal(p[3].([]any), []any{"one"}) || p[8] != false {
			t.Errorf("sent %v, want a queued mail to one", p)
		}
	}
	a.crew.Event("todos.changed", "kb", task.ID)
	a.crew.Connected()
	settleMessaging(c, w)
	if len(*sends) != 3 {
		t.Errorf("after more reads: %d sends, want the three", len(*sends))
	}
}

// A store this window's scope has never read would replay its whole retained log: there, what was
// assigned before the window started is history, skipped, and acked; the store is then kept as read
// (<agentDir>/.crew-todos/<scope>), so a later process catches up on what was assigned while it was
// away, history or not.
func TestAStoreNeverReadSkipsItsHistoryOnce(t *testing.T) {
	a, c, w, task, sends := todoFeedRig(t)
	ctx := context.Background()
	if _, err := w.d.todos.Assign(ctx, todo.At{Workspace: "kb"}, task.ID, task.Rev, "autodoc:one", "", ""); err != nil {
		t.Fatal(err)
	}
	a.crew.todos.start = time.Now().Add(time.Hour) // the window started after the assignment
	a.crew.Connected()
	settleMessaging(c, w)
	if len(*sends) != 0 {
		t.Fatalf("history mailed: %v", *sends)
	}
	scope, _, _ := a.crew.held()
	if seen := readTodoSeen(filepath.Join(a.crew.todos.dir, scope)); len(seen) != 1 {
		t.Fatalf("stores kept as read: %v, want the one", seen)
	}

	// the next process, after a reassignment made while it was away
	a.crew.todos = &todoFeed{h: a.crew, dir: a.crew.todos.dir, start: time.Now().Add(2 * time.Hour)}
	assignAndRead(t, a, c, w, task.ID, "autodoc:one", "")
	if len(*sends) != 1 {
		t.Fatalf("the catch-up of a store read before: %d sends, want one", len(*sends))
	}
}

// A mail whose send has no outcome (a lost answer) leaves its change unacked: the feed reads again
// todoFeedRetry later and asks once more with the same key, which the daemon answers with the first
// send's answer, so the agent gets it once. A refusal for good (no such agent any more) is not asked
// again.
func TestAnAssignmentWithNoOutcomeIsAskedAgainByItsKey(t *testing.T) {
	a, c, w, task, sends := todoFeedRig(t)
	lost := 1
	w.inboxSend = func(p []any) (any, error) {
		*sends = append(*sends, p)
		if lost > 0 {
			lost--
			return nil, io.ErrUnexpectedEOF
		}
		return map[string]any{"message": "m1"}, nil
	}
	assignAndRead(t, a, c, w, task.ID, "autodoc:one", "")
	if len(*sends) != 2 || (*sends)[0][5] != (*sends)[1][5] {
		t.Fatalf("sends %v, want the lost one asked again by its key", *sends)
	}
	a.crew.Connected()
	settleMessaging(c, w)
	if len(*sends) != 2 {
		t.Fatalf("after the answer: %d sends, want two", len(*sends))
	}

	w.inboxSend = func(p []any) (any, error) {
		*sends = append(*sends, p)
		return nil, &golibrpc.Error{Code: mail.CodeNoRecipient, Message: "an address names no active profile or live session"}
	}
	assignAndRead(t, a, c, w, task.ID, "autodoc:one", "")
	a.crew.Connected()
	settleMessaging(c, w)
	if len(*sends) != 3 {
		t.Fatalf("a refusal for good: %d sends in all, want it asked once", len(*sends))
	}
}
