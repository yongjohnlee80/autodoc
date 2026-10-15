package todo_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

var ctx = context.Background()

// kbAgents is a KB's AGENTS.md: its frontmatter's type: kb is what makes a directory a KB.
const kbAgents = "---\ntype: kb\nkind: agents\n---\n\n# kb\n"

// kb is the workspace every env has: /kb, a KB (its AGENTS.md says type: kb), so $KB_ROOT is its
// root too.
var kb = todo.At{Workspace: "kb"}

const kbStore = "/kb/.todo-list"

// env is a daemon's todo system over an in-memory disk and journal, with a settable clock.
type env struct {
	t      *testing.T
	disk   *todotest.Disk
	j      *todotest.Faulty
	roots  todotest.Roots
	mu     sync.Mutex
	now    time.Time
	m      *todo.Manager
	events []todo.Event
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, disk: todotest.NewDisk(), j: todotest.NewFaulty(newJournal(t)), roots: todotest.Roots{"kb": "/kb"},
		now: time.Date(2026, 10, 11, 10, 0, 0, 0, time.FixedZone("PDT", -7*3600))}
	e.disk.WriteFile("/kb/AGENTS.md", []byte(kbAgents))
	e.disk.MkdirAll("/home/u")
	e.restart()
	return e
}

// claimed is newEnv with the kb store claimed and scaffolded.
func claimed(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.must(e.m.Claim(ctx, kb))
	if _, err := e.m.Scaffold(ctx, kb); err != nil {
		t.Fatal(err)
	}
	return e
}

// restart is a new daemon over the same disk and journal: what a crash and a restart leave.
func (e *env) restart() {
	e.m = todo.New(e.roots, e.disk, e.j,
		todo.WithClock(func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }),
		todo.WithHome("/home/u"),
		todo.WithEvents(func(ev todo.Event) { e.mu.Lock(); defer e.mu.Unlock(); e.events = append(e.events, ev) }))
}

func (e *env) advance(d time.Duration) { e.mu.Lock(); e.now = e.now.Add(d); e.mu.Unlock() }

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) add(spec todo.Spec) todo.Task {
	e.t.Helper()
	task, err := e.m.Add(ctx, kb, spec)
	if err != nil {
		e.t.Fatalf("add %q: %v", spec.Title, err)
	}
	return task
}

func (e *env) get(id string) todo.Task {
	e.t.Helper()
	task, err := e.m.Get(ctx, kb, id)
	if err != nil {
		e.t.Fatalf("get %s: %v", id, err)
	}
	return task
}

func (e *env) read(p string) string {
	e.t.Helper()
	b, err := e.disk.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func (e *env) exists(p string) bool { _, err := e.disk.ReadFile(p); return err == nil }

func (e *env) storeID() string {
	e.t.Helper()
	r, err := e.m.Resolve(ctx, kb)
	if err != nil {
		e.t.Fatal(err)
	}
	return r.StoreID
}

func (e *env) rows() []todo.Change { return e.j.Rows(e.storeID()) }

func (e *env) pending() []todo.Change {
	var out []todo.Change
	for _, c := range e.rows() {
		if c.State != todo.StateCommitted {
			out = append(out, c)
		}
	}
	return out
}

func (e *env) eventKinds() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.events {
		out = append(out, ev.Kind)
	}
	return out
}

// taskFile is a valid v1 task file written as auto-core writes one, for setting a store up by hand.
func taskFile(id, status string, extra ...string) string {
	ts := "2026-10-01T09:00:00-07:00"
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %q\nversion: 1\nstatus: %s\ntitle: %q\n", id, status, "Task "+id)
	for _, x := range extra {
		b.WriteString(x + "\n")
	}
	fmt.Fprintf(&b, "created: %q\nupdated: %q\nstatus_changed: %q\n", ts, ts, ts)
	switch status {
	case "completed":
		fmt.Fprintf(&b, "completed_at: %q\n", ts)
	case "archived":
		fmt.Fprintf(&b, "archived_at: %q\n", ts)
	}
	b.WriteString("---\n\n# Task " + id + "\n\nBody of " + id + ".\n")
	return b.String()
}

// localDisk is todo.Filesystems over the real disk, through golib's vfs/local: for the tests that
// need real symlinks, ctime and the parity run. Only tests touch the disk directly.
type localDisk struct{}

func (localDisk) Open(_ context.Context, dir string) (vfs.FS, string, error) {
	canon, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, "", err
	}
	fi, err := os.Stat(canon)
	if err != nil {
		return nil, "", err
	}
	if !fi.IsDir() {
		return nil, "", fmt.Errorf("%s: not a directory", dir)
	}
	fsys, err := local.New(canon)
	return fsys, canon, err
}

func is(err, target error) bool { return errors.Is(err, target) }
