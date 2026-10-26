package todo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// A workspace's store is the daemon's from its first write (ADR 1791532020 r13): no claim comes
// first. The write claims it as todos.claim does, marker included, and the task written there by
// hand before is adopted, its file untouched. A store Release gave back takes no write at all, a
// write no longer claiming it, until Claim; its tasks read all along. The claim, and the release,
// are the daemon's records: each survives a restart.
func TestAStoreIsClaimedByItsFirstWrite(t *testing.T) {
	e := newEnv(t)
	hand := taskFile("t", "open")
	e.disk.WriteFile(kbStore+"/open/t.md", []byte(hand))
	if l, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil || len(l.Tasks) != 1 {
		t.Fatalf("reads before any write: %+v %v", l, err)
	}
	if e.exists(kbStore + "/.autodoc-owner") {
		t.Error("a marker before any write")
	}
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "first"}, ""); err != nil {
		t.Fatalf("the first write to a workspace's store: %v", err)
	}
	if !strings.HasPrefix(e.read(kbStore+"/.autodoc-owner"), "claimed by autodoc at ") {
		t.Error("the first write wrote no marker")
	}
	if got := e.read(kbStore + "/open/t.md"); got != hand {
		t.Errorf("the hand-written task was rewritten by the claim:\n%s", got)
	}
	if r, err := e.m.Resolve(ctx, kb); err != nil || !r.Claimed || r.Released {
		t.Errorf("after the first write: %+v %v; want claimed", r, err)
	}
	task := e.get("t")
	writes := func() map[string]error {
		_, add := e.m.Add(ctx, kb, todo.Spec{Title: "x"}, "")
		_, upd := e.m.Update(ctx, kb, "t", task.Rev, todo.Patch{"title": "y"})
		_, st := e.m.Status(ctx, kb, "t", task.Rev, "deferred")
		_, as := e.m.Assign(ctx, kb, "t", task.Rev, "x", "", "")
		_, ref := e.m.Refresh(ctx, kb)
		_, sc := e.m.Scaffold(ctx, kb)
		_, mv := e.m.MoveStore(ctx, kb, "/kb/moved")
		return map[string]error{"add": add, "update": upd, "status": st, "assign": as, "refresh": ref, "scaffold": sc, "move_store": mv,
			"remove": e.m.Remove(ctx, kb, "t", task.Rev), "remove_malformed": e.m.RemoveMalformed(ctx, kb, "open/t.md")}
	}
	e.must(e.m.Release(ctx, kb))
	if e.exists(kbStore + "/.autodoc-owner") {
		t.Error("the release left the marker")
	}
	if r, err := e.m.Resolve(ctx, kb); err != nil || r.Claimed || !r.Released {
		t.Errorf("after the release: %+v %v; want released", r, err)
	}
	for verb, err := range writes() {
		if !is(err, todo.ErrUnclaimed) {
			t.Errorf("%s after the release: %v", verb, err)
		}
	}
	if l, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil || len(l.Tasks) != 2 {
		t.Errorf("reads after the release: %+v %v", l, err)
	}
	e.restart()
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "after a restart"}, ""); !is(err, todo.ErrUnclaimed) {
		t.Errorf("a released store's write after a restart: %v; want it still released", err)
	}
	e.must(e.m.Claim(ctx, kb))
	if r, err := e.m.Resolve(ctx, kb); err != nil || !r.Claimed || r.Released {
		t.Errorf("claimed again: %+v %v", r, err)
	}
	e.restart()
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "claimed again, after a restart"}, ""); err != nil {
		t.Errorf("claimed again, after a restart: %v", err)
	}
}

// A workspace's first task makes its store, as it claims it: no .todo-list, no scaffold first.
func TestAWorkspacesFirstTaskMakesItsStore(t *testing.T) {
	e := newEnv(t)
	if _, err := e.disk.FS.Stat(ctx, strings.TrimPrefix(kbStore, "/")); err == nil {
		t.Fatal("the fixture has a store already")
	}
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "first"}, ""); err != nil {
		t.Fatalf("the first task of a workspace with no store: %v", err)
	}
	for _, b := range todo.Statuses {
		if fi, err := e.disk.FS.Stat(ctx, strings.TrimPrefix(kbStore, "/")+"/"+b); err != nil || !fi.IsDir() {
			t.Errorf("bucket %s", b)
		}
	}
	if !e.exists(kbStore + "/.autodoc-owner") {
		t.Error("no marker")
	}
	if l, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil || len(l.Tasks) != 1 {
		t.Errorf("the first task: %+v %v", l, err)
	}
}

// A store a registered root has not made yet reads as an empty one (ADR 1791532020 r13): no task,
// no malformed file, cursor 0, no such task, no change. The reads write nothing: no store, no
// marker, no claim. A root no one registered is refused, as before.
func TestAStoreNotMadeYetReadsEmpty(t *testing.T) {
	e := newEnv(t)
	e.disk.MkdirAll("/src/proj")
	proj, stray := todo.At{Root: "/src/proj"}, todo.At{Root: "/src/stray"}
	e.disk.MkdirAll("/src/stray")
	if _, err := e.m.Register(ctx, "/src/proj"); err != nil {
		t.Fatal(err)
	}
	for name, at := range map[string]todo.At{"a workspace": kb, "a registered root": proj} {
		if _, err := e.m.Resolve(ctx, at); err != nil { // the store's id, recorded as any root's is
			t.Fatal(err)
		}
		var calls []string
		e.j.Fail = func(op string) error { calls = append(calls, op); return nil }
		l, err := e.m.List(ctx, at, todo.ListOpts{})
		if err != nil || len(l.Tasks) != 0 || len(l.Malformed) != 0 || l.Cursor != 0 {
			t.Errorf("%s's list: %+v %v; want an empty store's", name, l, err)
		}
		if _, err := e.m.Get(ctx, at, "nope"); !is(err, todo.ErrNotFound) {
			t.Errorf("%s's get: %v; want no such task", name, err)
		}
		e.j.Fail = nil
		for _, op := range calls {
			if !slices.Contains([]string{"Settings", "Pending", "Head", "Snapshot", "After", "Consumers"}, op) {
				t.Errorf("%s's reads wrote the journal: %s, of %v", name, op, calls)
			}
		}
		if c, err := e.m.Changes(ctx, at, "agent", 0); err != nil || len(c.Changes) != 0 || c.Resync {
			t.Errorf("%s's changes: %+v %v; want none", name, c, err)
		}
		if r, err := e.m.Resolve(ctx, at); err != nil || r.Claimed {
			t.Errorf("%s resolved after the reads: %+v %v; want unclaimed", name, r, err)
		}
	}
	for _, dir := range []string{kbStore, "/src/proj/.todo-list", "/src/stray/.todo-list"} {
		if _, err := e.disk.FS.Stat(ctx, strings.TrimPrefix(dir, "/")); err == nil {
			t.Errorf("a read made %s", dir)
		}
	}
	if _, err := e.m.List(ctx, stray, todo.ListOpts{}); !is(err, todo.ErrNotAStore) {
		t.Errorf("the list of a root no one registered: %v; want not a store", err)
	}
	if _, err := e.m.Get(ctx, stray, "nope"); !is(err, todo.ErrNotAStore) {
		t.Errorf("the get of a root no one registered: %v; want not a store", err)
	}
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "first"}, ""); err != nil {
		t.Fatalf("the first task after the reads: %v", err)
	}
	if l, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil || len(l.Tasks) != 1 {
		t.Errorf("the store the first task made: %+v %v", l, err)
	}
}

// Priority medium is the value, normal its legacy spelling (ADR 1791532020 r14). A task written with
// normal is valid, with no error, and reads as medium; a refresh leaves its file byte for byte; its
// next write writes medium. A new task given normal is written medium, and the normal filter lists
// medium's tasks.
func TestPriorityNormalIsTheLegacySpellingOfMedium(t *testing.T) {
	e := claimed(t)
	hand := taskFile("old", "open", "priority: normal")
	e.disk.WriteFile(kbStore+"/open/old.md", []byte(hand))
	l, err := e.m.List(ctx, kb, todo.ListOpts{})
	if err != nil || len(l.Malformed) != 0 || len(l.Tasks) != 1 || l.Tasks[0].Priority != "medium" || len(l.Tasks[0].Errors) != 0 {
		t.Fatalf("a task written with normal: %+v %v; want it valid, read as medium", l, err)
	}
	if _, err := e.m.Refresh(ctx, kb); err != nil {
		t.Fatal(err)
	}
	if got := e.read(kbStore + "/open/old.md"); got != hand {
		t.Errorf("a refresh rewrote the file for its spelling:\n%s", got)
	}
	task := e.get("old")
	if _, err := e.m.Update(ctx, kb, "old", task.Rev, todo.Patch{"title": "Renamed"}); err != nil {
		t.Fatal(err)
	}
	if got := e.read(kbStore + "/open/old.md"); !strings.Contains(got, "\npriority: medium\n") || strings.Contains(got, "priority: normal") {
		t.Errorf("the task's next write kept normal:\n%s", got)
	}
	added, err := e.m.Add(ctx, kb, todo.Spec{Title: "Given normal", Priority: "normal"}, "")
	if err != nil || added.Priority != "medium" {
		t.Fatalf("a new task given normal: %+v %v; want medium", added, err)
	}
	if got := e.read(kbStore + "/open/" + added.ID + ".md"); !strings.Contains(got, "\npriority: medium\n") {
		t.Errorf("a new task given normal was written:\n%s", got)
	}
	if l, err := e.m.List(ctx, kb, todo.ListOpts{Priority: "normal"}); err != nil || len(l.Tasks) != 2 {
		t.Errorf("the normal filter: %d tasks, %v; want medium's two", len(l.Tasks), err)
	}
}

// A project that is not a workspace: Register, then Scaffold, which claims its store and makes the
// directory, its buckets and the marker. An unregistered root is never claimed nor written.
func TestNewProjectOrder(t *testing.T) {
	e := newEnv(t)
	e.disk.MkdirAll("/src/proj")
	at := todo.At{Root: "/src/proj"}
	if err := e.m.Claim(ctx, at); !is(err, todo.ErrNotWritable) {
		t.Errorf("claiming an unregistered root: %v", err)
	}
	if _, err := e.m.Scaffold(ctx, at); !is(err, todo.ErrNotWritable) {
		t.Errorf("scaffolding an unregistered root: %v", err)
	}
	if _, err := e.m.Add(ctx, at, todo.Spec{Title: "x"}, ""); !is(err, todo.ErrNotWritable) {
		t.Errorf("a task in an unregistered root: %v", err)
	}
	added, err := e.m.Register(ctx, "/src/proj")
	if err != nil || !added {
		t.Fatalf("register: %v %v", added, err)
	}
	if again, _ := e.m.Register(ctx, "/src/proj"); again {
		t.Error("registered twice")
	}
	o, err := e.m.Scaffold(ctx, at)
	if err != nil || !o.Scaffolded || o.Store != "/src/proj/.todo-list" {
		t.Fatalf("scaffold: %+v %v", o, err)
	}
	for _, b := range todo.Statuses {
		if fi, err := e.disk.FS.Stat(ctx, "src/proj/.todo-list/"+b); err != nil || !fi.IsDir() {
			t.Errorf("bucket %s", b)
		}
	}
	if !e.exists("/src/proj/.todo-list/.autodoc-owner") {
		t.Error("scaffold wrote no marker")
	}
	if r, err := e.m.Resolve(ctx, at); err != nil || !r.Claimed {
		t.Errorf("scaffolded, the store is not claimed: %+v %v", r, err)
	}
	if o, _ := e.m.Scaffold(ctx, at); o.Scaffolded {
		t.Error("a second scaffold made something")
	}
	if _, err := e.m.Add(ctx, at, todo.Spec{Title: "first"}, ""); err != nil {
		t.Error(err)
	}
	if _, err := e.m.Register(ctx, "/src/nope"); err == nil {
		t.Error("registered a missing directory")
	}
}

func TestWhere(t *testing.T) {
	e := claimed(t)
	byName, err := e.m.Resolve(ctx, kb)
	if err != nil {
		t.Fatal(err)
	}
	byRoot, err := e.m.Resolve(ctx, todo.At{Root: "/kb"})
	if err != nil || byRoot.StoreID != byName.StoreID || byRoot.Store != kbStore || !byRoot.Writable {
		t.Errorf("a workspace by name and by root: %+v %+v %v", byName, byRoot, err)
	}
	if _, err := e.m.Resolve(ctx, todo.At{Workspace: "nope"}); !is(err, todo.ErrNoWorkspace) {
		t.Errorf("unknown workspace: %v", err)
	}
	if _, err := e.m.Resolve(ctx, todo.At{Root: "relative"}); !is(err, todo.ErrInvalid) {
		t.Errorf("relative root: %v", err)
	}
	var at todo.At
	if err := at.DecodeParam(map[string]any{"root": "/kb"}); err != nil || at.Root != "/kb" {
		t.Errorf("decode {root}: %+v %v", at, err)
	}
	if err := at.DecodeParam("kb"); err != nil || at.Workspace != "kb" || at.ParamType() != "workspace | {root}" {
		t.Errorf("decode a name: %+v %v", at, err)
	}
	if err := at.DecodeParam(7); !is(err, todo.ErrInvalid) {
		t.Errorf("decode 7: %v", err)
	}
	// an override wins over <root>/.todo-list, is writable, and becomes its own store
	e.disk.MkdirAll("/shared/todos/open")
	r, err := e.m.SetDir(ctx, kb, "/shared/todos")
	if err != nil || r.Store != "/shared/todos" || r.Override != "/shared/todos" || !r.Writable || r.StoreID == byName.StoreID {
		t.Fatalf("set_dir: %+v %v", r, err)
	}
	if _, err := e.m.SetDir(ctx, kb, "/shared/missing"); !is(err, todo.ErrInvalid) {
		t.Errorf("set_dir to a missing directory: %v", err)
	}
	if kinds := e.eventKinds(); len(kinds) != 1 || kinds[0] != "todos.set_dir" {
		t.Errorf("set_dir is logged: %v", kinds)
	}
	if r, _ := e.m.SetDir(ctx, kb, ""); r.Store != kbStore || r.StoreID != byName.StoreID {
		t.Errorf("clearing the override: %+v", r)
	}
	stores, err := e.m.Stores(ctx)
	if err != nil || len(stores) != 2 {
		t.Fatalf("stores: %+v %v", stores, err)
	}
	if stores[0].Path != kbStore || len(stores[0].Roots) != 1 || stores[0].Roots[0] != "/kb" || !stores[0].Claimed {
		t.Errorf("stores: %+v", stores)
	}
}

// Writes stay inside the writable stores: an unregistered project's .todo-list is not one. A kb_root
// a client passes is ignored (ADR 1792015069): a store outside any KB has no $KB_ROOT until its own
// file names one, and it is never a write location.
func TestConfinement(t *testing.T) {
	e := claimed(t)
	e.disk.MkdirAll("/src/other/.todo-list/open")
	other := todo.At{Root: "/src/other"}
	if r, _ := e.m.Resolve(ctx, other); r.Writable {
		t.Error("an unregistered root's store is writable")
	}
	if err := e.m.Claim(ctx, other); !is(err, todo.ErrNotWritable) {
		t.Errorf("claim: %v", err)
	}
	if _, err := e.m.Open(ctx, other, todo.OpenOpts{KBRoot: "/kb"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if v := varOf(t, e, other, "KB_ROOT"); v.Value != "" {
		t.Errorf("a kb_root on open became the store's $KB_ROOT: %+v", v)
	}
	if l, err := e.m.List(ctx, other, todo.ListOpts{}); err != nil || len(l.Tasks) != 0 {
		t.Errorf("reading it is fine: %v", err)
	}
	// the store's own KB_ROOT: refs resolve there, nothing is written there
	e.must(func() error { _, err := e.m.Register(ctx, "/src/other"); return err }())
	e.must(e.m.Claim(ctx, other))
	e.must(e.m.SetKB(ctx, other, "kb"))
	task, err := e.m.Add(ctx, other, todo.Spec{Title: "t", ADR: []string{"/kb/adrs/x.md"}}, "")
	if err != nil || task.ADR[0] != "$KB_ROOT/adrs/x.md" || !e.exists("/src/other/.todo-list/open/"+task.ID+".md") ||
		e.exists("/kb/.todo-list/open/"+task.ID+".md") {
		t.Errorf("the store's KB_ROOT: %+v %v", task, err)
	}
	if _, err := e.m.Open(ctx, other, todo.OpenOpts{KBRoot: "relative"}); err != nil {
		t.Errorf("a kb_root is ignored, whatever it is: %v", err)
	}
	if _, err := e.m.Open(ctx, todo.At{Root: "/home/u"}, todo.OpenOpts{}); !is(err, todo.ErrNotAStore) {
		t.Errorf("open a directory with no store: %v", err)
	}
}

// On the real disk: a symlink in a store pointing outside it is never followed, a bucket that is
// a symlink is not read, and a .todo-list that is a symlink is not a writable store.
func TestSymlinksAreNotFollowed(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "ws")
	outside := filepath.Join(dir, "outside")
	for _, d := range []string{root + "/.todo-list/open", root + "/.todo-list/deferred", outside + "/bucket"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secret := taskFile("secret", "open")
	os.WriteFile(filepath.Join(outside, "secret.md"), []byte(secret), 0o644)
	os.WriteFile(filepath.Join(outside, "bucket", "x.md"), []byte(taskFile("x", "completed")), 0o644)
	os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, ".todo-list/open/secret.md"))
	os.Symlink(filepath.Join(outside, "bucket"), filepath.Join(root, ".todo-list/completed"))
	j := todotest.NewJournal()
	m := todo.New(todotest.Roots{"ws": root}, localDisk{}, j)
	at := todo.At{Workspace: "ws"}
	e := &env{t: t}
	e.must(m.Claim(ctx, at))
	l, err := m.List(ctx, at, todo.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Tasks) != 0 || len(l.Malformed) != 2 {
		t.Fatalf("%+v", l)
	}
	if _, err := m.Get(ctx, at, "secret"); !is(err, todo.ErrNotFound) {
		t.Errorf("get through a symlink: %v", err)
	}
	if _, err := m.Add(ctx, at, todo.Spec{Title: "t", ID: "secret"}, ""); !is(err, todo.ErrExists) {
		t.Errorf("a create over the symlink: %v (it must neither follow nor replace it)", err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret.md")); string(b) != secret {
		t.Error("the file outside the store changed")
	}
	// a .todo-list that is itself a symlink resolves elsewhere: not a writable store
	other := filepath.Join(dir, "ws2")
	os.MkdirAll(other, 0o755)
	os.Symlink(filepath.Join(root, ".todo-list"), filepath.Join(other, ".todo-list"))
	m.Register(ctx, other)
	if r, err := m.Resolve(ctx, todo.At{Root: other}); err != nil || r.Writable {
		t.Errorf("a symlinked .todo-list: %+v %v", r, err)
	}
}

// A hand edit that keeps the file's size and mtime is still caught, on the real disk: the rev and
// the vfs version (which carries ctime) both see it.
func TestSameSizeSameMtimeEditIsCaught(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".todo-list/open"), 0o755)
	m := todo.New(todotest.Roots{"ws": dir}, localDisk{}, todotest.NewJournal())
	at := todo.At{Workspace: "ws"}
	if err := m.Claim(ctx, at); err != nil {
		t.Fatal(err)
	}
	task, err := m.Add(ctx, at, todo.Spec{Title: "aaaa"}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".todo-list", task.Path)
	fi, _ := os.Stat(p)
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "# aaaa", "# bbbb", 1)), 0o644)
	os.Chtimes(p, fi.ModTime(), fi.ModTime())
	if _, err := m.Update(ctx, at, task.ID, task.Rev, todo.Patch{"title": "cccc"}); !is(err, todo.ErrStale) {
		t.Errorf("got %v", err)
	}
}

// MoveStore renames the directory and re-points the root; the store keeps its id, its claim, its
// snapshot and its consumers' cursors.
func TestMoveStoreKeepsTheStore(t *testing.T) {
	e := claimed(t)
	task := e.add(todo.Spec{Title: "t"})
	id := e.storeID()
	if _, err := e.m.Changes(ctx, kb, "auto-core", 10); err != nil {
		t.Fatal(err)
	}
	e.must(e.m.Ack(ctx, kb, "auto-core", 1))
	snap := mustSnapshot(t, e, id)
	e.disk.MkdirAll("/elsewhere/taken")
	if _, err := e.m.MoveStore(ctx, kb, "/elsewhere/taken"); !is(err, todo.ErrExists) {
		t.Errorf("onto an existing directory: %v", err)
	}
	if _, err := e.m.MoveStore(ctx, kb, "/kb/.todo-list/inside"); !is(err, todo.ErrInvalid) {
		t.Errorf("into itself: %v", err)
	}
	r, err := e.m.MoveStore(ctx, kb, "/elsewhere/todos")
	if err != nil {
		t.Fatal(err)
	}
	if r.StoreID != id || r.Store != "/elsewhere/todos" || r.Override != "/elsewhere/todos" || !r.Claimed || !r.Writable {
		t.Errorf("%+v", r)
	}
	if e.exists(kbStore+"/"+task.Path) || !e.exists("/elsewhere/todos/"+task.Path) {
		t.Error("the files did not move")
	}
	if got := mustSnapshot(t, e, id); len(got) != len(snap) {
		t.Error("the snapshot was lost")
	}
	if cs, _ := e.j.Consumers(ctx, id); len(cs) != 1 || cs[0].Acked != 1 {
		t.Errorf("the cursor was lost: %+v", cs)
	}
	got, err := e.m.Status(ctx, kb, task.ID, task.Rev, "completed")
	if err != nil || !e.exists("/elsewhere/todos/completed/"+task.ID+".md") {
		t.Errorf("a write after the move: %+v %v", got, err)
	}
	last := e.rows()[len(e.rows())-2]
	if last.Kind != todo.KindMoveStore || last.From != kbStore || last.To != "/elsewhere/todos" {
		t.Errorf("row %+v", last)
	}
}

// The default listing reads the active buckets only; it does not even list archived/. "archived"
// lists the months from their listings; a month is read on request, uncached; Get finds an
// archived task by id with stats alone.
func TestArchivesAreReadOnRequestOnly(t *testing.T) {
	e := claimed(t)
	e.add(todo.Spec{Title: "active"})
	for _, m := range []string{"2026/05", "2026/09", "2025/12"} {
		for i := range 3 {
			id := strings.ReplaceAll(m, "/", "-") + "-t" + string(rune('a'+i))
			e.disk.WriteFile(kbStore+"/archived/"+m+"/"+id+".md", []byte(strings.Replace(taskFile(id, "archived"),
				"archived_at: \"2026-10-01T09:00:00-07:00\"", "archived_at: \""+strings.ReplaceAll(m, "/", "-")+"-15T00:00:00Z\"", 1)))
		}
	}
	e.disk.SetFault(func(op, name string) error {
		if strings.Contains(name, "/archived") && (op == "readdir" || op == "read") {
			t.Errorf("the default listing touched %s (%s)", name, op)
		}
		return nil
	})
	if l, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil || len(l.Tasks) != 1 {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := e.m.Refresh(ctx, kb); err != nil {
		t.Fatal(err)
	}
	e.disk.SetFault(nil)
	e.disk.ResetReads()
	l, err := e.m.List(ctx, kb, todo.ListOpts{Buckets: []string{"archived"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []todo.Partition{{Month: "2026/09", Files: 3}, {Month: "2026/05", Files: 3}, {Month: "2025/12", Files: 3}}
	if len(l.Partitions) != 3 || l.Partitions[0] != want[0] || l.Partitions[2] != want[2] || len(l.Tasks) != 0 {
		t.Errorf("partitions %+v", l.Partitions)
	}
	if n := e.disk.Reads(kbStore + "/archived"); n != 0 {
		t.Errorf("listing the months read %d files", n)
	}
	l, err = e.m.List(ctx, kb, todo.ListOpts{Buckets: []string{"archived/2026/05"}})
	if err != nil || len(l.Tasks) != 3 || l.Tasks[0].Status != "archived" {
		t.Fatalf("a month: %+v %v", l, err)
	}
	e.disk.ResetReads()
	e.m.List(ctx, kb, todo.ListOpts{Buckets: []string{"archived/2026/05"}})
	if n := e.disk.Reads(kbStore + "/archived/2026/05"); n != 3 {
		t.Errorf("a month read again: %d reads, want 3 (archives are not cached)", n)
	}
	e.disk.ResetReads()
	task, err := e.m.Get(ctx, kb, "2025-12-tb")
	if err != nil || task.Path != "archived/2025/12/2025-12-tb.md" || e.disk.Reads(kbStore+"/archived") != 1 {
		t.Errorf("get an archived task: %+v %v (%d reads)", task, err, e.disk.Reads(kbStore+"/archived"))
	}
	// and it can be brought back
	back, err := e.m.Status(ctx, kb, task.ID, task.Rev, "open")
	if err != nil || back.Path != "open/2025-12-tb.md" || e.exists(kbStore+"/archived/2025/12/2025-12-tb.md") {
		t.Errorf("unarchive: %+v %v", back, err)
	}
}

// Thousands of archived tasks across many months: the default listing and a no-change refresh
// read none of them, and change none of their bytes or versions.
func TestArchivesAtScale(t *testing.T) {
	e := claimed(t)
	for i := range 20 {
		e.add(todo.Spec{Title: "active " + string(rune('a'+i))})
	}
	var archived []string
	for y := 2020; y <= 2025; y++ {
		for mo := 1; mo <= 12; mo++ {
			month := time.Date(y, time.Month(mo), 1, 0, 0, 0, 0, time.UTC).Format("2006/01")
			for i := range 40 {
				id := strings.ReplaceAll(month, "/", "-") + "-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
				p := kbStore + "/archived/" + month + "/" + id + ".md"
				e.disk.WriteFile(p, []byte(strings.Replace(taskFile(id, "archived"), "archived_at: \"2026-10-01T09:00:00-07:00\"",
					"archived_at: \""+strings.ReplaceAll(month, "/", "-")+"-15T00:00:00Z\"", 1)))
				archived = append(archived, p)
			}
		}
	}
	versions := map[string]string{}
	for _, p := range archived {
		versions[p] = string(e.disk.Version(p))
	}
	e.disk.ResetReads()
	if l, err := e.m.List(ctx, kb, todo.ListOpts{}); err != nil || len(l.Tasks) != 20 {
		t.Fatalf("%v", err)
	}
	sum, err := e.m.Refresh(ctx, kb)
	if err != nil || sum.Scanned != 20 || sum.Moved+sum.Rewritten+sum.Archived != 0 {
		t.Fatalf("refresh %+v %v", sum, err)
	}
	if n := e.disk.Reads(kbStore + "/archived"); n != 0 {
		t.Errorf("%d archived files read by the default listing and a refresh", n)
	}
	for _, p := range archived {
		if string(e.disk.Version(p)) != versions[p] {
			t.Fatalf("%s changed", p)
		}
	}
	t.Logf("%d archived tasks in %d months untouched", len(archived), 72)
}

// Reading never writes: listing, getting, the archive and a no-change refresh leave every file's
// bytes and version as they were, hand-edited ones included.
func TestReadingNeverWrites(t *testing.T) {
	e := claimed(t)
	e.add(todo.Spec{Title: "canonical", Tags: []string{"x"}})
	hand := "---\ntitle: hand\nid: hand\nversion: 1\nstatus: open\ncreated: 2026-10-01T09:00:00-07:00\n" +
		"updated: 2026-10-01T09:00:00-07:00\nstatus_changed: 2026-10-01T09:00:00-07:00\nassignee: null\n---\nno header, no h1\n\n\n"
	e.disk.WriteFile(kbStore+"/open/hand.md", []byte(hand))
	e.disk.WriteFile(kbStore+"/open/broken.md", []byte("---\nnope\n"))
	e.disk.WriteFile(kbStore+"/archived/2026/01/old.md", []byte(strings.Replace(taskFile("old", "archived"),
		"2026-10-01T09:00:00-07:00\"\n---", "2026-01-01T09:00:00-07:00\"\n---", 1)))
	e.m.Refresh(ctx, kb) // settles errors[] once; from here nothing changes
	files := map[string]string{}
	walk(t, e, "kb/.todo-list", func(p string) { files[p] = string(e.disk.Version("/" + p)) })
	e.restart()
	for range 2 {
		e.m.List(ctx, kb, todo.ListOpts{})
		e.m.List(ctx, kb, todo.ListOpts{Buckets: []string{"archived", "archived/2026/01"}})
		e.m.Get(ctx, kb, "hand")
		e.m.Get(ctx, kb, "old")
		if sum, err := e.m.Refresh(ctx, kb); err != nil || sum.Moved+sum.Rewritten+sum.Archived+sum.ErrorsSet != 0 {
			t.Errorf("a no-change refresh: %+v %v", sum, err)
		}
	}
	n := 0
	walk(t, e, "kb/.todo-list", func(p string) {
		n++
		if string(e.disk.Version("/"+p)) != files[p] {
			t.Errorf("%s changed", p)
		}
	})
	if n != len(files) || e.read(kbStore+"/open/hand.md") != hand {
		t.Errorf("files %d, was %d", n, len(files))
	}
}

func walk(t *testing.T, e *env, dir string, fn func(string)) {
	entries, err := e.disk.FS.ReadDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if en.IsDir() {
			walk(t, e, en.Path, fn)
		} else {
			fn(en.Path)
		}
	}
}

// Scaffold makes a store of the existing, empty directory SetDir chose: it has no buckets yet, so
// it is not a store until Scaffold creates them.
func TestScaffoldAnEmptyOverride(t *testing.T) {
	e := claimed(t)
	e.disk.MkdirAll("/plain")
	if _, err := e.m.SetDir(ctx, kb, "/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.List(ctx, kb, todo.ListOpts{}); !is(err, todo.ErrNotAStore) {
		t.Errorf("before the scaffold: %v", err)
	}
	e.must(e.m.Claim(ctx, kb))
	o, err := e.m.Scaffold(ctx, kb)
	if err != nil || !o.Scaffolded || !e.exists("/plain/.autodoc-owner") {
		t.Fatalf("scaffold: %+v %v", o, err)
	}
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "t"}, ""); err != nil {
		t.Error(err)
	}
}

// TestAClaimItCannotServeIsUndone: only a store with no directory or no buckets yet is left to
// Scaffold; a store the daemon cannot open or mark fails the claim, and a new claim is undone so
// the cutover can retry. A claim made before stays.
func TestAClaimItCannotServeIsUndone(t *testing.T) {
	claimedNow := func(e *env) bool {
		r, err := e.m.Resolve(ctx, kb)
		if err != nil {
			t.Fatal(err)
		}
		return r.Claimed
	}
	// no directory yet: deferred, claimed
	e := newEnv(t)
	if err := e.m.Claim(ctx, kb); err != nil || !claimedNow(e) {
		t.Fatalf("a store with no directory: %v, claimed %v", err, claimedNow(e))
	}
	// the store's path is a file: it cannot be opened, so the claim fails and is undone
	e = newEnv(t)
	e.disk.WriteFile(kbStore, []byte("not a directory\n"))
	if err := e.m.Claim(ctx, kb); err == nil || claimedNow(e) {
		t.Errorf("a store that cannot be opened: %v, claimed %v", err, claimedNow(e))
	}
	// the marker cannot be written: the claim fails and is undone; once the disk is well it succeeds
	e = newEnv(t)
	e.disk.WriteFile(kbStore+"/open/t.md", []byte(taskFile("t", "open")))
	e.disk.SetFault(func(op, name string) error {
		if strings.HasSuffix(name, ".autodoc-owner") && op != "stat" && op != "read" {
			return errors.New("disk full")
		}
		return nil
	})
	if err := e.m.Claim(ctx, kb); err == nil || claimedNow(e) {
		t.Errorf("a marker that cannot be written: %v, claimed %v", err, claimedNow(e))
	}
	e.disk.SetFault(nil)
	if err := e.m.Claim(ctx, kb); err != nil || !claimedNow(e) || !e.exists(kbStore+"/.autodoc-owner") {
		t.Errorf("the retry: %v, claimed %v", err, claimedNow(e))
	}
	// a claim made before stays when a later one fails
	e.disk.SetFault(func(op, name string) error {
		if strings.HasSuffix(name, ".autodoc-owner") && op != "stat" && op != "read" {
			return errors.New("disk full")
		}
		return nil
	})
	e.restart() // a new daemon: the store is opened again, and its marker must be written again
	if err := e.disk.FS.Remove(ctx, "kb/.todo-list/.autodoc-owner"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Claim(ctx, kb); err == nil || !claimedNow(e) {
		t.Errorf("a failed re-claim: %v, claimed %v (the earlier claim stays)", err, claimedNow(e))
	}
}

// releaseMidway is a journal that runs between once, right after the first Settings it reads
// returns: a Release landing between a write's snapshot of the store and its claim.
type releaseMidway struct {
	todo.Journal
	once    atomic.Bool
	between func()
}

func (j *releaseMidway) Settings(ctx context.Context) (todo.Settings, error) {
	s, err := j.Journal.Settings(ctx)
	if err == nil && j.once.CompareAndSwap(false, true) {
		j.between()
	}
	return s, err
}

// A write's own claim is decided in the claim's transaction, against the record it reads there: a
// Release that lands after the write read the store unclaimed, and before it claims, wins. The
// write is refused, the store stays released, and no marker is written.
func TestAWritesClaimLeavesAReleaseMadeMeanwhile(t *testing.T) {
	e := newEnv(t)
	e.disk.WriteFile(kbStore+"/open/t.md", []byte(taskFile("t", "open")))
	if r, err := e.m.Resolve(ctx, kb); err != nil || r.Claimed || r.Released {
		t.Fatalf("the fixture: %+v %v; want a store never claimed", r, err)
	}
	j := &releaseMidway{Journal: e.j}
	m := todo.New(e.roots, e.disk, j)
	released := false
	j.between = func() {
		e.must(m.Release(ctx, kb))
		released = true
	}
	_, err := m.Add(ctx, kb, todo.Spec{Title: "a write meeting a release"}, "")
	if !released {
		t.Fatal("the release never landed between the write's read and its claim")
	}
	r, rerr := m.Resolve(ctx, kb)
	if rerr != nil || r.Claimed || !r.Released || !is(err, todo.ErrUnclaimed) {
		t.Fatalf("the write: %v; the store: %+v %v; want the write refused and the store still released", err, r, rerr)
	}
	if e.exists(kbStore + "/.autodoc-owner") {
		t.Error("a marker on a store still released")
	}
}

// A claim of a released store that fails leaves no trace: the store is put back as it was, released,
// so after a restart it is still released, and a write is still refused rather than claiming it.
func TestAFailedClaimOfAReleasedStoreKeepsItReleased(t *testing.T) {
	e := claimed(t)
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "kept"}, ""); err != nil {
		t.Fatal(err)
	}
	e.must(e.m.Release(ctx, kb))
	e.disk.SetFault(func(op, name string) error {
		if strings.HasSuffix(name, ".autodoc-owner") && op != "stat" && op != "read" {
			return errors.New("disk full")
		}
		return nil
	})
	if err := e.m.Claim(ctx, kb); err == nil {
		t.Fatal("a claim whose marker cannot be written succeeded")
	}
	e.disk.SetFault(nil)
	e.restart()
	if r, err := e.m.Resolve(ctx, kb); err != nil || r.Claimed || !r.Released {
		t.Fatalf("after the failed claim and a restart: %+v %v; want released as before", r, err)
	}
	if _, err := e.m.Add(ctx, kb, todo.Spec{Title: "x"}, ""); !is(err, todo.ErrUnclaimed) {
		t.Fatalf("a write to the store still released: %v; want it refused", err)
	}
}

// TestAFailedClaimIsUndoneWhateverItsRequest: a request cancelled after its claim was recorded is
// still undone, on a context of the undo's own; and of two claims racing on one store, one
// failing, the undo never clears the other's claim: claims are one at a time.
func TestAFailedClaimIsUndoneWhateverItsRequest(t *testing.T) {
	claimedNow := func(e *env) bool {
		r, err := e.m.Resolve(ctx, kb)
		if err != nil {
			t.Fatal(err)
		}
		return r.Claimed
	}
	marker := func(op, name string) bool {
		return strings.HasSuffix(name, ".autodoc-owner") && op != "stat" && op != "read"
	}
	// cancelled while marking: the claim is undone all the same
	e := newEnv(t)
	e.disk.WriteFile(kbStore+"/open/t.md", []byte(taskFile("t", "open")))
	cctx, cancel := context.WithCancel(ctx)
	e.disk.SetFault(func(op, name string) error {
		if marker(op, name) {
			cancel()
			return context.Canceled
		}
		return nil
	})
	if err := e.m.Claim(cctx, kb); err == nil || claimedNow(e) {
		t.Errorf("a claim cancelled while marking: %v, claimed %v", err, claimedNow(e))
	}
	// two claims at once: the first fails while the second waits; the second then claims
	e = newEnv(t)
	e.disk.WriteFile(kbStore+"/open/t.md", []byte(taskFile("t", "open")))
	var writes atomic.Int32
	inside, release := make(chan struct{}), make(chan struct{})
	e.disk.SetFault(func(op, name string) error {
		if marker(op, name) && writes.Add(1) == 1 {
			close(inside)
			<-release
			return errors.New("disk full")
		}
		return nil
	})
	first := make(chan error, 1)
	go func() { first <- e.m.Claim(ctx, kb) }()
	<-inside
	second := make(chan error, 1)
	go func() { second <- e.m.Claim(ctx, kb) }()
	select {
	case err := <-second:
		t.Fatalf("the second claim did not wait for the first: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-first; err == nil {
		t.Error("the first claim, whose marker failed, succeeded")
	}
	if err := <-second; err != nil || !claimedNow(e) {
		t.Errorf("the second claim: %v, claimed %v", err, claimedNow(e))
	}
}

// CheckFile judges a task by its todo format, never a KB's schema (ADR 1792015212 amendment 2): a
// valid task has no diagnostic and its format's facets, normal faceted as medium; a malformed one
// has the reason todos.list gives; each errors[] entry is a diagnostic on the field it names.
func TestCheckFileJudgesATaskByItsFormat(t *testing.T) {
	ok := todo.CheckFile([]byte(taskFile("t", "open", "priority: normal")))
	if len(ok.Diagnostics) != 0 {
		t.Errorf("a valid task: %+v, want no diagnostic", ok.Diagnostics)
	}
	facets := map[string]string{}
	for _, f := range ok.Facets {
		facets[f.Field] = f.Value
	}
	if facets["status"] != "open" || facets["priority"] != "medium" || facets["type"] != "" {
		t.Errorf("a valid task's facets: %v, want status open and priority medium, and no KB type", facets)
	}
	bad := todo.CheckFile([]byte("---\nid: \"t\"\nversion: 1\nstatus: open\n---\n\n# t\n"))
	if len(bad.Diagnostics) != 1 || bad.Diagnostics[0].Rule != todo.RuleMalformed || !strings.Contains(bad.Diagnostics[0].Message, "title") {
		t.Errorf("a task with no title: %+v, want one %s naming title", bad.Diagnostics, todo.RuleMalformed)
	}
	withErr := todo.CheckFile([]byte(taskFile("t", "open", "adr:\n  - \"/kb/adrs/gone.md\"",
		"errors:\n  - code: not-found\n    detected: \"2026-10-01T09:00:00-07:00\"\n    field: \"adr[0]\"\n    message: \"adr[0]: no such file\"")))
	if len(withErr.Diagnostics) != 1 || withErr.Diagnostics[0].Field != "adr[0]" || withErr.Diagnostics[0].Rule != todo.RuleTaskError+"not-found" {
		t.Errorf("a task with an errors[] entry: %+v, want one %snot-found on adr[0]", withErr.Diagnostics, todo.RuleTaskError)
	}
}
