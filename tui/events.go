package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// PEER CHANGES — what other clients of the daemon changed (ADR 0212 §7).
//
// The daemon logs every configuration and lifecycle change with the token of the client that made
// it; this TUI follows the log (sys.events) once a second, as long as its connection lasts. A
// change another client made, or the daemon saw itself (a schema file edited), refreshes what it
// touches and says so once, in a notification: focus stays where it is, and the file in the editor
// is never replaced. This TUI's own changes are not announced back to it. A cursor the log no
// longer covers is answered with a snapshot (the workspaces listed again) and the log's head.

// eventsEvery is how often the log is asked; eventsPage how much of it at once.
const (
	eventsEvery = time.Second
	eventsPage  = 100
)

type peerEvent struct {
	seq                             int64
	kind, workspace, client, detail string
}

// followEvents starts following the log from where this connection's hello said it was.
func (h *Host) followEvents() {
	h.evCursor = h.session.EventsHead()
	h.pollEvents(h.session.Gen())
}

// pollEvents asks for the events after the cursor, applies them, and asks again: at once while
// more wait, else a second later, while the connection gen lasts.
func (h *Host) pollEvents(gen uint64) {
	since := h.evCursor
	type answer struct {
		events []peerEvent
		cursor int64
		more   bool
		err    error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "sys.events", since, int64(eventsPage))
		var re *golibrpc.Error
		if errors.As(err, &re) && re.Code == rpc.CodeCursorExpired {
			// too far behind (or another store): the head, after a snapshot
			res, err = h.call(ctx, "sys.events", int64(-1), int64(1))
			if err != nil {
				return answer{err: err}
			}
			return answer{cursor: num(asMap(res), "cursor"), err: errEventsExpired}
		}
		if errors.As(err, &re) && re.Code == rpc.CodeUnsupported {
			return answer{err: errNoEventLog}
		}
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		a := answer{cursor: num(m, "cursor")}
		a.more, _ = m["more"].(bool)
		for _, e := range asList(m["events"]) {
			em := asMap(e)
			a.events = append(a.events, peerEvent{seq: num(em, "seq"), kind: str(em, "kind"), workspace: str(em, "workspace"),
				client: str(em, "client"), detail: str(em, "detail")})
		}
		return a
	}, func(a answer) {
		if h.session.Gen() != gen {
			return // the connection ended: the next one follows from its own hello
		}
		switch {
		case errors.Is(a.err, errEventsExpired):
			h.evCursor = a.cursor
			h.loadWorkspaces()
		case errors.Is(a.err, errNoEventLog):
			return // a server that keeps no event log: nothing to follow
		case a.err != nil:
			// a failed poll is retried; the cursor stays
		default:
			h.evCursor = a.cursor
			h.applyEvents(a.events)
		}
		if a.more && a.err == nil {
			h.pollEvents(gen)
			return
		}
		h.after(eventsEvery, func() {
			if h.session.Gen() == gen {
				h.pollEvents(gen)
			}
		})
	})
}

var (
	errEventsExpired = errors.New("tui: the event cursor expired")
	errNoEventLog    = errors.New("tui: the server keeps no event log")
)

// applyEvents refreshes what other clients' changes touched and tells of each, once.
func (h *Host) applyEvents(events []peerEvent) {
	self := h.session.Client()
	var notices []string
	relist, recheck, reranked := false, false, false
	for _, e := range events {
		if e.client == self && self != "" {
			continue // this TUI's own change: it already knows
		}
		by := "by another client"
		if e.client == "" {
			by = "on disk"
		}
		var notice string
		switch e.kind {
		case "workspace.renamed":
			if e.workspace == h.ws && e.detail != "" {
				h.ws = e.detail // the same root and index under its new name: nothing to reopen
				h.setWhere(fmt.Sprintf("autodoc %s · %s", h.session.Version(), h.ws))
			}
			notice = fmt.Sprintf("workspace %s was renamed %s %s", e.workspace, e.detail, by)
			relist = true
		case "workspace.removed":
			if e.workspace == h.ws {
				h.keepDraftOnLeave()
			}
			notice = fmt.Sprintf("workspace %s was removed %s", e.workspace, by)
			relist = true
		case "workspace.added":
			notice, relist = fmt.Sprintf("workspace %s was added %s", e.workspace, by), true
		case "workspace.patterns":
			notice, relist = fmt.Sprintf("workspace %s: its rules were changed %s; reconciling", e.workspace, by), true
		case "workspace.schema":
			notice, relist = fmt.Sprintf("workspace %s: its frontmatter schema changed %s", e.workspace, by), true
			recheck = recheck || e.workspace == h.ws
		case "workspace.text_extensions":
			notice, relist = fmt.Sprintf("workspace %s: its text types were changed %s", e.workspace, by), true
		case "workspace.section_size", "workspace.embedding_policy", "workspace.provider", "workspace.databases":
			what := map[string]string{"workspace.section_size": "section size", "workspace.embedding_policy": "embedding policy",
				"workspace.provider": "embedding provider", "workspace.databases": "database settings"}[e.kind]
			notice, relist = fmt.Sprintf("workspace %s: its %s was changed %s", e.workspace, what, by), true
		case "embedding.switched":
			notice = fmt.Sprintf("embedding model switched to '%s' %s; files are re-indexing in the background, and search stays available", e.detail, by)
		case "embedding.cancelled":
			notice = "the model switch was cancelled " + by
		case "embedding.providers":
			notice = "the embedding providers were changed " + by
		case "ranker.switched":
			notice, reranked = "the ranker in use was changed "+by, true
		case "ranker.providers":
			notice, reranked = "the rankers were changed "+by, true
		}
		if notice != "" && !slices.Contains(notices, notice) {
			notices = append(notices, notice)
		}
	}
	for _, n := range notices {
		h.notify(n)
	}
	if reranked && h.aiTab == rankerTab {
		h.loadRankers()
	}
	if reranked {
		h.loadStageRanker(true)
	}
	if relist {
		h.loadWorkspaces()
	}
	if recheck {
		h.validateSoon()
	}
}

// keepDraftOnLeave keeps the file's unsaved text when its workspace is about to go: the file
// becomes the untitled draft (its path names a workspace that no longer exists), and the next
// workspace entered keeps it instead of starting a blank page.
func (h *Host) keepDraftOnLeave() {
	if !h.file.dirty {
		return
	}
	h.file.open, h.file.path, h.file.version = false, "", ""
	h.keepDraft = true
	h.set("App.fileTitle", untitled)
	h.setDirty(true)
	h.notify("the unsaved text is kept as an untitled draft: Ctrl+S names it in a workspace")
}
