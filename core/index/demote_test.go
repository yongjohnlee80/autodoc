package index

import (
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

func hitsOf(paths ...string) []Hit {
	out := make([]Hit, len(paths))
	for i, p := range paths {
		out[i] = Hit{Path: p}
	}
	return out
}

func pathsOfHits(hs []Hit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Path
	}
	return out
}

func TestDemote(t *testing.T) {
	for _, c := range []struct {
		name       string
		hits       []string
		successors map[string][]string
		want       []string
	}{
		{"below its successor", []string{"a", "b"}, map[string][]string{"a": {"b"}}, []string{"b", "a"}},
		// already below: never moved up
		{"never up", []string{"b", "x", "a"}, map[string][]string{"a": {"b"}}, []string{"b", "x", "a"}},
		// a's section above b moves just below b; its section already below stays put
		{"only the hits above", []string{"a#1", "x", "b", "y", "a#2"}, nil, nil},
		// successors first: [A, B, C] with A by B and B by C ends [C, B, A], not [A, C, B]
		{"a chain", []string{"a", "b", "c"}, map[string][]string{"a": {"b"}, "b": {"c"}}, []string{"c", "b", "a"}},
		// a loop has no order: neither moves
		{"a loop", []string{"a", "x", "b"}, map[string][]string{"a": {"b"}, "b": {"a"}}, []string{"a", "x", "b"}},
		// a document replaced by one on a loop still moves below it
		{"into a loop", []string{"d", "a", "b"}, map[string][]string{"d": {"a"}, "a": {"b"}, "b": {"a"}}, []string{"a", "d", "b"}},
		// several successors: below the lowest-placed one
		{"two successors", []string{"a", "b", "x", "c"}, map[string][]string{"a": {"b", "c"}}, []string{"b", "x", "c", "a"}},
		// a successor with no hit moves nothing
		{"successor absent", []string{"a", "x"}, map[string][]string{"a": {"gone"}}, []string{"a", "x"}},
	} {
		if c.name == "only the hits above" {
			// two hits of a, one on each side of b: the doc is a, so key the hits by path a
			hits := []Hit{{Path: "a", Breadcrumb: "1"}, {Path: "x"}, {Path: "b"}, {Path: "y"}, {Path: "a", Breadcrumb: "2"}}
			got := demote(hits, map[string][]string{"a": {"b"}})
			var order []string
			for _, h := range got {
				order = append(order, h.Path+h.Breadcrumb)
			}
			if want := []string{"x", "b", "a1", "y", "a2"}; !reflect.DeepEqual(order, want) {
				t.Errorf("%s: %v, want %v", c.name, order, want)
			}
			continue
		}
		if got := pathsOfHits(demote(hitsOf(c.hits...), c.successors)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDemoteMarksTheSuccessor: a superseded hit names a successor in the list, else its first; a
// hit nothing supersedes names none.
func TestDemoteMarksTheSuccessor(t *testing.T) {
	got := demote(hitsOf("a", "x", "c", "b"), map[string][]string{"a": {"gone", "b"}, "c": {"missing"}})
	marks := map[string]string{}
	for _, h := range got {
		marks[h.Path] = h.SupersededBy
	}
	if marks["a"] != "b" || marks["c"] != "missing" || marks["x"] != "" || marks["b"] != "" {
		t.Errorf("marks %v", marks)
	}
}

// TestDemoteIgnoresScores: the move is by position, so a negative, zero or huge score changes
// nothing: a superseded hit scored above its successor still ends below it.
func TestDemoteIgnoresScores(t *testing.T) {
	neg, worse := -0.5, -1.5
	hits := []Hit{{Path: "old", RankScore: &neg}, {Path: "new", RankScore: &worse}}
	if got := pathsOfHits(demote(hits, map[string][]string{"old": {"new"}})); !reflect.DeepEqual(got, []string{"new", "old"}) {
		t.Errorf("%v, want new before old", got)
	}
}

// TestDemoteProperties runs random hit lists and supersession graphs, chains, diamonds and loops
// among them, and checks the three things the demotion promises: every moved document's hits end
// below the last hit of each successor present; the documents not moved keep their relative
// order; a moved hit never ends above a hit of an unmoved document it was below.
func TestDemoteProperties(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20000; trial++ {
		n := 2 + r.Intn(5)
		var docs []string
		for i := 0; i < n; i++ {
			docs = append(docs, string(rune('a'+i)))
		}
		var paths []string
		for _, d := range docs {
			for k := 1 + r.Intn(2); k > 0; k-- {
				paths = append(paths, d)
			}
		}
		r.Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })
		succ := map[string][]string{}
		for _, d := range docs {
			for _, e := range docs {
				if d != e && r.Float64() < 0.25 {
					succ[d] = append(succ[d], e)
				}
			}
		}
		hits := make([]Hit, len(paths))
		for i, p := range paths {
			hits[i] = Hit{Path: p, Breadcrumb: string(rune('0' + i))} // each hit told apart
		}
		got := demote(hits, succ)
		pos := map[string]int{}
		for i, h := range got {
			pos[h.Path+h.Breadcrumb] = i
		}
		movers := map[string]bool{}
		// the documents the demotion may move: those with a successor among the hits, off any loop
		for _, h := range hits {
			if len(succ[h.Path]) > 0 && !onALoop(h.Path, succ, paths) {
				for _, s := range succ[h.Path] {
					if slices.ContainsFunc(hits, func(x Hit) bool { return x.Path == s }) {
						movers[h.Path] = true
					}
				}
			}
		}
		lastOf := func(p string) int {
			last := -1
			for i, h := range got {
				if h.Path == p {
					last = i
				}
			}
			return last
		}
		for i, h := range got {
			if !movers[h.Path] {
				continue
			}
			for _, s := range succ[h.Path] {
				if l := lastOf(s); l >= 0 && i < l && !onALoop(h.Path, succ, paths) {
					t.Fatalf("trial %d: %s at %d above its successor %s's last at %d\nhits %v succ %v got %v",
						trial, h.Path, i, s, l, paths, succ, pathsOfHits(got))
				}
			}
		}
		var keepBefore, keepAfter []string
		for _, h := range hits {
			if !movers[h.Path] {
				keepBefore = append(keepBefore, h.Path+h.Breadcrumb)
			}
		}
		for _, h := range got {
			if !movers[h.Path] {
				keepAfter = append(keepAfter, h.Path+h.Breadcrumb)
			}
		}
		if !reflect.DeepEqual(keepBefore, keepAfter) {
			t.Fatalf("trial %d: unmoved order %v became %v", trial, keepBefore, keepAfter)
		}
		for i, m := range hits {
			if !movers[m.Path] {
				continue
			}
			for j := 0; j < i; j++ {
				o := hits[j]
				if !movers[o.Path] && pos[m.Path+m.Breadcrumb] < pos[o.Path+o.Breadcrumb] {
					t.Fatalf("trial %d: %s moved above %s, which it was below", trial, m.Path, o.Path)
				}
			}
		}
	}
}

// onALoop reports whether p's supersession, among the listed documents, comes back round to it.
func onALoop(p string, succ map[string][]string, listed []string) bool {
	present := map[string]bool{}
	for _, l := range listed {
		present[l] = true
	}
	seen := map[string]bool{}
	stack := slices.Clone(succ[p])
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !present[x] {
			continue
		}
		if x == p {
			return true
		}
		if !seen[x] {
			seen[x] = true
			stack = append(stack, succ[x]...)
		}
	}
	return false
}
