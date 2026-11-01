package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

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
	path := "/kb/" + assigned.Path
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
