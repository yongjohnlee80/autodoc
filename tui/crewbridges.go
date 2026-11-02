package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// CREW BRIDGES — System › Crew Bridges…, a feature of its own: the crew's helper processes on this
// machine (crew.bridges: agy and Codex bridges, Codex servers, Codex's shared daemon), each with its
// owner and whether it is orphaned, listed again every few seconds while the dialog is open. Kill
// ends the row under the cursor and Kill orphans… every orphan, each with its own crew.bridge_kill
// under the scope the window holds: a live helper's kill asks first, naming the agent it cuts off,
// and Codex's shared daemon's says that any Codex outside the crew uses it. The daemon reclaims an
// orphan by itself after 10 minutes; its row says when (docs/decisions/crew.md#crew-bridges).

// crewBridgesEvery is how often the open dialog lists the helpers again.
const crewBridgesEvery = 3 * time.Second

// bridgesCore is what Crew Bridges needs of the program.
type bridgesCore interface {
	Call(ctx context.Context, verb string, params ...any) (any, error)
	Go(fn func(context.Context) any, then func(any))
	After(d time.Duration, fn func())
	Set(name string, v any)
	Open(id string)
	Ask(q Question) Asked
	Notify(msg string)
	Tr(id string) string
	Trf(id string, args ...any) string
	CopyText(text string) bool
}

// crewBridgeRow is one helper, as crew.bridges lists it.
type crewBridgeRow struct {
	kind, start, socket, session, owner, host, state, why string
	pid, started, reclaimAt                               int64
	argv                                                  []string
}

// crewBridges is the dialog. Its fields are the loop's.
type crewBridges struct {
	c      bridgesCore
	host   *crewHost // the scope the window holds, whose run token a kill is sent with
	rows   *rows[crewBridgeRow]
	shown  bool   // the dialog is open
	gen    uint64 // bumped by each open and close: an earlier opening's refreshes stop
	cursor int
	now    func() time.Time
}

func newCrewBridges(c bridgesCore, host *crewHost) *crewBridges {
	b := &crewBridges{c: c, host: host, now: time.Now}
	b.rows = newRows(tuidecl.NewListModel("key", "kind", "owner", "host", "pid", "started", "state"), b.rowOf)
	return b
}

func (*crewBridges) Name() string { return "crewBridges" }

func (b *crewBridges) State() map[string]any {
	return map[string]any{
		"App.crewBridges":       b.rows.Model(),
		"App.crewBridgesStatus": "",
		"App.crewBridgesDetail": "",
	}
}

func (b *crewBridges) Commands() map[string]Command {
	return map[string]Command{
		"App.openCrewBridges":        none(b.open),
		"App.crewBridgesClosed":      none(b.closed),
		"App.crewBridgeShown":        oneNumber("a row", b.showDetail),
		"App.crewBridgeKill":         oneNumber("a row", b.kill),
		"App.crewBridgesKillOrphans": none(b.killOrphans),
		"App.crewBridgeCopy":         oneNumber("a row", b.copy),
	}
}

// Catalog is the dialog's user command (catalog.go): it runs what App.openCrewBridges runs.
func (b *crewBridges) Catalog() []Entry {
	return []Entry{builtin("crew.bridges", "crew bridges", "App.openCrewBridges()", b.open)}
}

// open lists the helpers, opens the dialog, and lists them again every crewBridgesEvery while it
// stays open.
func (b *crewBridges) open() {
	b.gen++
	b.shown = true
	b.cursor = 0
	b.c.Set("App.crewBridgesStatus", b.c.Tr("autodoc.text.crew_bridges.listing"))
	b.c.Set("App.crewBridgesDetail", "")
	b.load()
	b.c.Open("crewBridges")
	b.refreshLater(b.gen)
}

// closed is the dialog dismissed: its refreshes stop.
func (b *crewBridges) closed() {
	b.shown = false
	b.gen++
}

func (b *crewBridges) refreshLater(gen uint64) {
	b.c.After(crewBridgesEvery, func() {
		if !b.shown || b.gen != gen {
			return
		}
		b.load()
		b.refreshLater(gen)
	})
}

// load lists the helpers; an answer for an earlier opening, or after the dialog closed, is dropped.
func (b *crewBridges) load() {
	gen := b.gen
	b.c.Go(func(ctx context.Context) any {
		res, err := b.c.Call(ctx, "crew.bridges")
		if err != nil {
			return err
		}
		var out []crewBridgeRow
		for _, x := range asList(res) {
			m := asMap(x)
			r := crewBridgeRow{kind: str(m, "kind"), start: str(m, "start"), socket: str(m, "socket"), session: str(m, "session"),
				owner: str(m, "owner"), host: str(m, "host"), state: str(m, "state"), why: str(m, "why"),
				pid: num(m, "pid"), started: num(m, "started"), reclaimAt: num(m, "reclaim_at")}
			for _, a := range asList(m["argv"]) {
				if s, ok := a.(string); ok {
					r.argv = append(r.argv, s)
				}
			}
			out = append(out, r)
		}
		return out
	}, func(v any) {
		if !b.shown || b.gen != gen {
			return
		}
		if err, ok := v.(error); ok {
			b.rows.Reset(nil)
			b.c.Set("App.crewBridgesDetail", "")
			b.c.Set("App.crewBridgesStatus", b.listFailed(err))
			return
		}
		rows, _ := v.([]crewBridgeRow)
		b.rows.Reset(rows)
		b.c.Set("App.crewBridgesStatus", b.statusOf(rows))
		b.showDetail(b.cursor)
	})
}

// listFailed says why crew.bridges listed nothing: a daemon that can't read the machine's processes
// (only Linux can yet) or one older than the verb, in words; any other failure as the daemon put it.
func (b *crewBridges) listFailed(err error) string {
	switch code(err) {
	case crew.CodeNoBridges:
		return b.c.Tr("autodoc.text.crew_bridges.unsupported")
	case golibrpc.CodeMethodNotFound:
		return b.c.Tr("autodoc.text.crew_bridges.older_daemon")
	}
	return b.c.Trf("autodoc.text.crew_bridges.not_listed", wireMessage(err))
}

// statusOf is the line under the list: how many helpers, and how many orphans.
func (b *crewBridges) statusOf(rows []crewBridgeRow) string {
	if len(rows) == 0 {
		return b.c.Tr("autodoc.text.crew_bridges.none")
	}
	orphans := 0
	for _, r := range rows {
		if r.state == "orphan" {
			orphans++
		}
	}
	return b.c.Trf("autodoc.text.crew_bridges.count", len(rows), orphans)
}

// rowOf is a helper as the list shows it: an orphan's state says when the daemon reclaims it.
func (b *crewBridges) rowOf(_ int, r crewBridgeRow) rowOf {
	state := r.state
	if r.state == "orphan" && r.reclaimAt > 0 {
		left := max(time.Unix(r.reclaimAt, 0).Sub(b.now()).Round(time.Minute), 0)
		state = b.c.Trf("autodoc.text.crew_bridges.orphan_reclaimed_in", int(left/time.Minute))
	}
	owner := r.owner
	if owner == "" {
		owner = "—"
	}
	return rowOf{"key": fmt.Sprint(r.pid) + "@" + r.start, "kind": r.kind, "owner": owner, "host": r.host,
		"pid": fmt.Sprint(r.pid), "started": time.Unix(r.started, 0).Format("Jan 2 15:04"), "state": state}
}

// showDetail shows row i's command line, socket, session and why under the list.
func (b *crewBridges) showDetail(i int) {
	b.cursor = i
	r, ok := b.rows.At(i)
	if !ok {
		b.c.Set("App.crewBridgesDetail", "")
		return
	}
	b.c.Set("App.crewBridgesDetail", b.detailOf(r))
}

func (b *crewBridges) detailOf(r crewBridgeRow) string {
	dash := func(s string) string {
		if s == "" {
			return "—"
		}
		return s
	}
	return b.c.Trf("autodoc.text.crew_bridges.detail", strings.Join(r.argv, " "), dash(r.socket), dash(r.session), dash(r.why))
}

// copy puts row i's details on the clipboard.
func (b *crewBridges) copy(i int) {
	r, ok := b.rows.At(i)
	if !ok {
		return
	}
	text := fmt.Sprintf("%s %d (%s)\n%s", r.kind, r.pid, r.state, b.detailOf(r))
	if b.c.CopyText(text) {
		b.c.Notify(b.c.Trf("autodoc.notice.crew_bridges.copied", r.kind, r.pid))
	}
}

// kill ends row i: an orphan at once; a live helper, Codex's shared daemon, or a helper not of this
// store's sessions once the user agrees.
func (b *crewBridges) kill(i int) {
	r, ok := b.rows.At(i)
	if !ok {
		return
	}
	switch r.state {
	case "live":
		b.c.Ask(Question{Title: b.c.Tr("autodoc.question.crew_bridges.kill_title"),
			Text:  b.c.Trf("autodoc.question.crew_bridges.kill_live", r.kind, r.pid, r.owner),
			OnYes: func() { b.killRow(r, true) }})
	case "foreign":
		text := b.c.Trf("autodoc.question.crew_bridges.kill_foreign", r.pid)
		if r.why == crew.ForeignUnowned { // not this store's: perhaps another AutoDoc's agent's
			text = b.c.Trf("autodoc.question.crew_bridges.kill_unowned", r.kind, r.pid)
		}
		b.c.Ask(Question{Title: b.c.Tr("autodoc.question.crew_bridges.kill_title"), Text: text,
			OnYes: func() { b.killRow(r, true) }})
	default:
		b.killRow(r, false)
	}
}

// crewBridgeSurvivors is a kill's incomplete outcome: how many of its processes still ran.
type crewBridgeSurvivors int

// killOrphans asks, then ends every orphan listed, each by its own crew.bridge_kill: the daemon
// checks each again as it ends it.
func (b *crewBridges) killOrphans() {
	var orphans []crewBridgeRow
	for _, r := range b.rows.items {
		if r.state == "orphan" {
			orphans = append(orphans, r)
		}
	}
	if len(orphans) == 0 {
		b.c.Set("App.crewBridgesStatus", b.c.Tr("autodoc.text.crew_bridges.no_orphans"))
		return
	}
	b.c.Ask(Question{Title: b.c.Tr("autodoc.question.crew_bridges.kill_orphans_title"),
		Text: b.c.Trf("autodoc.question.crew_bridges.kill_orphans", len(orphans)),
		OnYes: func() {
			for _, r := range orphans {
				b.killRow(r, false)
			}
		}})
}

// killRow sends crew.bridge_kill for r under the scope the window holds, says how it ended, and
// lists the helpers again.
func (b *crewBridges) killRow(r crewBridgeRow, confirmed bool) {
	scope, epoch, ok := b.host.held()
	if !ok {
		b.c.Set("App.crewBridgesStatus", b.c.Tr("autodoc.text.crew_bridges.no_scope"))
		return
	}
	run := b.host.run
	b.c.Go(func(ctx context.Context) any {
		res, err := b.c.Call(ctx, "crew.bridge_kill", scope, run, epoch, r.pid, r.start, confirmed)
		if err != nil {
			return err
		}
		m := asMap(res)
		if o := str(m, "outcome"); o != crew.KilledIncomplete {
			return o
		}
		return crewBridgeSurvivors(num(m, "survivors"))
	}, func(v any) {
		if n, ok := v.(crewBridgeSurvivors); ok { // some of it still ran after SIGKILL: said, never claimed ended
			b.c.Notify(b.c.Trf("autodoc.notice.crew_bridges.incomplete", r.kind, r.pid, int(n)))
			if b.shown {
				b.load()
			}
			return
		}
		switch v {
		case "term":
			b.c.Notify(b.c.Trf("autodoc.notice.crew_bridges.ended", r.kind, r.pid))
		case "kill":
			b.c.Notify(b.c.Trf("autodoc.notice.crew_bridges.killed", r.kind, r.pid))
		case "gone":
			b.c.Notify(b.c.Trf("autodoc.notice.crew_bridges.gone", r.kind, r.pid))
		default:
			err, ok := v.(error)
			if !ok {
				err = fmt.Errorf("the daemon answered %v", v)
			}
			b.c.Notify(b.c.Trf("autodoc.notice.crew_bridges.not_killed", r.kind, r.pid, wireMessage(err)))
		}
		if b.shown {
			b.load()
		}
	})
}
