package tui

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
)

// aliasDisk is the in-memory disk with one folder reached by a second spelling (a link, a short
// name, another case): opening alias opens canon, and canon is the path it answers, as a real disk
// answers a folder's canonical path.
type aliasDisk struct {
	*todotest.Disk
	alias, canon string
}

func (d aliasDisk) Open(ctx context.Context, dir string) (vfs.FS, string, error) {
	if dir == d.alias {
		dir = d.canon
	}
	return d.Disk.Open(ctx, dir)
}

// asSent is v as the daemon sends it, through JSON.
func asSent(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// managerCore is a host with no workspace whose daemon's todos verbs are m's own answers.
func managerCore(t *testing.T, m *todo.Manager) *featuretest.ScriptedCore {
	ctx := context.Background()
	s := newScripted("")
	s.Answers["todos.register"] = func(p []any) (any, error) {
		fresh, err := m.Register(ctx, p[0].(string))
		return asSent(t, map[string]bool{"new": fresh}), err
	}
	s.Answers["todos.resolve"] = func(p []any) (any, error) {
		r, err := m.Resolve(ctx, todo.At{Root: p[0].(map[string]any)["root"].(string)})
		return asSent(t, r), err
	}
	s.Answers["todos.stores"] = func([]any) (any, error) {
		st, err := m.Stores(ctx)
		return asSent(t, st), err
	}
	s.Answers["todos.list"] = func([]any) (any, error) { return map[string]any{"tasks": []any{}}, nil }
	return s
}

// A folder loaded is shown as the store the daemon resolves for it: a folder never resolved before
// (its store recorded as it is resolved), and one reached by another spelling, shown as the store at
// its canonical place.
func TestALoadedFolderIsShownByTheStoreTheDaemonResolves(t *testing.T) {
	disk := todotest.NewDisk()
	fresh, canon, alias := slashFile("/fresh"), slashFile("/canon"), slashFile("/alias")
	disk.MkdirAll(fresh)
	disk.MkdirAll(canon)
	m := todo.New(todotest.Roots{}, aliasDisk{disk, alias, canon}, todotest.NewJournal())
	v := newTodos(managerCore(t, m))
	v.list()
	for _, c := range []struct{ chosen, want string }{{taskFile("/fresh"), fresh}, {taskFile("/alias"), canon}} {
		v.folderAs = "load"
		v.folderChosen(c.chosen)
		if v.cur == nil || v.cur.id != "root:"+c.want {
			t.Errorf("%s loaded: shown %+v; want the store at %s", c.chosen, v.cur, c.want)
		}
	}
}

// A store's root and the home folder as ~ by whole segments, a volume's root included: the root of
// /.todo-list is /, and under a home at / a folder is ~/notes.
func TestTheRootsAndHomeOfAStoreAtAVolumesRoot(t *testing.T) {
	for p, want := range map[string]string{"/.todo-list": "/", "/p/.todo-list": "/p", "/p": "/p"} {
		if got := storeRoot(p); got != want {
			t.Errorf("storeRoot(%q) = %q, want %q", p, got, want)
		}
	}
	env := procenv.Env(nil).With("HOME=/", "USERPROFILE=/")
	if got := tildePath(env, "/notes"); got != "~/notes" {
		t.Errorf("tildePath under a home at /: %q", got)
	}
	if got := tildePath(env, "/"); got != "~" {
		t.Errorf("tildePath of the home itself: %q", got)
	}
}
