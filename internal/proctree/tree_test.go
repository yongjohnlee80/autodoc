package proctree

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// treeHelper is this test binary run again as part of a tree, by PROCTREE_TREE, and never returns
// then: "root" starts a child at once, says its pid and lives on; "parent" does the same and ends;
// "leaf", the child, lives on. Run as the test, it returns.
func treeHelper(test string) {
	role := os.Getenv("PROCTREE_TREE")
	switch role {
	case "root", "parent":
		leaf := exec.Command(os.Args[0], "-test.run=^"+test+"$")
		leaf.Env = append(os.Environ(), "PROCTREE_TREE=leaf")
		if err := leaf.Start(); err != nil {
			os.Stdout.WriteString("no child: " + err.Error() + "\n")
			os.Exit(3)
		}
		os.Stdout.WriteString(strconv.Itoa(leaf.Process.Pid) + "\n")
		if role == "parent" {
			os.Exit(0)
		}
		fallthrough
	case "leaf":
		for {
			time.Sleep(time.Hour)
		}
	}
}

// runTree starts the test binary as a tree's root in role, and reads the pid of the child it
// started. done closes once the root is waited on. The test's end stops the tree, waits on the
// root, closes the tree and kills the child, whatever became of them.
func runTree(t *testing.T, test, role string) (tree *Tree, done <-chan struct{}, leaf int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+test+"$")
	cmd.Env = append(os.Environ(), "PROCTREE_TREE="+role)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	tree, err = StartTree(cmd)
	if err != nil {
		t.Fatalf("StartTree: %v", err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() {
		tree.Stop(true)
		select {
		case <-waited:
		case <-time.After(10 * time.Second):
			t.Error("the root outlived its tree's kill")
		}
		tree.Close()
		if leaf > 0 && Alive(leaf) {
			if p, err := os.FindProcess(leaf); err == nil {
				_ = p.Kill()
			}
		}
	})
	if err != nil {
		t.Fatalf("the root said nothing: %v", err)
	}
	leaf, err = strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("the root said %q, not its child's pid", line)
	}
	return tree, waited, leaf
}

// ended waits for done, the root's wait.
func ended(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not end", what)
	}
}

// gone waits for pid to be gone, a while: a killed process takes a moment to be reaped.
func gone(t *testing.T, pid int, what string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); Alive(pid); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s (%d) still runs", what, pid)
		}
	}
}

// The contract every OS keeps: a tree's Stop with kill ends its root and the process the root
// started; the tree holds it whole (Contained is nil). The helpers are this test binary.
func TestAStoppedTreeEndsWhatItsRootStarted(t *testing.T) {
	const name = "TestAStoppedTreeEndsWhatItsRootStarted"
	treeHelper(name)
	tree, done, leaf := runTree(t, name, "root")
	if err := tree.Contained(); err != nil {
		t.Fatalf("the tree holds the root alone: %v", err)
	}
	tree.Stop(true)
	ended(t, done, "the root")
	gone(t, leaf, "the root's child")
}
