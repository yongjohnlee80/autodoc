package tui

import (
	"regexp"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// A vectors listing read before a purge and answering after the purge's own listing never shows
// the purged model again. The dialog, opened a second time, still shows the first listing's rows
// while its own listing is on its way; a purge from those rows lists again. The open's listing is
// held here once the daemon has answered it (the model still there) and let go only after the
// purge's listing answered. The mutant, named here: loadVectors' ticket with no sequence (the
// purged model is listed again).
func TestAVectorsListingReadBeforeAPurgeNeverShowsItAgain(t *testing.T) {
	t.Parallel()
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	seedUnusedModel(t, d.db, "kb", "ollama|old|sha256:old|3", 2)
	var holdAnswer atomic.Bool
	answerHeld, answerGo := make(chan struct{}, 1), make(chan struct{})
	sess := NewSession(d.sock, nil)
	sess.SetAfterCall(func(method string) {
		if method == "index.models" && holdAnswer.CompareAndSwap(true, false) {
			answerHeld <- struct{}{}
			<-answerGo
		}
	})
	r := runTUI(t, sess, Options{})
	t.Cleanup(func() { close(answerGo) }) // a failed cell never leaves a worker held
	r.waitAttached(t, "kb")
	loading := func() int { return onLoop(r, func() int { return r.h.vectorsLoading }) }
	listed := func() bool {
		return onLoop(r, func() bool {
			return slices.ContainsFunc(r.h.vectors.All(), func(v vectorRow) bool { return v.name == "old" })
		})
	}
	r.h.p.Post(r.h.openVectors)
	r.s.WaitFor(t, "old listed", func(sc string) bool { return regexp.MustCompile(`unused +old +3 +2 `).MatchString(sc) })
	r.waitWithin(t, "the first listing answered", daemonWait, func() bool { return loading() == 0 })
	holdAnswer.Store(true)
	r.h.p.Post(r.h.openVectors) // opened again: its listing, read with old there, is held
	select {
	case <-answerHeld:
	case <-time.After(daemonWait):
		t.Fatal("the second open's listing: never answered")
	}
	r.h.p.Post(func() { r.h.startPurge(0) }) // from the rows still shown
	r.s.WaitForText(t, "purge the model?")
	r.keys(t, key('y'))
	r.s.WaitForText(t, "purged old's 2 vectors from kb")
	r.waitWithin(t, "the purge's listing answered", daemonWait, func() bool { return loading() == 1 })
	if listed() {
		t.Fatal("old still listed once the purge's listing answered")
	}
	answerGo <- struct{}{} // the open's listing, of old, answers last
	r.waitWithin(t, "the open's listing answered", daemonWait, func() bool { return loading() == 0 })
	if listed() {
		t.Fatalf("old, purged, is listed again once the listing read before the purge answered:\n%s", r.s.String())
	}
}
