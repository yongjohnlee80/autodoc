package crew_test

import (
	"errors"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestAConnectionHoldsTwoLongPolls (docs/decisions/crew.md#durable-mail): a connection's third
// long-poll is refused until one of its two ends; a leave is idempotent; another connection is
// counted apart; a caller with no connection identity, or a nil limiter, is never refused.
func TestAConnectionHoldsTwoLongPolls(t *testing.T) {
	l := crew.NewLongPolls()
	a1, err1 := l.Enter("tui#a")
	a2, err2 := l.Enter("tui#a")
	if err1 != nil || err2 != nil {
		t.Fatalf("two long-polls: %v, %v", err1, err2)
	}
	if _, err := l.Enter("tui#a"); !errors.Is(err, crew.ErrLongPollsBusy) {
		t.Fatalf("a third: %v, want ErrLongPollsBusy", err)
	}
	if n := l.Held(); n != 2 {
		t.Fatalf("Held: %d, want 2 (a refused call holds nothing)", n)
	}
	if b, err := l.Enter("nvim#b"); err != nil {
		t.Fatalf("another connection's first: %v", err)
	} else {
		b()
	}
	a1()
	a1() // a second leave of the same long-poll frees nothing more
	if n := l.Held(); n != 1 {
		t.Fatalf("Held after one left twice: %d, want 1", n)
	}
	a3, err := l.Enter("tui#a")
	if err != nil {
		t.Fatalf("a long-poll after one ended: %v", err)
	}
	if _, err := l.Enter("tui#a"); !errors.Is(err, crew.ErrLongPollsBusy) {
		t.Fatalf("a third again: %v, want ErrLongPollsBusy (the double leave freed a slot it didn't hold)", err)
	}
	a2()
	a3()
	if n := l.Held(); n != 0 {
		t.Fatalf("Held once all left: %d, want 0", n)
	}
	for range 5 {
		if _, err := l.Enter(nil); err != nil {
			t.Fatalf("a caller with no connection identity: %v", err)
		}
	}
	var none *crew.LongPolls
	if leave, err := none.Enter("tui#a"); err != nil || leave == nil {
		t.Fatalf("a nil limiter: %v", err)
	}
}
