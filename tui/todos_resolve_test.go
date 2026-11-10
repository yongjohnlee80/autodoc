package tui

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
)

// aliasDisk is the in-memory disk with folders reached by a second spelling (a link, a short name,
// another case): opening an alias opens its canonical folder, and that is the path it answers, as a
// real disk answers a folder's canonical path.
type aliasDisk struct {
	*todotest.Disk
	canon map[string]string // alias → its canonical folder
}

func (d aliasDisk) Open(ctx context.Context, dir string) (vfs.FS, string, error) {
	if c, ok := d.canon[dir]; ok {
		dir = c
	}
	return d.Disk.Open(ctx, dir)
}

// atOf is a todos verb's place as the panel sends it: a workspace's name, or a {root} map.
func atOf(p any) todo.At {
	if m, ok := p.(map[string]any); ok {
		root, _ := m["root"].(string)
		return todo.At{Root: root}
	}
	ws, _ := p.(string)
	return todo.At{Workspace: ws}
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
	s.Answers["todos.scaffold"] = func(p []any) (any, error) {
		o, err := m.Scaffold(ctx, atOf(p[0]))
		return asSent(t, o), err
	}
	s.Answers["todos.list"] = func(p []any) (any, error) {
		var buckets []string
		if opts, ok := p[1].(map[string]any); ok {
			for _, b := range feature.AsList(opts["buckets"]) {
				if s, ok := b.(string); ok {
					buckets = append(buckets, s)
				}
			}
		}
		l, err := m.List(ctx, atOf(p[0]), todo.ListOpts{Buckets: buckets})
		return asSent(t, l), err
	}
	return s
}

// A folder loaded, or made a store with New…, is shown as the store the daemon resolves for it: a
// folder never resolved before (its store recorded as it is resolved), and one reached by another
// spelling, shown as the store at its canonical place; New… makes that store's buckets there.
func TestALoadedFolderIsShownByTheStoreTheDaemonResolves(t *testing.T) {
	disk := todotest.NewDisk()
	for _, d := range []string{"/before", "/fresh", "/canon", "/made", "/madecanon"} {
		disk.MkdirAll(slashFile(d))
	}
	m := todo.New(todotest.Roots{}, aliasDisk{disk, map[string]string{
		slashFile("/alias"): slashFile("/canon"), slashFile("/madealias"): slashFile("/madecanon"),
	}}, todotest.NewJournal())
	t.Cleanup(func() { _ = m.Close() })
	// a store listed first (resolved, as a store is listed once it is), so a store not found among
	// them (the panel's fallback is the first) is never taken for the one each case wants
	if _, err := m.Register(context.Background(), slashFile("/before")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Resolve(context.Background(), todo.At{Root: slashFile("/before")}); err != nil {
		t.Fatal(err)
	}
	v := newTodos(managerCore(t, m))
	v.list()
	if want := "root:" + slashFile("/before"); v.cur == nil || v.cur.id != want {
		t.Fatalf("the store listed first: shown %+v; want %s", v.cur, want)
	}
	for _, c := range []struct{ as, chosen, want string }{
		{"load", "/fresh", "/fresh"}, {"load", "/alias", "/canon"},
		{"new", "/made", "/made"}, {"new", "/madealias", "/madecanon"},
	} {
		v.folderAs = c.as
		v.folderChosen(taskFile(c.chosen))
		if want := slashFile(c.want); v.cur == nil || v.cur.id != "root:"+want {
			t.Errorf("%s %s: shown %+v; want the store at %s", c.as, c.chosen, v.cur, want)
		}
		if c.as != "new" {
			continue
		}
		fsys, _, err := disk.Open(context.Background(), slashFile(c.want)+"/.todo-list")
		if err != nil {
			t.Errorf("new %s: no store at %s: %v", c.chosen, c.want, err)
			continue
		}
		if _, err := fsys.Stat(context.Background(), "open"); err != nil {
			t.Errorf("new %s: the store at %s has no open bucket: %v", c.chosen, c.want, err)
		}
		_ = fsys.Close()
	}
}

// A store moved into the folder loaded is shown, by its identity: todos.stores lists it by its first
// root (/a, the one it was made under), not by the folder chosen (/moved), so a match by a root's
// spelling would miss it and leave another store shown in its place.
func TestAStoreMovedIntoTheFolderLoadedIsShown(t *testing.T) {
	ctx := context.Background()
	disk := todotest.NewDisk()
	for _, d := range []string{"/before", "/a", "/moved"} {
		disk.MkdirAll(slashFile(d))
	}
	m := todo.New(todotest.Roots{}, disk, todotest.NewJournal())
	t.Cleanup(func() { _ = m.Close() })
	for _, root := range []string{"/before", "/a"} { // /before is listed first, the panel's fallback
		if _, err := m.Register(ctx, slashFile(root)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Resolve(ctx, todo.At{Root: slashFile("/before")}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Scaffold(ctx, todo.At{Root: slashFile("/a")}); err != nil {
		t.Fatal(err)
	}
	moved, err := m.MoveStore(ctx, todo.At{Root: slashFile("/a")}, slashFile("/moved")+"/.todo-list")
	if err != nil {
		t.Fatal(err)
	}
	v := newTodos(managerCore(t, m))
	v.list()
	if v.cur == nil || v.cur.sid == moved.StoreID {
		t.Fatalf("the store shown first: %+v; want another than the moved one (the cell's premise)", v.cur)
	}
	v.folderAs = "load"
	v.folderChosen(taskFile("/moved"))
	// listed by its first root, /a: the folder chosen is not its listed root
	if v.cur == nil || v.cur.sid != moved.StoreID || v.cur.path != slashFile("/moved")+"/.todo-list" || v.cur.id != "root:"+slashFile("/a") {
		t.Errorf("/moved loaded: shown %+v; want the store moved there, %s at %s/.todo-list, listed as root:%s", v.cur, moved.StoreID, slashFile("/moved"), slashFile("/a"))
	}
}

// A folder whose store the daemon resolves but doesn't list is said so, and no other store is shown
// in its place: the one shown before stays, even when it isn't the first listed.
func TestAStoreNotListedIsSaidAndNoOtherIsShownInItsPlace(t *testing.T) {
	ctx := context.Background()
	disk := todotest.NewDisk()
	for _, d := range []string{"/before", "/fresh", "/gone"} {
		disk.MkdirAll(slashFile(d))
	}
	m := todo.New(todotest.Roots{}, disk, todotest.NewJournal())
	t.Cleanup(func() { _ = m.Close() })
	if _, err := m.Register(ctx, slashFile("/before")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Resolve(ctx, todo.At{Root: slashFile("/before")}); err != nil {
		t.Fatal(err)
	}
	core := managerCore(t, m)
	resolve := core.Answers["todos.resolve"]
	core.Answers["todos.resolve"] = func(p []any) (any, error) { // /gone's store, as if released from the list
		v, err := resolve(p)
		if r, ok := v.(map[string]any); ok && atOf(p[0]).Root == slashFile("/gone") {
			r["store_id"] = "not-listed"
		}
		return v, err
	}
	v := newTodos(core)
	v.list()
	v.folderAs = "load"
	v.folderChosen(taskFile("/fresh"))
	shown := v.cur
	if shown == nil || shown.id != "root:"+slashFile("/fresh") {
		t.Fatalf("/fresh loaded: shown %+v", shown)
	}
	v.folderChosen(taskFile("/gone"))
	if v.cur == nil || v.cur.id != shown.id {
		t.Errorf("a store not listed: shown %+v; want the one shown before, %s", v.cur, shown.id)
	}
	// said, naming the folder (the core's text is the catalog's, or the message's id)
	if !slices.ContainsFunc(core.Notes, func(n string) bool {
		return (strings.Contains(n, "todos_the_store_of_is_not_listed") || strings.Contains(n, "isn't listed")) && strings.Contains(n, "gone")
	}) {
		t.Errorf("a store not listed was not said: notes %q", core.Notes)
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
