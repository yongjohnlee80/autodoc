package tui

import (
	"sync/atomic"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

func TestAskRealDialogCancellationAndShutdownCompleteBeforeOwnerUnmount(t *testing.T) {
	for _, ending := range []string{"no", "escape", "close", "shutdown"} {
		t.Run(ending, func(t *testing.T) {
			d := startDaemon(t, map[string][]string{"kb": {"note.md", "# Note\n"}})
			r := attached(t, d)
			var calls atomic.Int32
			live := make(chan bool, 2)
			onLoop(r, func() bool {
				r.h.ask(Question{Title: "test cancellation?", OnYes: func() { t.Error("cancellation test accepted") }, OnCancel: func() {
					calls.Add(1)
					view, found := r.h.p.Find("editor")
					ctx, ok := view.(interface{ Context() *tuicore.Context })
					mounted := found && ok && ctx.Context() != nil && ctx.Context().Mounted()
					select {
					case <-r.h.p.App().Done():
						mounted = false
					default:
					}
					select {
					case live <- mounted:
					default:
					}
				}})
				return true
			})
			r.s.WaitForText(t, "test cancellation?")
			switch ending {
			case "no":
				r.keys(t, enter())
			case "escape":
				r.keys(t, esc())
			case "close":
				onLoop(r, func() bool { r.h.closeDialog("confirm"); return true })
			case "shutdown":
				onLoop(r, func() bool { r.h.p.Quit(); return true })
			}
			select {
			case mounted := <-live:
				if !mounted {
					t.Error("completion ran after owner/loop teardown")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancel did not complete")
			}
			if ending != "shutdown" {
				onLoop(r, func() bool { r.h.asker.rejected(); r.h.asker.closed(); r.h.p.Quit(); return true })
			}
			select {
			case <-r.s.Quit():
			case <-time.After(5 * time.Second):
				t.Fatal("program did not quit")
			}
			if calls.Load() != 1 {
				t.Fatalf("cancellation ran %d times", calls.Load())
			}
		})
	}
}

func TestAskCancellationEndingsAreOnceAndExclusiveWithYes(t *testing.T) {
	for _, ending := range []string{"no", "escape", "close", "replacement", "shutdown", "yes"} {
		t.Run(ending, func(t *testing.T) {
			s := newScripted("kb")
			a := newAsker(s)
			yes, cancel := 0, 0
			asked := a.ask(Question{Text: "original", OnYes: func() { yes++ }, OnCancel: func() { cancel++ }})
			switch ending {
			case "no", "escape":
				a.rejected()
				a.closed()
			case "close":
				a.closed()
			case "replacement":
				a.ask(Question{Text: "replacement"})
			case "shutdown":
				a.Stopping()
				a.Stopped()
				a.Stopped()
			case "yes":
				a.accepted()
				a.closed()
			}
			a.rejected()
			a.closed()
			a.accepted()
			a.Stopped()
			wantYes, wantCancel := 0, 1
			if ending == "yes" {
				wantYes, wantCancel = 1, 0
			}
			if yes != wantYes || cancel != wantCancel {
				t.Fatalf("%s: yes=%d cancel=%d; want %d/%d", ending, yes, cancel, wantYes, wantCancel)
			}
			asked.Say("stale")
			if s.set["App.askText"] == "stale" {
				t.Error("ended question can still rewrite text")
			}
		})
	}
}

func TestAskReplacementCancellationKeepsReentrantNewestQuestion(t *testing.T) {
	a := newAsker(newScripted("kb"))
	oldCancel, displacedCancel, newestYes := 0, 0, 0
	a.ask(Question{OnCancel: func() { oldCancel++; a.ask(Question{OnYes: func() { newestYes++ }}) }})
	a.ask(Question{OnCancel: func() { displacedCancel++ }})
	a.accepted()
	a.closed()
	a.Stopped()
	if oldCancel != 1 || displacedCancel != 1 || newestYes != 1 {
		t.Fatalf("reentrant replacement: old=%d displaced=%d newestYes=%d", oldCancel, displacedCancel, newestYes)
	}
}

func TestAskAnsweredClosedNotificationDoesNotCancelNewQuestion(t *testing.T) {
	for _, answer := range []string{"yes", "no"} {
		t.Run(answer, func(t *testing.T) {
			a := newAsker(newScripted("kb"))
			newYes, newCancel := 0, 0
			next := func() { a.ask(Question{OnYes: func() { newYes++ }, OnCancel: func() { newCancel++ }}) }
			a.ask(Question{OnYes: next, OnCancel: next})
			if answer == "yes" {
				a.accepted()
			} else {
				a.rejected()
			}
			a.closed() // notification for the OLD answered dialog
			a.accepted()
			a.closed()
			a.Stopped()
			if newYes != 1 || newCancel != 0 {
				t.Fatalf("old closed cancelled new question: yes=%d cancel=%d", newYes, newCancel)
			}
		})
	}
}

func TestAskStoppedCancelsPendingAndRefusesToOpenAgain(t *testing.T) {
	s := newScripted("kb")
	a := newAsker(s)
	cancel := 0
	a.ask(Question{OnCancel: func() { cancel++ }})
	a.Stopped()
	a.Stopped()
	opened := len(s.opened)
	a.ask(Question{OnCancel: func() { cancel++ }, OnYes: func() { t.Error("stopped question accepted") }})
	a.accepted()
	if cancel != 2 || len(s.opened) != opened {
		t.Fatalf("stopped ask: cancelled=%d opened=%d want %d", cancel, len(s.opened), opened)
	}
}

// The card asks one question at a time: Yes runs its OnYes once, No and a newer question drop it,
// and only the question still asked can have its text rewritten.
func TestAskRunsYesOnceAndOnlyForTheQuestionAsked(t *testing.T) {
	s := newScripted("kb")
	a := newAsker(s)
	if st := a.State(); st["App.askYes"] != "&Yes" || st["App.askNo"] != "&No" {
		t.Fatalf("first labels %v", st)
	}
	ran := 0
	asked := a.ask(Question{Title: "remove the thing?", Text: "counting…", Yes: "&Yes, remove it", OnYes: func() { ran++ }})
	if s.opened[len(s.opened)-1] != "confirm" || s.set["App.askTitle"] != "remove the thing?" ||
		s.set["App.askYes"] != "&Yes, remove it" || s.set["App.askNo"] != "&No" {
		t.Fatalf("the card: opened %v, set %v", s.opened, s.set)
	}
	asked.Say("3 things go")
	if s.set["App.askText"] != "3 things go" {
		t.Errorf("Say while asked: %v", s.set["App.askText"])
	}
	a.Commands()["App.askAccepted"].Run(nil)
	a.Commands()["App.askAccepted"].Run(nil)
	if ran != 1 {
		t.Errorf("Yes ran OnYes %d times, want once", ran)
	}
	asked.Say("late")
	if s.set["App.askText"] != "3 things go" {
		t.Errorf("Say after the answer rewrote the text: %v", s.set["App.askText"])
	}

	first := a.ask(Question{Title: "one?", OnYes: func() { ran += 10 }})
	a.ask(Question{Title: "two?", OnYes: func() { ran += 100 }})
	first.Say("stale")
	if s.set["App.askText"] == "stale" {
		t.Error("an older question's Say rewrote the newer one")
	}
	a.Commands()["App.askAccepted"].Run(nil)
	if ran != 101 {
		t.Errorf("Yes on the newer question: ran %d, want 101 (only the newer one's OnYes)", ran)
	}

	a.ask(Question{Title: "three?", OnYes: func() { ran += 1000 }})
	a.Commands()["App.askRejected"].Run(nil)
	a.Commands()["App.askAccepted"].Run(nil)
	if ran != 101 {
		t.Errorf("No, then a stray accept: ran %d, want 101", ran)
	}
	Asked{}.Say("no-op") // a zero Asked, as a test's Core returns, says nothing
}
