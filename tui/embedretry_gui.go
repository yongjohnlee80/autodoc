//go:build gui

package tui

import (
	"context"
	"strconv"
	"sync"
	"time"

	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
)

// A FAILED EMBED READ IS NOT A LINE'S LAST LAYOUT. The editor keeps a block's layout while the
// note's text is unchanged, and an embedded file is not the note's text: a read that failed as the
// line was laid out (the file being rewritten, or its folder made again) would leave the line its
// plain link until something else laid it out. So a line holding alone an embed the Rendered view
// draws, whose file did not read, has the editor lay it out again a little later. It asks through
// the editor's diagram host, which owns the request: cancelled when the line changes or the editor
// goes, and, when it fires, that one block laid out again. A file that keeps failing is asked for
// after each of the renderer's delays, then no more; a later read that succeeds (an edit there,
// view.redraw, opening the note again) draws it as before, and a later failure is retried afresh.

// embedRetryDelays are how long after each failed read the line is laid out again: about 8 s in
// all, longer each time, so a rewrite that takes a second or two is drawn once it lands, and a file
// that is not there costs five layouts of its one line.
var embedRetryDelays = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}

// embedRetryLang is the language of a retry's request of the diagram host. No fence spells it, so
// a retry is never taken for a diagram's request, nor one for a retry.
const embedRetryLang = "autodoc.embed-retry"

// embedRetries are the retries of the open note's embeds that did not read. Its methods run on the
// editor's loop, except a timer's fired, which runs on the timer's goroutine; mu guards fired.
type embedRetries struct {
	delays []time.Duration
	mu     sync.Mutex
	note   string         // the note fired counts for; another note starts afresh
	fired  map[string]int // by destination: the retries that fired since it last read
}

// ask has host lay block b out again after the next delay, for dest of note, whose file did not
// read. A request already waiting for that retry is the host's to keep (the same request), so a
// block laid out again meanwhile asks nothing new. With no host, or every delay spent, nothing.
func (e *embedRetries) ask(host guiwidget.DiagramHost, b guiwidget.Block, note, dest string) {
	if host == nil {
		return
	}
	e.mu.Lock()
	if e.note != note {
		e.note, e.fired = note, nil
	}
	n := e.fired[dest]
	e.mu.Unlock()
	if n >= len(e.delays) {
		return
	}
	timer := retryTimer{after: e.delays[n], fired: func() { e.count(note, dest, n) }}
	host.Diagram(timer, b, guiwidget.DiagramRequest{Lang: embedRetryLang, Src: dest + "\x00" + strconv.Itoa(n)})
}

// count records retry n of dest, for note, as fired: the next failed read asks retry n+1.
func (e *embedRetries) count(note, dest string, n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.note != note || e.fired[dest] != n {
		return
	}
	if e.fired == nil {
		e.fired = map[string]int{}
	}
	e.fired[dest] = n + 1
}

// read forgets dest's retries, for note: its file read, so a later failure is retried afresh.
func (e *embedRetries) read(note, dest string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.note == note {
		delete(e.fired, dest)
	}
}

// retryTimer is the Diagrammer of one retry: never a picture, it answers Pending and calls ready
// after its delay, unless the request is withdrawn first. fired runs just before ready.
type retryTimer struct {
	after time.Duration
	fired func()
}

func (t retryTimer) Diagram(ctx context.Context, _ guiwidget.DiagramRequest, ready func()) guiwidget.DiagramAnswer {
	timer := time.AfterFunc(t.after, func() {
		if ctx.Err() != nil {
			return
		}
		t.fired()
		ready()
	})
	context.AfterFunc(ctx, func() { timer.Stop() })
	return guiwidget.DiagramAnswer{State: guiwidget.Pending}
}
