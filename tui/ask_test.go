package tui

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/feature"
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
				r.h.ask(feature.Question{Title: "test cancellation?", OnYes: func() { t.Error("cancellation test accepted") }, OnCancel: func() {
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
				onLoop(r, func() bool { r.h.asker.Rejected(); r.h.asker.Closed(); r.h.p.Quit(); return true })
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
			a := feature.NewAsker(s)
			yes, cancel := 0, 0
			asked := a.Ask(feature.Question{Text: "original", OnYes: func() { yes++ }, OnCancel: func() { cancel++ }})
			switch ending {
			case "no", "escape":
				a.Rejected()
				a.Closed()
			case "close":
				a.Closed()
			case "replacement":
				a.Ask(feature.Question{Text: "replacement"})
			case "shutdown":
				a.Stopping()
				a.Stopped()
				a.Stopped()
			case "yes":
				a.Accepted()
				a.Closed()
			}
			a.Rejected()
			a.Closed()
			a.Accepted()
			a.Stopped()
			wantYes, wantCancel := 0, 1
			if ending == "yes" {
				wantYes, wantCancel = 1, 0
			}
			if yes != wantYes || cancel != wantCancel {
				t.Fatalf("%s: yes=%d cancel=%d; want %d/%d", ending, yes, cancel, wantYes, wantCancel)
			}
			asked.Say("stale")
			if s.Sets["App.askText"] == "stale" {
				t.Error("ended question can still rewrite text")
			}
		})
	}
}

func TestAskReplacementCancellationKeepsReentrantNewestQuestion(t *testing.T) {
	a := feature.NewAsker(newScripted("kb"))
	oldCancel, displacedCancel, newestYes := 0, 0, 0
	a.Ask(feature.Question{OnCancel: func() { oldCancel++; a.Ask(feature.Question{OnYes: func() { newestYes++ }}) }})
	a.Ask(feature.Question{OnCancel: func() { displacedCancel++ }})
	a.Accepted()
	a.Closed()
	a.Stopped()
	if oldCancel != 1 || displacedCancel != 1 || newestYes != 1 {
		t.Fatalf("reentrant replacement: old=%d displaced=%d newestYes=%d", oldCancel, displacedCancel, newestYes)
	}
}

func TestAskAnsweredClosedNotificationDoesNotCancelNewQuestion(t *testing.T) {
	for _, answer := range []string{"yes", "no"} {
		t.Run(answer, func(t *testing.T) {
			a := feature.NewAsker(newScripted("kb"))
			newYes, newCancel := 0, 0
			next := func() { a.Ask(feature.Question{OnYes: func() { newYes++ }, OnCancel: func() { newCancel++ }}) }
			a.Ask(feature.Question{OnYes: next, OnCancel: next})
			if answer == "yes" {
				a.Accepted()
			} else {
				a.Rejected()
			}
			a.Closed() // notification for the OLD answered dialog
			a.Accepted()
			a.Closed()
			a.Stopped()
			if newYes != 1 || newCancel != 0 {
				t.Fatalf("old closed cancelled new question: yes=%d cancel=%d", newYes, newCancel)
			}
		})
	}
}

func TestAskStoppedCancelsPendingAndRefusesToOpenAgain(t *testing.T) {
	s := newScripted("kb")
	a := feature.NewAsker(s)
	cancel := 0
	a.Ask(feature.Question{OnCancel: func() { cancel++ }})
	a.Stopped()
	a.Stopped()
	opened := len(s.Opened)
	a.Ask(feature.Question{OnCancel: func() { cancel++ }, OnYes: func() { t.Error("stopped question accepted") }})
	a.Accepted()
	if cancel != 2 || len(s.Opened) != opened {
		t.Fatalf("stopped ask: cancelled=%d opened=%d want %d", cancel, len(s.Opened), opened)
	}
}

// The card asks one question at a time: Yes runs its OnYes once, No and a newer question drop it,
// and only the question still asked can have its text rewritten.
func TestAskRunsYesOnceAndOnlyForTheQuestionAsked(t *testing.T) {
	s := newScripted("kb")
	a := feature.NewAsker(s)
	if st := a.State(); st["App.askYes"] != "&Yes" || st["App.askNo"] != "&No" {
		t.Fatalf("first labels %v", st)
	}
	ran := 0
	asked := a.Ask(feature.Question{Title: "remove the thing?", Text: "counting…", Yes: "&Yes, remove it", OnYes: func() { ran++ }})
	if s.Opened[len(s.Opened)-1] != "confirm" || s.Sets["App.askTitle"] != "remove the thing?" ||
		s.Sets["App.askYes"] != "&Yes, remove it" || s.Sets["App.askNo"] != "&No" {
		t.Fatalf("the card: opened %v, set %v", s.Opened, s.Sets)
	}
	asked.Say("3 things go")
	if s.Sets["App.askText"] != "3 things go" {
		t.Errorf("Say while asked: %v", s.Sets["App.askText"])
	}
	a.Commands()["App.askAccepted"].Run(nil)
	a.Commands()["App.askAccepted"].Run(nil)
	if ran != 1 {
		t.Errorf("Yes ran OnYes %d times, want once", ran)
	}
	asked.Say("late")
	if s.Sets["App.askText"] != "3 things go" {
		t.Errorf("Say after the answer rewrote the text: %v", s.Sets["App.askText"])
	}

	first := a.Ask(feature.Question{Title: "one?", OnYes: func() { ran += 10 }})
	a.Ask(feature.Question{Title: "two?", OnYes: func() { ran += 100 }})
	first.Say("stale")
	if s.Sets["App.askText"] == "stale" {
		t.Error("an older question's Say rewrote the newer one")
	}
	a.Commands()["App.askAccepted"].Run(nil)
	if ran != 101 {
		t.Errorf("Yes on the newer question: ran %d, want 101 (only the newer one's OnYes)", ran)
	}

	a.Ask(feature.Question{Title: "three?", OnYes: func() { ran += 1000 }})
	a.Commands()["App.askRejected"].Run(nil)
	a.Commands()["App.askAccepted"].Run(nil)
	if ran != 101 {
		t.Errorf("No, then a stray accept: ran %d, want 101", ran)
	}
	feature.Asked{}.Say("no-op") // a zero Asked, as a test's Core returns, says nothing
}

// A question's third button (Question.Other) shows only when asked for. Chosen, it runs OnOther
// once and neither OnYes nor OnCancel, and the card's closed notification after it cancels nothing:
// what was asked lives on (a candidate's Audit…).
func TestAskOtherRunsOnceAndNeitherYesNorCancel(t *testing.T) {
	s := newScripted("kb")
	a := feature.NewAsker(s)
	if st := a.State(); st["App.askOther"] != "" || st["App.askOtherShown"] != false {
		t.Fatalf("first state %v", st)
	}
	yes, cancel, other := 0, 0, 0
	a.Ask(feature.Question{Title: "install?", Other: "&Audit…", OnYes: func() { yes++ }, OnCancel: func() { cancel++ }, OnOther: func() { other++ }})
	if s.Sets["App.askOther"] != "&Audit…" || s.Sets["App.askOtherShown"] != true {
		t.Fatalf("the third button: %v %v", s.Sets["App.askOther"], s.Sets["App.askOtherShown"])
	}
	a.Commands()["App.askOtherChosen"].Run(nil)
	a.Commands()["App.askOtherChosen"].Run(nil)
	a.Closed() // the card's closed notification after the button
	a.Accepted()
	if other != 1 || yes != 0 || cancel != 0 {
		t.Fatalf("other %d, yes %d, cancel %d; want 1, 0, 0", other, yes, cancel)
	}

	a.Ask(feature.Question{Title: "remove?", OnCancel: func() { cancel++ }})
	if s.Sets["App.askOther"] != "" || s.Sets["App.askOtherShown"] != false {
		t.Fatalf("a question without one shows a third button: %v %v", s.Sets["App.askOther"], s.Sets["App.askOtherShown"])
	}
	a.OtherChosen() // a card without one: nothing is chosen, and the question is still asked
	a.Rejected()
	if cancel != 1 || other != 1 {
		t.Fatalf("cancel %d, other %d; want 1, 1", cancel, other)
	}
}

// TestAQuestionAskedWhenFreeWaitsAndYields: a question asked to wait for a free card is shown only
// once the open one is answered, in order; another question displaces it back to the head of the
// wait without running its callbacks, and it is shown again once that one is answered; a stopping
// host cancels what still waits.
func TestAQuestionAskedWhenFreeWaitsAndYields(t *testing.T) {
	a := feature.NewAsker(newScripted("kb"))
	var got []string
	q := func(name string) feature.Question {
		return feature.Question{Title: name, OnYes: func() { got = append(got, name+":yes") }, OnCancel: func() { got = append(got, name+":cancel") }}
	}
	shown := func() string {
		if !a.Pending {
			return ""
		}
		return a.Current.Title
	}
	a.Ask(q("user"))
	a.AskWhenFree(q("one"))
	a.AskWhenFree(q("two"))
	if shown() != "user" {
		t.Fatalf("shown %q while the user's question is open", shown())
	}
	a.Accepted()
	a.Closed()
	if shown() != "one" {
		t.Fatalf("after the user's answer: shown %q, want one", shown())
	}
	a.Ask(q("other"))
	if shown() != "other" || len(got) != 1 {
		t.Fatalf("a question over one: shown %q, %v; want other shown, one's callbacks not run", shown(), got)
	}
	a.Rejected()
	a.Closed()
	if shown() != "one" {
		t.Fatalf("after other's answer: shown %q, want one again", shown())
	}
	a.Accepted()
	a.Closed()
	if shown() != "two" {
		t.Fatalf("after one: shown %q, want two", shown())
	}
	a.AskWhenFree(q("three"))
	a.Stopped()
	if want := []string{"user:yes", "other:cancel", "one:yes", "two:cancel", "three:cancel"}; !slices.Equal(got, want) {
		t.Fatalf("callbacks %v, want %v", got, want)
	}
}

// TestAQuestionAskedWhenFreeOfAStoppedHostIsCancelledAtOnce: once the host has stopped, a question
// asked to wait for a free card is neither shown nor kept waiting: its OnCancel runs at once, and
// once only, and its OnYes never; one without an OnCancel is dropped as quietly.
func TestAQuestionAskedWhenFreeOfAStoppedHostIsCancelledAtOnce(t *testing.T) {
	s := newScripted("kb")
	a := feature.NewAsker(s)
	a.Stopped()
	cancel := 0
	a.AskWhenFree(feature.Question{Title: "late", OnCancel: func() { cancel++ }, OnYes: func() { t.Error("a stopped host's question accepted") }})
	if cancel != 1 {
		t.Fatalf("asked when free of a stopped host: cancelled %d times, want once at once", cancel)
	}
	a.AskWhenFree(feature.Question{Title: "quiet"}) // no OnCancel: nothing to run, nothing to panic on
	a.Accepted()
	a.Stopped()
	if _, set := s.Sets["App.askTitle"]; set || len(s.Opened) != 0 || a.Pending || len(a.Waiting) != 0 {
		t.Fatalf("a stopped host's question went on: title set %v, opened %v, pending %v, waiting %d", set, s.Opened, a.Pending, len(a.Waiting))
	}
	if cancel != 1 {
		t.Fatalf("the stopped host's question cancelled %d times, want once", cancel)
	}
}

// A question's OnNo runs once, for the card's own No alone (its No button or Escape, the dialog's
// reject), and OnCancel after it; a close, a replacement or the host stopping run OnCancel and
// never OnNo, and a Yes runs neither: an answer remembered (a plugin's launch) is never a card
// that merely went away.
func TestAskOnNoIsTheCardsOwnNoAlone(t *testing.T) {
	for _, ending := range []string{"no", "close", "replacement", "shutdown", "yes"} {
		t.Run(ending, func(t *testing.T) {
			a := feature.NewAsker(newScripted("kb"))
			var order []string
			a.Ask(feature.Question{Text: "start it?", OnYes: func() { order = append(order, "yes") },
				OnNo: func() { order = append(order, "no") }, OnCancel: func() { order = append(order, "cancel") }})
			switch ending {
			case "no":
				a.Rejected()
				a.Closed() // the card's closed notification after the button
			case "close":
				a.Closed()
			case "replacement":
				a.Ask(feature.Question{Text: "another"})
			case "shutdown":
				a.Stopping()
			case "yes":
				a.Accepted()
				a.Closed()
			}
			a.Rejected() // answered already: nothing more
			want := map[string][]string{"no": {"no", "cancel"}, "close": {"cancel"}, "replacement": {"cancel"},
				"shutdown": {"cancel"}, "yes": {"yes"}}[ending]
			if !slices.Equal(order, want) {
				t.Fatalf("%s ran %v, want %v", ending, order, want)
			}
		})
	}
}
