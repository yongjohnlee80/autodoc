package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PROGRESS — what the daemon still has to do, on the status line's right while there is any.
//
// The TUI polls index.status once a second while it is attached: the documents indexed, the jobs
// pending (a first scan's are every note), and the texts the embedding provider has yet to embed.
// While any is pending, the right slot shows a bar beside the last message; when indexing ends, it
// says so once, and the workspace's notes are listed again, the pickers' and the explorer's (a
// listing taken mid-scan was partial). They are listed again, too, whenever the index's change log
// has moved while nothing is pending: a note added, removed or renamed outside the TUI is indexed
// between two polls, faster than any poll sees it busy.

const progressEvery = time.Second

type progress struct {
	docs, pending, embedding int64
	busy                     bool  // pending or embedding work, as last polled
	cursor                   int64 // the change log's head, as last polled
	polled                   bool  // a poll has answered in this workspace
}

// poll asks for the status, and asks again a second after the answer, while the program runs.
func (h *Host) poll() {
	ep, ws := h.epoch, h.ws
	type answer struct {
		docs, pending, embedding, cursor int64
		err                              error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "index.status", ws)
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		a := answer{}
		a.docs, _ = m["docs"].(int64)
		a.pending, _ = m["pending_jobs"].(int64)
		a.cursor, _ = m["cursor"].(int64)
		if e := asMap(m["embeddings"]); e != nil {
			a.embedding, _ = e["pending"].(int64)
		}
		return a
	}, func(a answer) {
		if ep != h.epoch {
			return // another workspace or connection: its own poll is running
		}
		if a.err == nil {
			h.showProgress(a.docs, a.pending, a.embedding, a.cursor)
		}
		// the next poll is this epoch's: a switch or a reconnect in the meantime has started its own
		h.after(progressEvery, func() {
			if ep == h.epoch {
				h.poll()
			}
		})
	})
}

// after runs fn on the loop once d has passed, unless the program stopped.
func (h *Host) after(d time.Duration, fn func()) {
	ctx := h.ctx
	go func() {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			h.p.Post(fn)
		}
	}()
}

func (h *Host) showProgress(docs, pending, embedding, cursor int64) {
	was := h.prog.busy
	moved := h.prog.polled && cursor != h.prog.cursor
	h.prog.docs, h.prog.pending, h.prog.embedding = docs, pending, embedding
	h.prog.cursor, h.prog.polled = cursor, true
	h.prog.busy = pending > 0 || embedding > 0
	if was && pending == 0 && embedding == 0 {
		h.message = fmt.Sprintf("indexed %d notes", docs)
	}
	if pending == 0 && (was || moved) {
		// the lists taken while indexing were partial, or the notes changed since
		h.listNotes()
		h.relistInExplorer(h.ws)
	}
	h.publishStatus()
}

// progressText is the bar: indexing as done of done+pending, then what embedding has left.
func progressText(docs, pending, embedding int64) string {
	var parts []string
	if pending > 0 {
		total := docs + pending
		const width = 10
		filled := int(docs * width / max(total, 1))
		parts = append(parts, fmt.Sprintf("indexing %s%s %d/%d", strings.Repeat("█", filled), strings.Repeat("░", width-filled), docs, total))
	}
	if embedding > 0 {
		parts = append(parts, fmt.Sprintf("embedding %d pending", embedding))
	}
	return strings.Join(parts, " · ")
}

// publishStatus is the right slot: the progress, while there is any, then the last message.
func (h *Host) publishStatus() {
	right := h.message
	if h.prog.busy {
		if p := progressText(h.prog.docs, h.prog.pending, h.prog.embedding); p != "" {
			right = p + "  " + h.message
		}
	}
	h.set("App.status", strings.TrimSpace(right))
}
