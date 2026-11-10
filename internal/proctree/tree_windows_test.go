//go:build windows

package proctree

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The root runs only once it is in the job: held a second between its start and its assignment
// (afterStart), a root that ran would have started its child outside the job by then. It has not,
// so the child it starts at once is in the job, and the kill ends it too.
func TestAChildStartedAtOnceIsInTheTree(t *testing.T) {
	const name = "TestAChildStartedAtOnceIsInTheTree"
	treeHelper(name)
	afterStart = func() { time.Sleep(time.Second) }
	t.Cleanup(func() { afterStart = nil })
	tree, done, leaf := runTree(t, name, "root")
	tree.Stop(true)
	ended(t, done, "the root")
	gone(t, leaf, "the child the root started at once")
}

// A root that ends of itself leaves its child running, in the job; the tree's Close ends it (the
// job kills what is in it when its last handle closes).
func TestClosingTheTreeEndsWhatIsLeft(t *testing.T) {
	const name = "TestClosingTheTreeEndsWhatIsLeft"
	treeHelper(name)
	tree, done, leaf := runTree(t, name, "parent")
	ended(t, done, "the root")
	if !Alive(leaf) {
		t.Fatal("the child ended with its root: nothing was left for the close")
	}
	tree.Close()
	gone(t, leaf, "the child left in the closed tree")
}

// A job Windows refuses (an enclosing job may forbid one) leaves the root running on its own:
// Contained says why, and Stop still ends the root, the root alone.
func TestATreeWindowsRefusesIsTheRootAlone(t *testing.T) {
	const name = "TestATreeWindowsRefusesIsTheRootAlone"
	treeHelper(name)
	assignToJob = func(windows.Handle, windows.Handle) error { return windows.ERROR_ACCESS_DENIED }
	t.Cleanup(func() { assignToJob = windows.AssignProcessToJobObject })
	tree, done, leaf := runTree(t, name, "root")
	if err := tree.Contained(); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("Contained: %v, want the refusal", err)
	}
	tree.Stop(true)
	ended(t, done, "the root")
	if !Alive(leaf) {
		t.Error("the child ended with its root, though no job held it: the refusal was not the job's")
	}
}
