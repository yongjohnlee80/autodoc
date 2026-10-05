package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/search/embed"
)

// PROGRESS — what the daemon still has to do, in a toast while there is any (notify.go).
//
// The TUI polls index.status once a second while it is attached: the documents indexed, the jobs
// pending (a first scan's are every file), and the texts the embedding provider has yet to embed
// (a new model's, while it fills to replace the active one). While any is pending, a toast shows a
// bar, a spinner turning while the model embeds; when indexing ends, the toast says so, and goes, and the workspace's files are listed again, the pickers' and the explorer's (a
// listing taken mid-scan was partial). They are listed again, too, whenever the index's change log
// has moved while nothing is pending: a file added, removed or renamed outside the TUI is indexed
// between two polls, faster than any poll sees it busy.

const progressEvery = time.Second

// spinEvery is how often the spinner turns while the provider embeds.
const spinEvery = 150 * time.Millisecond

// spinFrames are the spinner's: a model is making vectors.
var spinFrames = []string{"-", "\\", "|", "/"}

type progress struct {
	ws            string // the workspace polled, which the progress names
	docs, pending int64
	emb           embedProgress
	busy          bool     // pending or embedding work, as last polled
	cursor        int64    // the change log's head, as last polled
	polled        bool     // a poll has answered in this workspace
	spin          int      // the spinner's frame
	spinning      bool     // the spinner's timer is running
	warming       []string // what the workspace waits on before it answers fully, as last polled
}

// warmingReasons is what index.status says the workspace is warming up for: its own list, and a
// follower still on its first scan of the root (searches and listings answer, but are not complete).
func warmingReasons(m map[string]any) []string {
	var out []string
	if f := asMap(m["following"]); str(f, "mode") == "starting" {
		out = append(out, "scanning the workspace's files")
	}
	for _, r := range asList(m["warming"]) {
		if s, ok := r.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// embedProgress is index.status's embeddings: nil there is off.
type embedProgress struct {
	on                     bool // a provider is in use
	model                  string
	texts                  int64  // the distinct texts every model covers once done
	pending                int64  // those the active model has no vector for
	target                 string // the model filling to replace the active one; "" for none
	queueState, waitingFor string
	semantic               string
	targetPending          int64
	refused, targetRefused int64
	failing                bool // the provider's last call failed
}

// embedOf reads index.status's embeddings.
func embedOf(m map[string]any) embedProgress {
	if m == nil {
		return embedProgress{}
	}
	e := embedProgress{on: true, model: str(m, "model"), target: str(m, "target"), failing: str(m, "last_error") != ""}
	e.texts, _ = m["texts"].(int64)
	e.pending, _ = m["pending"].(int64)
	e.targetPending, _ = m["target_pending"].(int64)
	e.refused, _ = m["refused"].(int64)
	e.targetRefused, _ = m["target_refused"].(int64)
	e.queueState, e.waitingFor = str(m, "queue_state"), str(m, "waiting_for")
	e.semantic = str(m, "semantic")
	return e
}

// working is the embedding left: the target's while one fills, else the active model's.
func (e embedProgress) working() int64 {
	if e.target != "" {
		return max(0, e.targetPending-e.targetRefused)
	}
	return max(0, e.pending-e.refused)
}

// online is semantic search answering: a provider in use, its model not replaced mid-switch, its
// last call answered. Offline, a search is by words.
func (e embedProgress) online() bool {
	return e.on && e.target == "" && !e.failing && e.queueState != "paused" && e.semantic != "off"
}

// poll asks for the status, and asks again a second after the answer, while the program runs.
func (h *Host) poll() {
	ep, ws := h.epoch, h.ws
	if ws != "" && time.Since(h.focusSent) >= 10*time.Second {
		h.focusSent = time.Now()
		do(h, func(ctx context.Context) error { _, err := h.call(ctx, "workspace.focus", ws); return err }, func(error) {})
	}
	type answer struct {
		docs, pending, cursor int64
		emb                   embedProgress
		warming               []string
		err                   error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "index.status", ws)
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		a := answer{emb: embedOf(asMap(m["embeddings"])), warming: warmingReasons(m)}
		a.docs, _ = m["docs"].(int64)
		a.pending, _ = m["pending_jobs"].(int64)
		a.cursor, _ = m["cursor"].(int64)
		return a
	}, func(a answer) {
		if ep != h.epoch {
			return // another workspace or connection: its own poll is running
		}
		if a.err == nil {
			h.showWarming(a.warming)
			h.showProgress(a.docs, a.pending, a.emb, a.cursor)
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

// showWarming says, in a toast that stays while it lasts, that the daemon is warming up — its first
// scan of the root, a provider being set up, a restart to take one — so a search or a listing that
// comes back short reads as "not yet", not as "nothing". When it ends, the toast says so and the
// files are listed again: a listing taken while it lasted may have failed or been partial.
func (h *Host) showWarming(reasons []string) {
	was := len(h.prog.warming) > 0
	h.prog.warming = reasons
	switch {
	case len(reasons) > 0:
		h.notifyOngoing(toastWarming, "warming up: "+strings.Join(reasons, "; ")+"…")
	case was:
		h.notifyDone(toastWarming, "ready: search and the files are up to date")
		h.listFiles()
		h.relistInExplorer(h.ws)
	}
}

func (h *Host) showProgress(docs, pending int64, emb embedProgress, cursor int64) {
	was := h.prog.busy
	prev := h.prog.emb
	polled := h.prog.polled
	moved := h.prog.polled && cursor != h.prog.cursor
	h.prog.ws, h.prog.docs, h.prog.pending, h.prog.emb = h.ws, docs, pending, emb
	h.prog.cursor, h.prog.polled = cursor, true
	// a new embedding model (a switch starting, the new one taking over, or words only) makes the
	// search's hits answers to another model: the search starts afresh rather than refresh them
	modelChanged := polled && (prev.model != emb.model || prev.target != emb.target || prev.on != emb.on)
	if modelChanged {
		h.clearSearch("the embedding model changed")
	}
	if !polled || prev.semantic != emb.semantic || prev.model != emb.model || prev.target != emb.target || prev.on != emb.on || prev.pending != emb.pending || prev.targetPending != emb.targetPending {
		switch {
		case emb.target != "" || emb.semantic == "switching":
			h.notifyOngoing(toastSemantic, "semantic search is temporarily words-only while the new model fills in "+h.ws)
		case polled && prev.target != "" && emb.target == "" && emb.on && emb.semantic != "off":
			h.notifyDone(toastSemantic, "semantic search is available again in "+h.ws+" ("+emb.semantic+")")
		case polled && prev.target != "" && emb.target == "":
			h.notifyDone(toastSemantic, "model switch ended; search in "+h.ws+" remains words-only")
		case polled && prev.pending > 0 && emb.pending == 0 && emb.semantic == "ready":
			h.notifyDone(toastSemantic, "semantic search is ready in "+h.ws)
		}
		if !modelChanged {
			h.refreshSearch()
		}
	}
	h.prog.busy = pending > 0 || emb.working() > 0 || (emb.queueState == "paused" && emb.pending > 0)
	if h.searchWaitToast && emb.working() == 0 {
		h.notifyDone(toastSearchWait, "embedding finished; search is refreshing in "+h.ws)
		h.searchWaitToast = false
	}
	if was && !h.prog.busy {
		h.notifyDone(toastProgress, fmt.Sprintf("indexed %d files", docs))
	}
	if pending == 0 && (was || moved) {
		// the lists taken while indexing were partial, or the files changed since
		h.listFiles()
		h.relistInExplorer(h.ws)
	}
	h.showSemantic()
	h.syncStages(false) // a search a model's change affects is refreshed or cleared above
	h.publishStatus()
	h.spinWhileEmbedding()
}

// showSemantic is semantic search's mark: a green dot and the model while it answers, a red one
// and "lexical search" while it does not, with the model it is switching to. The AI models dialog
// and the search's title say why, too.
func (h *Host) showSemantic() {
	e := h.prog.emb
	dot, label, why := "red", "lexical search", ""
	switch {
	case e.online():
		dot, label = "green", "semantic search"
		if e.model != "" {
			label += " · " + embed.ModelName(e.model)
		}
		if e.refused > 0 {
			why = fmt.Sprintf(" · %d refused texts (see Vectors…)", e.refused)
		}
	case len(h.prog.warming) > 0:
		why = " · warming up"
	case e.target != "":
		label += " · switching to " + embed.ModelName(e.target)
	case e.queueState == "paused":
		why = " · embedding paused by workspace setting"
	case e.failing:
		why = " · the provider is not answering"
	}
	h.set("App.semanticMark", "●")
	h.set("App.semanticDot", dot)
	h.set("App.semanticLabel", label)
	h.set("App.semanticDetail", label+why)
	h.set("App.searchTitle", "search · "+label+why)
}

// spinWhileEmbedding turns the spinner while the provider has texts to embed, and stops it after.
func (h *Host) spinWhileEmbedding() {
	if h.prog.spinning || h.prog.emb.working() == 0 || h.prog.emb.failing || h.prog.emb.queueState == "paused" || (h.prog.emb.queueState == "waiting" && h.prog.emb.waitingFor != "") {
		return
	}
	h.prog.spinning = true
	ep := h.epoch
	var turn func()
	turn = func() {
		if ep != h.epoch || h.prog.emb.working() == 0 || h.prog.emb.failing || h.prog.emb.queueState == "paused" || (h.prog.emb.queueState == "waiting" && h.prog.emb.waitingFor != "") {
			if ep == h.epoch {
				h.prog.spinning = false
			}
			return
		}
		h.prog.spin = (h.prog.spin + 1) % len(spinFrames)
		h.publishStatus()
		h.after(spinEvery, turn)
	}
	h.after(spinEvery, turn)
}

// bar is done of total as ten cells, then the numbers.
func bar(done, total int64) string {
	const width = 10
	done = min(max(done, 0), total)
	filled := int(done * width / max(total, 1))
	return fmt.Sprintf("%s%s %d/%d", strings.Repeat("█", filled), strings.Repeat("░", width-filled), done, total)
}

// progressText is the right slot's work: indexing as done of done+pending, then embedding (or a
// switch's fill) as the texts covered of all of them, the spinner turning while the model works.
// Each names the workspace it is for; while the daemon's one embedding queue serves another
// workspace, the text names that one, the workspace being processed.
func progressText(p progress) string {
	var parts []string
	ws := ""
	if p.ws != "" {
		ws = " " + p.ws
	}
	if p.pending > 0 {
		parts = append(parts, "indexing"+ws+" "+bar(p.docs, p.docs+p.pending))
	}
	e := p.emb
	if e.queueState == "paused" && (e.pending > 0 || e.working() > 0) {
		parts = append(parts, "embedding paused by workspace setting")
		return strings.Join(parts, " · ")
	}
	if left := e.working(); left > 0 {
		if e.queueState == "waiting" && e.waitingFor != "" {
			waits := "embedding waiting for " + e.waitingFor
			if p.ws != "" {
				waits = "embedding " + e.waitingFor + " · " + p.ws + " waits"
			}
			parts = append(parts, waits)
			return strings.Join(parts, " · ")
		}
		what := "embedding" + ws
		if e.target != "" {
			what = "switching" + ws + " to " + embed.ModelName(e.target)
		}
		mark := spinFrames[p.spin%len(spinFrames)]
		if e.failing {
			mark = "!" // waiting on the provider, not working
		}
		parts = append(parts, what+" "+mark+" "+bar(e.texts-left, e.texts))
	}
	return strings.Join(parts, " · ")
}

// publishStatus is the progress's toast, updated in place while there is work left; it ends when
// the work does (showProgress).
func (h *Host) publishStatus() {
	if h.prog.busy {
		if p := progressText(h.prog); p != "" {
			h.notifyOngoing(toastProgress, p)
		}
	}
}
