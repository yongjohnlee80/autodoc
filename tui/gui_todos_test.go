//go:build gui

package tui

import (
	"context"
	"strings"
	"testing"

	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// Under --gui's style the todos panel is the TUI's, the same QML: the store's dropdown and the icons
// along the top, a tab per status; on a task its action row is Status…, Assign, Remove and Add on one
// line, nothing greyed; ? opens the card of its keys and ? closes it; s's picker moves the task to
// deferred, the Deferred tab showing.
func TestTheGUITodosPanelActionRowCardAndStatusPicker(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	seedTodos(t, d, true, todo.Spec{Title: "Ship it"})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(guidecl.Native())}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	openTodos(t, r, "kb", '1')
	r.s.WaitForText(t, "Ship it")
	r.s.WaitForText(t, "status: open")
	r.s.WaitFor(t, "the task's action row, one line", func(sc string) bool {
		for _, line := range strings.Split(sc, "\n") {
			if strings.Contains(line, "Status…") && strings.Contains(line, "Assign") && strings.Contains(line, "Remove") && strings.Contains(line, "Add") {
				return !strings.Contains(sc, "Status ▶")
			}
		}
		return false
	})
	r.keys(t, key('?'))
	r.s.WaitForText(t, "todos — keys")
	r.keys(t, key('?'))
	r.s.WaitFor(t, "the card closed by ?", func(sc string) bool { return !strings.Contains(sc, "todos — keys") })
	chooseStatusOf(t, r, "deferred")
	r.s.WaitFor(t, "deferred", func(string) bool {
		l, err := d.todos.List(context.Background(), kbTodos, todo.ListOpts{Buckets: []string{"deferred"}})
		return err == nil && len(l.Tasks) == 1
	})
	r.s.WaitForText(t, "status: deferred")
	for _, icon := range []string{"✚", "⎘", "✎", "⟳"} {
		if !strings.Contains(r.s.String(), icon) {
			t.Errorf("the top bar lacks %s", icon)
		}
	}
}
