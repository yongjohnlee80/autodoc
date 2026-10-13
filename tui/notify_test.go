package tui

import (
	"errors"
	"strings"
	"testing"
)

// TestAKeptErrorIsNotifiedOnce: an error a handler keeps is shown, as a notification naming it,
// once however often it is kept; Run still gets every one.
func TestAKeptErrorIsNotifiedOnce(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool {
		r.h.keep(errors.New("the frobnicator jammed"))
		r.h.keep(errors.New("the frobnicator jammed"))
		return true
	})
	r.s.WaitFor(t, "the notification", func(string) bool { return r.noticed("Something went wrong: the frobnicator jammed") })
	n := onLoop(r, func() int { // after both keeps' notices were posted, and ran
		n := 0
		for _, item := range r.h.noticeLog.items {
			if strings.Contains(item.text, "the frobnicator jammed") {
				n++
			}
		}
		return n
	})
	if n != 1 {
		t.Errorf("the kept error was notified %d times", n)
	}
	if got := onLoop(r, func() int { r.h.mu.Lock(); defer r.h.mu.Unlock(); return len(r.h.errs) }); got != 2 {
		t.Errorf("Run would get %d of the 2 errors", got)
	}
}

// TestABackgroundErrorIsRecordedNotNotified: a source projection that fails (as one set on a
// daemon event would) is recorded for Run and shows no notification: the user did not cause it.
func TestABackgroundErrorIsRecordedNotNotified(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool { r.h.set("App.status", make(chan int)); return true }) // no source takes a channel
	errs := onLoop(r, func() []error { r.h.mu.Lock(); defer r.h.mu.Unlock(); return append([]error(nil), r.h.errs...) })
	if len(errs) != 1 || !strings.HasPrefix(errs[0].Error(), "App.status: ") {
		t.Fatalf("the failed projection recorded %v", errs)
	}
	onLoop(r, func() bool { return true }) // anything it posted has run
	for _, item := range onLoop(r, func() []notice { return append([]notice(nil), r.h.noticeLog.items...) }) {
		if strings.Contains(item.text, "App.status") {
			t.Errorf("a background error was notified: %q", item.text)
		}
	}
}
