package todo_test

import (
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// waitingForTheStore is a goroutine waiting for a store's lock in openLocked.
var waitingForTheStore = regexp.MustCompile(`sync\.\(\*Mutex\)\.Lock\(.*\n.*\n.*core/todo\.\(\*Manager\)\.openLocked\(`)

// blockedOnTheStore reports whether the goroutine running fn (a test function of this file, named in
// its stack) is waiting for a store's lock: true once it is, false if it finished first. It reads
// the goroutines' stacks until one or the other, so it waits for a state, never for a time.
func blockedOnTheStore(t *testing.T, fn string, done <-chan struct{}) bool {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // a hang's bound, not a wait for anything
	buf := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return false
		default:
		}
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "core/todo_test."+fn+"(") && waitingForTheStore.MatchString(g) {
				return true
			}
		}
		runtime.Gosched()
	}
	t.Fatalf("%s neither waited for the store nor finished", fn)
	return false
}

// listing lists ws, for the probes: its name is the goroutine's mark.
func listing(m *todo.Manager, ws todo.At, out *[]string, done chan<- struct{}) {
	defer close(done)
	if l, err := m.List(ctx, ws, todo.ListOpts{}); err == nil {
		for _, task := range l.Tasks {
			*out = append(*out, task.ID)
		}
	}
}

// closing closes m, for the probes.
func closing(m *todo.Manager, done chan<- struct{}) {
	defer close(done)
	m.Close()
}

// moving moves ws's store to to, for the probes.
func moving(m *todo.Manager, ws todo.At, to string, r *todo.Resolved, err *error, done chan<- struct{}) {
	defer close(done)
	*r, *err = m.MoveStore(ctx, ws, to)
}

// Close while an operation is inside a store: Close waits for it, and an operation asked for
// meanwhile waits too, then runs on the store opened again, after the first; no operation ever
// reaches the disk while another holds the store; and nothing is held once the last Close returns.
func TestCloseWaitsForTheStoresUseAndAdmitsNoSecondOne(t *testing.T) {
	disk := todotest.NewDisk()
	disk.MkdirAll("/ws")
	m := todo.New(todotest.Roots{"ws": "/ws"}, disk, todotest.NewJournal())
	ws := todo.At{Workspace: "ws"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatal(err)
	}
	parked, release := make(chan struct{}), make(chan struct{})
	var holding atomic.Bool
	var overlaps atomic.Int32
	disk.SetFault(func(op, name string) error {
		if (op == "create" || op == "write") && strings.HasSuffix(name, "/a.md") && !holding.Load() {
			holding.Store(true)
			close(parked)
			<-release // inside the store's lock: the operation A
			holding.Store(false)
			return nil
		}
		if holding.Load() {
			overlaps.Add(1) // another operation reached the disk while A held the store
		}
		return nil
	})
	aDone := make(chan struct{})
	go func() { defer close(aDone); m.Add(ctx, ws, todo.Spec{Title: "a", ID: "a"}, "") }()
	<-parked
	closeDone := make(chan struct{})
	go closing(m, closeDone)
	if !blockedOnStoreOrClose(t, closeDone) {
		t.Fatal("Close returned while an operation was inside the store")
	}
	var listed []string
	bDone := make(chan struct{})
	go listing(m, ws, &listed, bDone)
	if !blockedOnTheStore(t, "listing", bDone) {
		t.Fatal("an operation asked for during Close ran while another held the store: a second object for one store")
	}
	close(release)
	<-aDone
	<-closeDone
	<-bDone
	disk.SetFault(nil)
	if !slices.Contains(listed, "a") {
		t.Errorf("the second operation listed %v: it ran before the first ended", listed)
	}
	if n := overlaps.Load(); n != 0 {
		t.Errorf("%d disk operations while another operation held the store", n)
	}
	m.Close()
	if held := disk.Held(); len(held) != 0 {
		t.Errorf("after Close, still held: %v", held)
	}
}

// blockedOnStoreOrClose reports whether Close is waiting for a store's lock in Close itself.
func blockedOnStoreOrClose(t *testing.T, done <-chan struct{}) bool {
	t.Helper()
	inClose := regexp.MustCompile(`sync\.\(\*Mutex\)\.Lock\(.*\n.*\n.*core/todo\.\(\*Manager\)\.Close\(`)
	deadline := time.Now().Add(30 * time.Second)
	buf := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return false
		default:
		}
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "core/todo_test.closing(") && inClose.MatchString(g) {
				return true
			}
		}
		runtime.Gosched()
	}
	t.Fatal("Close neither waited for the store nor returned")
	return false
}

// A move holds its store from the release of its directory to the end: an operation asked for
// while the directory is being renamed waits, then runs on the store at its new path.
func TestAMoveAdmitsNoOperationUntilItEnds(t *testing.T) {
	disk := todotest.NewDisk()
	disk.MkdirAll("/ws")
	m := todo.New(todotest.Roots{"ws": "/ws"}, disk, todotest.NewJournal())
	t.Cleanup(func() { m.Close() })
	ws := todo.At{Workspace: "ws"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(ctx, ws, todo.Spec{Title: "a", ID: "a"}, ""); err != nil {
		t.Fatal(err)
	}
	parked, release := make(chan struct{}), make(chan struct{})
	var holding atomic.Bool
	var overlaps atomic.Int32
	disk.SetFault(func(op, name string) error {
		if op == "rename" && name == "/ws/.todo-list" && !holding.Load() {
			holding.Store(true)
			close(parked)
			<-release // the move, its store's directory released, about to rename it
			holding.Store(false)
			return nil
		}
		if holding.Load() {
			overlaps.Add(1)
		}
		return nil
	})
	var moved todo.Resolved
	var moveErr error
	moveDone := make(chan struct{})
	go moving(m, ws, "/ws/tasks", &moved, &moveErr, moveDone)
	<-parked
	var listed []string
	bDone := make(chan struct{})
	go listing(m, ws, &listed, bDone)
	if !blockedOnTheStore(t, "listing", bDone) {
		t.Fatal("an operation asked for during the move ran before it ended: the store was admitted at its old path")
	}
	close(release)
	<-moveDone
	<-bDone
	disk.SetFault(nil)
	if moveErr != nil || moved.Store != "/ws/tasks" {
		t.Fatalf("the move: %+v, %v", moved, moveErr)
	}
	if !slices.Contains(listed, "a") {
		t.Errorf("after the move, the operation listed %v", listed)
	}
	if n := overlaps.Load(); n != 0 {
		t.Errorf("%d disk operations while the move held the store", n)
	}
	if held := disk.Held(); held["/ws/.todo-list"] != 0 || held["/ws/tasks"] != 1 {
		t.Errorf("held %v: want the store open at its new path only", held)
	}
}
