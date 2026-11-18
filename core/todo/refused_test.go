package todo_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/todo"
)

// catalogued is the todos.* catalog's name for err, as the daemon maps a verb's error (the
// module's errors, first match): "" when the catalog lists none of it.
func catalogued(m *todo.Manager, err error) string {
	for _, e := range m.Errors() {
		if errors.Is(err, e.Err) {
			return e.Name
		}
	}
	return ""
}

// A task file the documents refuse to write is todos.invalid, which the todos.* catalog lists,
// naming the file: one over the size limit (a description too long), on an add and on an update,
// and one whose name the disk refuses (vfs.ErrInvalidName, as Windows refuses a name it
// reserves). The write changes nothing: the task is as it was, and no other file appears. The mutant, named here:
// settle passing the documents' error through (the daemon then answers -32602 with a message the
// catalog does not list).
func TestAWriteTheDocumentsRefuseIsInvalid(t *testing.T) {
	e := claimed(t)
	call := func(name string, params ...any) (any, error) {
		return verb(t, e.m, name).Call(context.Background(), params)
	}
	big := strings.Repeat("a long description ", docs.MaxSize/19+1)
	_, err := call("todos.add", "kb", map[string]any{"title": "Too long", "description": big})
	if name := catalogued(e.m, err); !errors.Is(err, todo.ErrInvalid) || !errors.Is(err, docs.ErrTooLarge) || name != "todos.invalid" ||
		!strings.Contains(err.Error(), "too-long.md") {
		t.Fatalf("a task over the size limit: %v (catalogued %q); want todos.invalid naming its file", err, name)
	}
	if out, err := call("todos.list", "kb"); err != nil || len(out.(todo.Listing).Tasks) != 0 {
		t.Fatalf("after the refused add: %v, %v; want no task", out, err)
	}

	out, err := call("todos.add", "kb", map[string]any{"title": "Grows"})
	if err != nil {
		t.Fatal(err)
	}
	grows := out.(todo.Assigned).Task
	_, err = call("todos.update", "kb", grows.ID, grows.Rev, map[string]any{"description": big})
	if !errors.Is(err, todo.ErrInvalid) || catalogued(e.m, err) != "todos.invalid" {
		t.Fatalf("an update past the size limit: %v; want todos.invalid", err)
	}
	if out, err := call("todos.get", "kb", grows.ID); err != nil || out.(todo.Task).Rev != grows.Rev {
		t.Fatalf("after the refused update: %v, %v; want the task as it was", out, err)
	}

	// the disk refuses the name, as Windows refuses one it reserves (aux), before anything is written
	e.disk.SetFault(func(op, name string) error {
		if (op == "write" || op == "create" || op == "rename") && strings.HasSuffix(name, "-aux.md") {
			return &fs.PathError{Op: op, Path: name, Err: vfs.ErrInvalidName}
		}
		return nil
	})
	t.Cleanup(func() { e.disk.SetFault(nil) })
	_, err = call("todos.add", "kb", map[string]any{"title": "Aux"})
	if !errors.Is(err, todo.ErrInvalid) || !errors.Is(err, vfs.ErrInvalidName) || catalogued(e.m, err) != "todos.invalid" ||
		!strings.Contains(err.Error(), "aux.md") {
		t.Fatalf("a task whose name the disk refuses: %v; want todos.invalid naming its file", err)
	}
	if out, err := call("todos.list", "kb"); err != nil || len(out.(todo.Listing).Tasks) != 1 {
		t.Fatalf("after the refused name: %v, %v; want the one task there was", out, err)
	}
}
