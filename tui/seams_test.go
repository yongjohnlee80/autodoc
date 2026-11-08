package tui

import (
	"strings"
	"sync"
	"testing"
)

// tunings are the tests' own seams (seams.go), by test name. Parallel tests share the map, so a
// mutex guards it; each entry goes with its test (t.Cleanup).
var tunings = struct {
	sync.Mutex
	m map[string]*tuning
}{m: map[string]*tuning{}}

// tuning is one test's seams, and whether a program was made from them yet.
type tuning struct {
	s     seams
	built bool
}

// tune is t's own seams, to set before t's program starts: on first use a copy of its nearest
// parent test's, else of defaultSeams. A program copies its seams as it is made (runTUI), so a tune
// after that would reach nothing, and fails the test: change r.h.seams on the loop instead.
func tune(t testing.TB) *seams {
	t.Helper()
	tunings.Lock()
	defer tunings.Unlock()
	if tu, ok := tunings.m[t.Name()]; ok {
		if tu.built {
			t.Fatalf("tune(t) after %s's program started: set r.h.seams on the loop instead", t.Name())
		}
		return &tu.s
	}
	tu := &tuning{s: defaultSeams}
	if p := tunedParent(t.Name()); p != nil {
		tu.s = p.s
	}
	tunings.m[t.Name()] = tu
	name := t.Name()
	t.Cleanup(func() {
		tunings.Lock()
		delete(tunings.m, name)
		tunings.Unlock()
	})
	return &tu.s
}

// seamsFor is a copy of the seams t's program is made with: t's own, else its nearest parent
// test's (a subtest reads the tune its parent made), else nil, defaultSeams. t's own are marked
// built, so a later tune(t) fails.
func seamsFor(t testing.TB) *seams {
	tunings.Lock()
	defer tunings.Unlock()
	if tu, ok := tunings.m[t.Name()]; ok {
		tu.built = true
		s := tu.s
		return &s
	}
	if p := tunedParent(t.Name()); p != nil {
		s := p.s
		return &s
	}
	return nil
}

// tunedParent is the nearest parent test of name with seams of its own; the lock is held.
func tunedParent(name string) *tuning {
	for {
		i := strings.LastIndex(name, "/")
		if i < 0 {
			return nil
		}
		name = name[:i]
		if tu, ok := tunings.m[name]; ok {
			return tu
		}
	}
}

// The seams a test tunes reach its program, a subtest's reach its own, a subtest without its own
// reads its parent's, and a tune once the program is made fails the test.
func TestATestsSeamsReachOnlyItsOwnProgram(t *testing.T) {
	if seamsFor(t) != nil {
		t.Fatal("an untuned test's program is made from tuned seams; want nil, defaultSeams")
	}
	tune(t).globeMapPage = 7
	t.Run("inherits", func(t *testing.T) {
		if s := seamsFor(t); s == nil || s.globeMapPage != 7 {
			t.Fatalf("a subtest without its own seams reads %+v; want its parent's (globeMapPage 7)", s)
		}
	})
	t.Run("own", func(t *testing.T) {
		tune(t).globeMapPage = 3
		if s := seamsFor(t); s == nil || s.globeMapPage != 3 {
			t.Fatalf("a subtest's own seams read %+v; want globeMapPage 3", s)
		}
	})
	if s := seamsFor(t); s.globeMapPage != 7 {
		t.Fatalf("the parent's seams after its subtests tuned theirs: globeMapPage %d, want 7", s.globeMapPage)
	}
	built := seamsFor(t)
	built.globeMapPage = 9 // a copy: the program's, not the test's
	if s := seamsFor(t); s.globeMapPage != 7 {
		t.Fatalf("a change to a made program's copy reached the test's seams: %d, want 7", s.globeMapPage)
	}
	rec := &fatalRecorder{TB: t, msg: make(chan string, 1)}
	ran := make(chan struct{})
	go func() {
		defer close(ran)
		tuneAs(rec, t.Name())
	}()
	<-ran
	select {
	case msg := <-rec.msg:
		if !strings.Contains(msg, "set r.h.seams on the loop") {
			t.Fatalf("a late tune failed with %q; want it to say what to do instead", msg)
		}
	default:
		t.Fatal("a tune after the test's program was made did not fail the test")
	}
}

// tuneAs is tune for name's entry, through rec: the cell above reads a late tune's failure.
func tuneAs(rec *fatalRecorder, name string) {
	tune(namedTB{rec, name})
}

// namedTB is a testing.TB named as another test is.
type namedTB struct {
	*fatalRecorder
	name string
}

func (n namedTB) Name() string { return n.name }
