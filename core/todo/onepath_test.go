package todo_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// errExists is a create of a file that is there, as a client's exclusive create answers.
var errExists = errors.New("exists")

// createAt is a client's exclusive create of content at abs on e's disk, once release is closed:
// entered is closed as it starts, so a cell can act inside its window.
func (e *env) createAt(abs string, content []byte, entered, release chan struct{}) func() error {
	return func() error {
		close(entered)
		<-release
		if _, err := e.disk.ReadFile(abs); err == nil {
			return errExists
		}
		e.disk.WriteFile(abs, content)
		return nil
	}
}

// TestAClientsCreateIsSerializedWithTheStore (docs/decisions/todos.md#a-tasks-file-is-written-at-one-path):
// a client's create of a task file is checked and written under the lock the store's own moves
// take. A move of the task asked inside a create's window waits for it: the create, checked with
// the task still at its path, then finds the file there, and the move moves the task's one file. A
// second create of the same new id at another path, asked inside the first's window, waits too,
// and is refused once the first is written. The mutant (the check and the write outside the lock):
// the move runs inside the window and the create writes the old path again, two files of one
// task; both creates are written.
func TestAClientsCreateIsSerializedWithTheStore(t *testing.T) {
	e := claimed(t)
	a := e.add(todo.Spec{Title: "a"})
	abs := kbStore + "/" + a.Path
	content, err := e.disk.ReadFile(abs)
	e.must(err)
	entered, release := make(chan struct{}), make(chan struct{})
	once := &sync.Once{}
	open := func() { once.Do(func() { close(release) }) }
	t.Cleanup(open)
	created, moved := make(chan error, 1), make(chan error, 1)
	go func() {
		_, at, err := e.m.CreateOnce(ctx, abs, content, e.createAt(abs, content, entered, release))
		if at != "" {
			err = errors.New("refused as a second file of the task at " + at)
		}
		created <- err
	}()
	<-entered
	go func() { _, err := e.m.Status(ctx, kb, a.ID, a.Rev, "completed"); moved <- err }()
	select {
	case err := <-moved:
		t.Fatalf("the move ran inside the create's window (%v): the check and the write are not under the store's lock", err)
	case <-time.After(300 * time.Millisecond):
	}
	open()
	if err := <-created; !errors.Is(err, errExists) {
		t.Fatalf("the create, checked with the task at its path: %v, want it to find the file there", err)
	}
	e.must(<-moved)
	if _, err := e.disk.ReadFile(abs); err == nil {
		t.Fatalf("%s is there after the move: two files of task %s", abs, a.ID)
	}
	if _, err := e.disk.ReadFile(kbStore + "/completed/" + a.ID + ".md"); err != nil {
		t.Fatalf("the moved file: %v", err)
	}

	// two creates of one new id at two paths: the second waits for the first, and is refused
	fresh := []byte(taskFile("fresh", "open"))
	first, second := kbStore+"/open/fresh.md", kbStore+"/deferred/fresh.md"
	entered, release, once = make(chan struct{}), make(chan struct{}), &sync.Once{}
	firstDone, secondDone := make(chan error, 1), make(chan string, 1)
	go func() {
		_, _, err := e.m.CreateOnce(ctx, first, fresh, e.createAt(first, fresh, entered, release))
		firstDone <- err
	}()
	<-entered
	go func() {
		_, at, err := e.m.CreateOnce(ctx, second, fresh, func() error { e.disk.WriteFile(second, fresh); return nil })
		if err != nil {
			at = "error: " + err.Error()
		}
		secondDone <- at
	}()
	select {
	case at := <-secondDone:
		t.Fatalf("the second create ran inside the first's window (refused for %q): not under the store's lock", at)
	case <-time.After(300 * time.Millisecond):
	}
	open()
	e.must(<-firstDone)
	if at := <-secondDone; at != "open/fresh.md" {
		t.Fatalf("the second create of fresh: refused for %q, want refused, fresh being at open/fresh.md", at)
	}
	if _, err := e.disk.ReadFile(second); err == nil {
		t.Fatalf("%s was written: two files of task fresh", second)
	}
}
