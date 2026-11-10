package tui

import (
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
)

// listCalls is how many todos.list the feature has asked.
func listCalls(s *featuretest.ScriptedCore) int {
	n := 0
	for _, c := range s.Calls {
		if strings.HasPrefix(c, "todos.list") {
			n++
		}
	}
	return n
}

// TestAFailedTodoWriteReadsThePanelAgain: a write the daemon refuses (a remove of a task that
// changed since its row was read: a conflict) is told, and the store shown is read again, so the
// row it was refused for shows the task as it is now, its rev with it.
func TestAFailedTodoWriteReadsThePanelAgain(t *testing.T) {
	s := scriptedStore(true)
	v := newTodos(s)
	v.list()
	s.Answers["todos.remove"] = func([]any) (any, error) {
		return nil, &golibrpc.Error{Code: todo.CodeStale, Message: "the task changed since it was read"}
	}
	before := listCalls(s)
	v.remove()
	s.Yes()
	if len(s.Failures) != 1 || !strings.Contains(s.Failures[0], "todos.remove") {
		t.Fatalf("the refused remove was told as %v, want its notice", s.Failures)
	}
	if listCalls(s) == before {
		t.Errorf("after the refused remove the store was not read again: %v", s.Calls)
	}
}
