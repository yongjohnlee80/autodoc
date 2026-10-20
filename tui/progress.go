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
		out = append(out, scanningFiles)
	}
	for _, r := range asList(m["warming"]) {
		if s, ok := r.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// scanningFiles is the reason a follower's first scan gives: AutoDoc's own, said in the UI's
// language (warmingText); the daemon's reasons are its own words, said as written.
const scanningFiles = "scanning the workspace's files"

// warmingText is reasons as the toast says them.
func (h *Host) warmingText(reasons []string) string {
	out := make([]string, len(reasons))
	for i, r := range reasons {
		if r == scanningFiles {
			r = h.tr("autodoc.notice.progress.scanning_the_workspace_s_files")
		}
		out[i] = r
	}
	return strings.Join(out, "; ")
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
	tk, ws := h.ticket(nil, TicketWorkspace), h.ws
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
		if !tk.Current() {
			return // another workspace or connection: its own poll is running
		}
		if a.err == nil {
			h.showWarming(a.warming)
			h.showProgress(a.docs, a.pending, a.emb, a.cursor)
		}
		// the next poll is this epoch's: a switch or a reconnect in the meantime has started its own
		h.after(progressEvery, func() {
			if tk.Current() {
				h.poll()
			}
		})
	})
}

// scheduled, when set (tests only), hears each delay h.after is given, on the loop: the seam that
// shows a consumer's wait without timing it. Set it before the host runs anything that schedules.
var scheduled func(time.Duration)

// after runs fn on the loop once d has passed, unless the program stopped.
func (h *Host) after(d time.Duration, fn func()) {
	if scheduled != nil {
		scheduled(d)
	}
	ctx := h.ctx
	go func() {
		// NOTE: a timer on a goroutine of its own, which posts fn to the loop: the loop never blocks on
		// a wait, and the host's ctx ends every pending one.
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
		h.notifyOngoing(toastWarming, h.trf("autodoc.notice.progress.warming_up", h.warmingText(reasons)))
	case was:
		h.notifyDone(toastWarming, h.tr("autodoc.notice.progress.ready_search_and_the_files_are_up_to_date"))
		h.listFiles()
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
		h.clearSearch(h.tr("autodoc.notice.progress.the_embedding_model_changed"))
	}
	if !polled || prev.semantic != emb.semantic || prev.model != emb.model || prev.target != emb.target || prev.on != emb.on || prev.pending != emb.pending || prev.targetPending != emb.targetPending {
		switch {
		case emb.target != "" || emb.semantic == "switching":
			h.notifyOngoing(toastSemantic, h.trf("autodoc.notice.progress.semantic_search_is_temporarily_words_only_while", h.ws))
		case polled && prev.target != "" && emb.target == "" && emb.on && emb.semantic != "off":
			h.notifyDone(toastSemantic, h.trf("autodoc.notice.progress.semantic_search_is_available_again_in", h.ws, emb.semantic))
		case polled && prev.target != "" && emb.target == "":
			h.notifyDone(toastSemantic, h.trf("autodoc.notice.progress.model_switch_ended_search_in_remains_words_only", h.ws))
		case polled && prev.pending > 0 && emb.pending == 0 && emb.semantic == "ready":
			h.notifyDone(toastSemantic, h.trf("autodoc.notice.progress.semantic_search_is_ready_in", h.ws))
		}
		if !modelChanged {
			h.refreshSearch()
		}
	}
	h.prog.busy = pending > 0 || emb.working() > 0 || (emb.queueState == "paused" && emb.pending > 0)
	if h.searchWaitToast && emb.working() == 0 {
		h.notifyDone(toastSearchWait, h.trf("autodoc.notice.progress.embedding_finished_search_is_refreshing_in", h.ws))
		h.searchWaitToast = false
	}
	if was && !h.prog.busy {
		h.notifyDone(toastProgress, h.trf("autodoc.notice.progress.indexed_files", docs))
	}
	if pending == 0 && (was || moved) {
		// the lists taken while indexing were partial, or the files changed since
		h.listFiles()
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
	dot, label, why := "red", h.tr("autodoc.text.progress.lexical_search"), ""
	switch {
	case e.online():
		dot, label = "green", h.tr("autodoc.text.progress.semantic_search")
		if e.model != "" {
			label = h.trf("autodoc.text.progress.semantic_search_model", embed.ModelName(e.model))
		}
		if e.refused > 0 {
			why = " · " + h.trf("autodoc.text.progress.refused_texts_see_vectors", e.refused)
		}
	case len(h.prog.warming) > 0:
		why = " · " + h.tr("autodoc.text.progress.warming_up")
	case e.target != "":
		label = h.trf("autodoc.text.progress.lexical_search_switching_to", embed.ModelName(e.target))
	case e.queueState == "paused":
		why = " · " + h.tr("autodoc.text.progress.embedding_paused_by_workspace_setting")
	case e.failing:
		why = " · " + h.tr("autodoc.text.progress.the_provider_is_not_answering")
	}
	h.set("App.semanticMark", "●")
	h.set("App.semanticDot", dot)
	h.set("App.semanticLabel", label)
	h.set("App.semanticDetail", label+why)
	h.set("App.searchTitle", h.trf("autodoc.text.progress.search", label, why))
}

// spinWhileEmbedding turns the spinner while the provider has texts to embed, and stops it after.
func (h *Host) spinWhileEmbedding() {
	if h.prog.spinning || h.prog.emb.working() == 0 || h.prog.emb.failing || h.prog.emb.queueState == "paused" || (h.prog.emb.queueState == "waiting" && h.prog.emb.waitingFor != "") {
		return
	}
	h.prog.spinning = true
	tk := h.ticket(nil, TicketWorkspace)
	var turn func()
	turn = func() {
		if !tk.Current() || h.prog.emb.working() == 0 || h.prog.emb.failing || h.prog.emb.queueState == "paused" || (h.prog.emb.queueState == "waiting" && h.prog.emb.waitingFor != "") {
			if tk.Current() {
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
// workspace, the text names that one, the workspace being processed. It is in t's language.
func progressText(t wording, p progress) string {
	var parts []string
	if p.pending > 0 {
		done := bar(p.docs, p.docs+p.pending)
		if p.ws != "" {
			parts = append(parts, t.Trf("autodoc.notice.progress.indexing_workspace", p.ws, done))
		} else {
			parts = append(parts, t.Trf("autodoc.notice.progress.indexing", done))
		}
	}
	e := p.emb
	if e.queueState == "paused" && (e.pending > 0 || e.working() > 0) {
		parts = append(parts, t.Tr("autodoc.notice.progress.embedding_paused_by_workspace_setting"))
		return strings.Join(parts, " · ")
	}
	if left := e.working(); left > 0 {
		if e.queueState == "waiting" && e.waitingFor != "" {
			waits := t.Trf("autodoc.notice.progress.embedding_waiting_for", e.waitingFor)
			if p.ws != "" {
				waits = t.Trf("autodoc.notice.progress.embedding_waits", e.waitingFor, p.ws)
			}
			parts = append(parts, waits)
			return strings.Join(parts, " · ")
		}
		mark := spinFrames[p.spin%len(spinFrames)]
		if e.failing {
			mark = "!" // waiting on the provider, not working
		}
		done := bar(e.texts-left, e.texts)
		var what string
		switch {
		case e.target != "" && p.ws != "":
			what = t.Trf("autodoc.notice.progress.switching_workspace_to", p.ws, embed.ModelName(e.target), mark, done)
		case e.target != "":
			what = t.Trf("autodoc.notice.progress.switching_to", embed.ModelName(e.target), mark, done)
		case p.ws != "":
			what = t.Trf("autodoc.notice.progress.embedding_workspace", p.ws, mark, done)
		default:
			what = t.Trf("autodoc.notice.progress.embedding", mark, done)
		}
		parts = append(parts, what)
	}
	return strings.Join(parts, " · ")
}

// publishStatus is the progress's toast, updated in place while there is work left; it ends when
// the work does (showProgress).
func (h *Host) publishStatus() {
	if h.prog.busy {
		if p := progressText(core{h}, h.prog); p != "" {
			h.notifyOngoing(toastProgress, p)
		}
	}
}
