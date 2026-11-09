package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// taskpath_test.go: a task's file is written at one path
// (docs/decisions/todos.md#a-tasks-file-is-written-at-one-path), on a daemon serving a workspace
// with a todo store, its task moved to in-progress by an assignment.

// movedTask is a daemon serving kb, a task of id move-me assigned there (so moved to
// in-progress/move-me.md), a client, and the task's file as it was in open/ before the move.
func movedTask(t *testing.T) (kb string, cli *golibrpc.Client, stale []byte) {
	t.Helper()
	dir := short(t)
	state, sock := filepath.Join(dir, "state"), ipc.ScratchAddr(dir, "s.sock")
	kb = mustEval(t, t.TempDir())
	start(t, writeConfig(t, sock, state, "", "kb="+kb), sock)
	cli = dial(t, sock)
	task := call(t, cli, "todos.add", "kb", map[string]any{"title": "Move me", "id": "move-me"}).(map[string]any)
	if task["path"] != "open/move-me.md" {
		t.Fatalf("the task is at %v, want open/move-me.md", task["path"])
	}
	stale, err := os.ReadFile(filepath.Join(kb, ".todo-list", "open", "move-me.md"))
	if err != nil {
		t.Fatal(err)
	}
	moved := call(t, cli, "todos.assign", "kb", "move-me", task["rev"], "autodoc:one").(map[string]any)
	if moved["path"] != "in-progress/move-me.md" {
		t.Fatalf("the assigned task is at %v, want in-progress/move-me.md", moved["path"])
	}
	return kb, cli, stale
}

// withID is a task file's text with its frontmatter id replaced.
func withID(src []byte, id string) []byte {
	return regexp.MustCompile(`(?m)^id: .*$`).ReplaceAll(src, []byte(`id: "`+id+`"`))
}

// callCode is a call's error code, 0 when it succeeded, and its message.
func callCode(cli *golibrpc.Client, method string, params ...any) (int64, string) {
	_, err := cli.Call(context.Background(), method, params...)
	var re *golibrpc.Error
	if errors.As(err, &re) {
		return re.Code, re.Message
	}
	if err != nil {
		return -1, err.Error()
	}
	return 0, ""
}

// TestAWriteThatWouldMakeASecondFileOfATaskIsRefused: a client writing open/move-me.md again from
// its view before the move, a create (version ""), is refused with a conflict naming the task and
// where it is, through doc.write and file.write alike; nothing is written in open/.
func TestAWriteThatWouldMakeASecondFileOfATaskIsRefused(t *testing.T) {
	kb, cli, stale := movedTask(t)
	old := filepath.Join(kb, ".todo-list", "open", "move-me.md")
	for _, w := range []struct {
		verb   string
		params []any
	}{
		{"doc.write", []any{"kb", ".todo-list/open/move-me.md", stale, ""}},
		{"doc.write_raw", []any{"kb", ".todo-list/open/move-me.md", stale, ""}},
		{"file.write", []any{old, stale, ""}},
	} {
		code, msg := callCode(cli, w.verb, w.params...)
		if code != rpc.CodeConflict || !strings.Contains(msg, "move-me") || !strings.Contains(msg, "in-progress/move-me.md") {
			t.Errorf("%s of open/move-me.md: code %d %q; want a conflict naming the task and in-progress/move-me.md", w.verb, code, msg)
		}
		if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("after %s: open/move-me.md is on disk (%v): a second file of the task", w.verb, err)
		}
	}
}

// TestASaveOfATasksOwnFileIsWritten: the task's own file, written at the version read, is a save
// like any other.
func TestASaveOfATasksOwnFileIsWritten(t *testing.T) {
	_, cli, _ := movedTask(t)
	got := call(t, cli, "doc.read", "kb", ".todo-list/in-progress/move-me.md").(map[string]any)
	content, _ := got["content"].([]byte)
	edited := append(content, []byte("\nA line of my own.\n")...)
	if code, msg := callCode(cli, "doc.write", "kb", ".todo-list/in-progress/move-me.md", edited, got["version"]); code != 0 {
		t.Fatalf("a save of the task's own file: code %d %q", code, msg)
	}
}

// TestACreateOfAnotherIDOrOutsideTheStoreIsWritten: a new task's file, a file in a folder named
// like a bucket outside the store, and an id differing only in case (another id: ids are compared
// byte for byte) are each written.
func TestACreateOfAnotherIDOrOutsideTheStoreIsWritten(t *testing.T) {
	kb, cli, stale := movedTask(t)
	for _, w := range []struct{ what, path, id string }{
		{"a new id", ".todo-list/open/fresh.md", "fresh"},
		{"outside the store", "notes/open/move-me.md", "move-me"},
		{"another case", ".todo-list/open/MOVE-ME.md", "MOVE-ME"},
	} {
		if code, msg := callCode(cli, "doc.write", "kb", w.path, withID(stale, w.id), ""); code != 0 {
			t.Errorf("%s (%s): code %d %q, want it written", w.what, w.path, code, msg)
			continue
		}
		if _, err := os.Stat(filepath.Join(kb, filepath.FromSlash(w.path))); err != nil {
			t.Errorf("%s: %s not on disk: %v", w.what, w.path, err)
		}
	}
}
