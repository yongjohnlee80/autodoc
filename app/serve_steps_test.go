package app

import (
	"slices"
	"testing"
)

// TestServeStepsStartInOrder: the socket is bound before the store's lease is taken, every
// workspace starts on the open store, and the API is built over all of them last.
func TestServeStepsStartInOrder(t *testing.T) {
	var names []string
	for _, s := range serveSteps {
		names = append(names, s.name)
	}
	if want := []string{"socket", "store", "workspaces", "files", "rpc"}; !slices.Equal(names, want) {
		t.Fatalf("serveSteps %v, want %v", names, want)
	}
}

// TestTheStackStopsInReverse: what started last stops first, each once, and a stack unwound twice
// stops nothing the second time.
func TestTheStackStopsInReverse(t *testing.T) {
	var s stack
	var stopped []int
	for i := range 3 {
		s.push(func() { stopped = append(stopped, i) })
	}
	s.unwind()
	s.unwind()
	if !slices.Equal(stopped, []int{2, 1, 0}) {
		t.Fatalf("stopped %v, want [2 1 0]", stopped)
	}
}
