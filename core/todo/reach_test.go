package todo_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/todo"
)

// An assignment says who it reaches (docs/decisions/todos.md#an-assignment-says-who-it-reaches):
// todos.assign and todos.add with crew answer reach, resolved from that crew scope for the bare
// name of an agent:<name> assignee, with the warning every surface shows when nobody will be told
// (none, ambiguous: naming each) and the note when it is another project's. crew is only asked:
// the change keeps the assignment's own scope, never crew.
func TestAnAssignmentSaysWhoItReaches(t *testing.T) {
	e := claimed(t)
	e.reach = func(crew, name string) (todo.Reach, error) {
		switch name {
		case "jarvis":
			return todo.Reach{State: todo.ReachOwn}, nil
		case "ultron":
			return todo.Reach{State: todo.ReachOther, Address: "ultron@mcat"}, nil
		case "cloud":
			return todo.Reach{State: todo.ReachAmbiguous, Candidates: []string{"cloud@a", "cloud@b"}}, nil
		}
		return todo.Reach{State: todo.ReachNone}, nil
	}
	assign := func(c context.Context, assignee string, crew ...any) (todo.Assigned, error) {
		t.Helper()
		added, err := verb(t, e.m, "todos.add").Call(ctx, []any{"kb", map[string]any{"title": "For " + assignee}})
		if err != nil {
			t.Fatal(err)
		}
		task := added.(todo.Assigned).Task
		out, err := verb(t, e.m, "todos.assign").Call(c, append([]any{"kb", task.ID, task.Rev, assignee, "", "auto-agents:p1"}, crew...))
		if err != nil {
			return todo.Assigned{}, err
		}
		return out.(todo.Assigned), nil
	}
	for _, c := range []struct {
		assignee string
		want     todo.Reach
	}{
		{"agent:jarvis", todo.Reach{State: todo.ReachOwn}},
		{"agent:ultron", todo.Reach{State: todo.ReachOther, Address: "ultron@mcat", Note: "ultron is another project's agent: told as ultron@mcat"}},
		{"agent:nobody", todo.Reach{State: todo.ReachNone, Warning: "nobody will be told: no project has an agent named nobody"}},
		{"agent:cloud", todo.Reach{State: todo.ReachAmbiguous, Candidates: []string{"cloud@a", "cloud@b"},
			Warning: "nobody will be told: cloud is cloud@a or cloud@b; assign one of them by name@project"}},
	} {
		got, err := assign(ctx, c.assignee, "autovim:plugins")
		if err != nil {
			t.Fatalf("%s: %v", c.assignee, err)
		}
		if got.Assignee != c.assignee || got.Reach == nil || !reflect.DeepEqual(*got.Reach, c.want) {
			t.Errorf("%s: assignee %q, reach %+v; want %+v", c.assignee, got.Assignee, got.Reach, c.want)
		}
	}
	if want := []string{"autovim:plugins|jarvis", "autovim:plugins|ultron", "autovim:plugins|nobody", "autovim:plugins|cloud"}; !reflect.DeepEqual(e.asked, want) {
		t.Errorf("reach asked %q; want each bare name from the crew scope given", e.asked)
	}
	// todos.add with an assignee is an assignment too: the same reach
	added, err := verb(t, e.m, "todos.add").Call(ctx, []any{"kb", map[string]any{"title": "Added for ultron", "assignee": "agent:ultron"}, "auto-agents:p1", "autovim:plugins"})
	if r := added.(todo.Assigned).Reach; err != nil || r == nil || r.State != todo.ReachOther || r.Address != "ultron@mcat" {
		t.Errorf("todos.add with an assignee and crew: reach %+v, %v; want ultron@mcat", r, err)
	}
	// the change keeps the assignment's scope, never crew
	page, err := e.m.Changes(ctx, kb, "reach-reader", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range page.Changes {
		if ch.Kind == todo.KindAssign && ch.Scope != "auto-agents:p1" {
			t.Errorf("an assign change's scope %q; want the assignment's auto-agents:p1, crew is read for the answer alone", ch.Scope)
		}
	}
}

// Nothing to say, no reach: no crew given (a caller below 54, as every caller before it), an
// assignee in any form but agent:<name> (the user, a window's, an instance's, an address, a
// cleared one), and a lookup that fails, after which the assignment still stands. None of them
// asks the lookup but the failing one.
func TestAnAssignmentWithNothingToSayHasNoReach(t *testing.T) {
	e := claimed(t)
	e.reach = func(crew, name string) (todo.Reach, error) { return todo.Reach{State: todo.ReachOwn}, nil }
	n := 0
	add := func() todo.Task {
		t.Helper()
		n++
		out, err := verb(t, e.m, "todos.add").Call(ctx, []any{"kb", map[string]any{"title": fmt.Sprintf("Nothing to say %d", n)}})
		if err != nil {
			t.Fatal(err)
		}
		return out.(todo.Assigned).Task
	}
	for _, c := range []struct {
		assignee string
		crew     []any
	}{
		{"agent:jarvis", nil},
		{"agent:jarvis", []any{""}},
		{"user", []any{"autovim:plugins"}},
		{"autodoc:one", []any{"autovim:plugins"}},
		{"agent:jarvis:inst-1", []any{"autovim:plugins"}},
		{"agent:jarvis@mcat", []any{"autovim:plugins"}},
		{"", []any{"autovim:plugins"}},
	} {
		task := add()
		out, err := verb(t, e.m, "todos.assign").Call(ctx, append([]any{"kb", task.ID, task.Rev, c.assignee, "", "auto-agents:p1"}, c.crew...))
		if err != nil {
			t.Fatalf("%q crew %v: %v", c.assignee, c.crew, err)
		}
		if r := out.(todo.Assigned).Reach; r != nil {
			t.Errorf("%q crew %v: reach %+v; want none", c.assignee, c.crew, r)
		}
		if wire := wireOf(t, out); wire["reach"] != nil || wire["id"] != task.ID || wire["Task"] != nil {
			t.Errorf("%q crew %v: the wire answer %v; want the task's keys flat (id %s) and no reach", c.assignee, c.crew, wire, task.ID)
		}
	}
	if len(e.asked) != 0 {
		t.Errorf("reach asked %q; want no lookup for an answer with nothing to say", e.asked)
	}
	e.reach = func(crew, name string) (todo.Reach, error) { return todo.Reach{}, errors.New("the crew store is down") }
	task := add()
	out, err := verb(t, e.m, "todos.assign").Call(ctx, []any{"kb", task.ID, task.Rev, "agent:jarvis", "", "auto-agents:p1", "autovim:plugins"})
	if err != nil {
		t.Fatalf("a failed lookup failed the assignment: %v", err)
	}
	if a := out.(todo.Assigned); a.Reach != nil || a.Assignee != "agent:jarvis" {
		t.Errorf("after a failed lookup: %+v; want the assignment written and no reach", a)
	}
	if got, err := e.m.Get(ctx, kb, task.ID); err != nil || got.Assignee != "agent:jarvis" {
		t.Errorf("the task after a failed lookup: %+v, %v; want it assigned", got, err)
	}
}

// crew is a protocol-54 param: a session below it is refused crew (and its assignment isn't
// written), while the same session without crew assigns as before. A crew scope past 256 bytes is
// invalid, as scope is.
func TestCrewIsRefusedBelowProtocol54AndBounded(t *testing.T) {
	e := claimed(t)
	e.reach = func(crew, name string) (todo.Reach, error) { return todo.Reach{State: todo.ReachOwn}, nil }
	out, err := verb(t, e.m, "todos.add").Call(ctx, []any{"kb", map[string]any{"title": "Old session"}})
	if err != nil {
		t.Fatal(err)
	}
	task := out.(todo.Assigned).Task
	old := module.WithProtocol(ctx, 53)
	if _, err := verb(t, e.m, "todos.assign").Call(old, []any{"kb", task.ID, task.Rev, "agent:jarvis", "", "auto-agents:p1", "autovim:plugins"}); !errors.Is(err, module.ErrInvalidParams) || !strings.Contains(err.Error(), "crew") {
		t.Errorf("crew at protocol 53: %v; want the param refused", err)
	}
	if got, _ := e.m.Get(ctx, kb, task.ID); got.Assignee != "" {
		t.Errorf("a refused crew wrote the assignment: %+v", got)
	}
	if _, err := verb(t, e.m, "todos.add").Call(old, []any{"kb", map[string]any{"title": "Old add", "assignee": "agent:jarvis"}, "auto-agents:p1", "autovim:plugins"}); !errors.Is(err, module.ErrInvalidParams) {
		t.Errorf("todos.add's crew at protocol 53: %v; want the param refused", err)
	}
	if out, err := verb(t, e.m, "todos.assign").Call(old, []any{"kb", task.ID, task.Rev, "agent:jarvis", "", "auto-agents:p1"}); err != nil || out.(todo.Assigned).Reach != nil {
		t.Errorf("protocol 53 without crew: %v; want the assignment as before, no reach", err)
	}
	fresh, err := verb(t, e.m, "todos.add").Call(ctx, []any{"kb", map[string]any{"title": "Long crew"}})
	if err != nil {
		t.Fatal(err)
	}
	ft := fresh.(todo.Assigned).Task
	if _, err := verb(t, e.m, "todos.assign").Call(ctx, []any{"kb", ft.ID, ft.Rev, "agent:jarvis", "", "", strings.Repeat("x", todo.MaxScope+1)}); !errors.Is(err, todo.ErrInvalid) {
		t.Errorf("a crew scope of %d bytes: %v; want ErrInvalid", todo.MaxScope+1, err)
	}
}
