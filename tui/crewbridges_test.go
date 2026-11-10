package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// bridgesWindow is Crew Bridges' program: crew.bridges answers rows, crew.bridge_kill is recorded
// and answers outcome, timers wait until fired, questions are kept for the cell to answer.
type bridgesWindow struct {
	rows      []any
	listErr   error // what crew.bridges answers instead of rows, when set
	outcome   string
	survivors int64 // with outcome incomplete
	kills     [][]any
	listed    int // crew.bridges calls
	timers    []func()
	state     map[string]any
	opened    []string
	asked     []feature.Question
	notices   []string
	copied    string
}

func (w *bridgesWindow) Call(_ context.Context, verb string, p ...any) (any, error) {
	switch verb {
	case "crew.bridges":
		w.listed++
		if w.listErr != nil {
			return nil, w.listErr
		}
		return w.rows, nil
	case "crew.bridge_kill":
		w.kills = append(w.kills, p)
		return map[string]any{"outcome": w.outcome, "survivors": w.survivors}, nil
	}
	return nil, errors.New("unknown method: " + verb)
}
func (w *bridgesWindow) Go(fn func(context.Context) any, then func(any)) {
	then(fn(context.Background()))
}
func (w *bridgesWindow) After(_ time.Duration, fn func()) { w.timers = append(w.timers, fn) }
func (w *bridgesWindow) Set(name string, v any)           { w.state[name] = v }
func (w *bridgesWindow) Open(id string)                   { w.opened = append(w.opened, id) }
func (w *bridgesWindow) Ask(q feature.Question) feature.Asked {
	w.asked = append(w.asked, q)
	return feature.Asked{}
}
func (w *bridgesWindow) Notify(msg string)   { w.notices = append(w.notices, msg) }
func (w *bridgesWindow) Tr(id string) string { return w.Trf(id) }
func (w *bridgesWindow) Trf(id string, args ...any) string {
	return feature.Fill(englishText(id), args...)
}
func (w *bridgesWindow) CopyText(text string) bool { w.copied = text; return true }

func (w *bridgesWindow) fire() {
	ts := w.timers
	w.timers = nil
	for _, f := range ts {
		f()
	}
}

// bridgeWire is a row as crew.bridges answers it on the wire.
func bridgeWire(kind string, pid int64, state, owner, why string, reclaimAt int64) map[string]any {
	return map[string]any{"kind": kind, "pid": pid, "start": "boot:ns:" + strconv.FormatInt(pid, 10) + ":77", "started": int64(1793500000),
		"argv": []any{"/opt/autodoc/autodoc", "crew", "bridge", "--agy", "--foreground", "--session", "s-1"}, "session": "s-1",
		"owner": owner, "host": "autodoc:w", "state": state, "why": why, "reclaim_at": reclaimAt}
}

// newBridgesCell is Crew Bridges over w, the window holding its scope on a real crew.
func newBridgesCell(t *testing.T, w *bridgesWindow) (*crewBridges, *crewHost) {
	t.Helper()
	w.state = map[string]any{}
	host := newCrewHost(&crewWindow{d: newCrewDaemonForHost()}, nil, t.TempDir())
	host.Connected()
	if _, _, ok := host.held(); !ok {
		t.Fatal("the window holds no scope")
	}
	return newCrewBridges(w, host), host
}

// Crew Bridges (docs/decisions/crew.md#crew-bridges): an orphan's Kill is sent at once, unconfirmed,
// with the window's scope, run token and epoch, and the helper's pid and start; a live helper's
// asks first, naming the agent it cuts off, and goes confirmed on Yes; Codex's shared daemon's asks
// too. Kill orphans… asks once, then kills each orphan with its own call. An orphan's row says when
// the daemon reclaims it.
func TestCrewBridgesKillAnOrphanAtOnceAndAskForALiveOne(t *testing.T) {
	now := time.Unix(1793600000, 0)
	w := &bridgesWindow{outcome: "term", rows: []any{
		bridgeWire("agy-bridge", 101, "orphan", "", "session ended", now.Add(7*time.Minute).Unix()),
		bridgeWire("codex-prompt-bridge", 102, "live", "cloud@autodoc:w", "", 0),
		bridgeWire("codex-shared-daemon", 103, "foreign", "", "", 0),
		bridgeWire("codex-server", 104, "orphan", "", "no endpoint names its socket", 0),
	}}
	b, host := newBridgesCell(t, w)
	b.now = func() time.Time { return now }
	b.open()
	if len(w.opened) != 1 || w.opened[0] != "crewBridges" {
		t.Fatalf("opened %v, want the dialog", w.opened)
	}
	if got := w.state["App.crewBridgesStatus"]; !strings.Contains(got.(string), "4 helper(s), 2 orphaned") {
		t.Fatalf("the status %q", got)
	}
	if row := b.rowOf(0, b.rows.Items[0]); row["state"] != "orphan, ends in 7 min" || row["owner"] != "—" {
		t.Fatalf("an orphan's row %v, want when it is reclaimed", row)
	}
	scope, epoch, _ := host.held()

	b.kill(0) // the orphan: no question
	if len(w.asked) != 0 || len(w.kills) != 1 {
		t.Fatalf("an orphan's kill: asked %d, sent %v; want it sent at once", len(w.asked), w.kills)
	}
	if k := w.kills[0]; k[0] != scope || k[1] != host.run || k[2] != epoch || k[3] != int64(101) || k[4] != "boot:ns:101:77" || k[5] != false {
		t.Fatalf("the orphan's crew.bridge_kill %v, want scope %s, the run token, epoch %d, pid 101, its start, unconfirmed", k, scope, epoch)
	}
	if last := w.notices[len(w.notices)-1]; last != "agy-bridge 101 ended, with every process it held (one forked after its leader went isn't tracked)" {
		t.Fatalf("the notice %q", last)
	}

	b.kill(1) // the live one: asked, naming its agent
	if len(w.kills) != 1 || len(w.asked) != 1 || !strings.Contains(w.asked[0].Text, "cloud@autodoc:w") {
		t.Fatalf("a live helper's kill: sent %d, asked %+v; want a question naming cloud@autodoc:w first", len(w.kills), w.asked)
	}
	w.asked[0].OnYes()
	if k := w.kills[1]; k[3] != int64(102) || k[5] != true {
		t.Fatalf("the live helper's kill after Yes: %v, want confirmed", k)
	}

	b.kill(2) // Codex's shared daemon: asked, saying what else uses it
	if len(w.asked) != 2 || !strings.Contains(w.asked[1].Text, "any Codex running outside the crew") {
		t.Fatalf("the shared daemon's kill asked %+v", w.asked)
	}

	b.killOrphans()
	if len(w.asked) != 3 || !strings.Contains(w.asked[2].Text, "End 2 orphaned helper(s)?") {
		t.Fatalf("Kill orphans… asked %+v", w.asked)
	}
	sent := len(w.kills)
	w.asked[2].OnYes()
	if got := w.kills[sent:]; len(got) != 2 || got[0][3] != int64(101) || got[1][3] != int64(104) || got[0][5] != false {
		t.Fatalf("Kill orphans sent %v, want one unconfirmed kill per orphan", got)
	}
}

// Crew Bridges lists the helpers again every few seconds while it is open, and stops once it is
// closed; a kill with no scope held says so and sends nothing.
func TestCrewBridgesRefreshOnlyWhileOpen(t *testing.T) {
	w := &bridgesWindow{rows: []any{bridgeWire("agy-bridge", 101, "orphan", "", "session ended", 0)}}
	b, host := newBridgesCell(t, w)
	b.open()
	w.fire()
	w.fire()
	if w.listed != 3 {
		t.Fatalf("listed %d times after two refreshes, want 3", w.listed)
	}
	b.closed()
	w.fire()
	w.fire()
	if w.listed != 3 || len(w.timers) != 0 {
		t.Fatalf("after the dialog closed: listed %d times, %d timers left; want no more", w.listed, len(w.timers))
	}
	b.copy(0)
	if !strings.Contains(w.copied, "agy-bridge 101") || !strings.Contains(w.copied, "--session s-1") || !strings.Contains(w.copied, "session ended") {
		t.Fatalf("copied %q, want the row's kind, pid, command line and why", w.copied)
	}
	host.scope = "" // as before the first claim
	b.kill(0)
	if len(w.kills) != 0 || !strings.Contains(w.state["App.crewBridgesStatus"].(string), "holds no crew scope") {
		t.Fatalf("a kill with no scope: sent %v, status %q", w.kills, w.state["App.crewBridgesStatus"])
	}
}

// bridgeCellEnv makes this test binary a stand-in crew helper (TestMain): it waits to be signalled.
const bridgeCellEnv = "AUTODOC_TUI_BRIDGE_CELL"

func bridgeCell() {
	for {
		time.Sleep(time.Hour)
	}
}

// A daemon that can't read the machine's processes (only Linux can yet), or one older than Crew
// Bridges, is said so in words, never as an empty list or a bare code; any other failure as the
// daemon put it.
func TestCrewBridgesSayWhyNothingIsListed(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&golibrpc.Error{Code: crew.CodeNoBridges, Message: "crew: listing the crew's helper processes needs Linux"}, "on Linux only for now"},
		{&golibrpc.Error{Code: golibrpc.CodeMethodNotFound, Message: "unknown method: crew.bridges"}, "older than Crew Bridges"},
		{errors.New("the store is unavailable"), "not listed: the store is unavailable"},
	} {
		w := &bridgesWindow{listErr: c.err}
		b, _ := newBridgesCell(t, w)
		b.open()
		if got := w.state["App.crewBridgesStatus"].(string); !strings.Contains(got, c.want) || strings.Contains(got, "-32") {
			t.Errorf("crew.bridges answering %v: the status %q, want it to say %q", c.err, got, c.want)
		}
	}
}

// The daemon's reclaim gave up on an orphan its kills left running: its row says so, with how many
// still run, and that it's the user's to end. A kill that left processes running says so too,
// never "ended".
func TestCrewBridgesSayWhatTheKillsLeftRunning(t *testing.T) {
	now := time.Unix(1793600000, 0)
	stopped := bridgeWire("agy-bridge", 101, "orphan", "", "session ended", 0)
	stopped["reclaim_stopped"], stopped["survivors"] = true, int64(2)
	w := &bridgesWindow{outcome: "incomplete", rows: []any{stopped}}
	w.survivors = 2
	b, _ := newBridgesCell(t, w)
	b.now = func() time.Time { return now }
	b.open()
	if row := b.rowOf(0, b.rows.Items[0]); row["state"] != "orphan, 2 still running: the daemon stopped trying, it's yours to end" {
		t.Fatalf("the orphan the daemon gave up on: %v", row)
	}
	b.kill(0)
	if last := w.notices[len(w.notices)-1]; last != "agy-bridge 101: 2 of its processes still ran after it was killed" {
		t.Fatalf("an incomplete kill's notice %q", last)
	}
}
