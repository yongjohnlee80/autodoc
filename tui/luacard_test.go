//go:build linux || darwin

package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// luacard_test.go: autodoc.ui.card, its tick, and :usage (ADR 1792256101 §4.4-§4.6, §5).

// heldAfter is the program's luaHost with its after held by the test: each fn waits in pending
// until the test runs it, so a cell orders the timers itself, with no sleep.
type heldAfter struct {
	luaHostOf
	mu      sync.Mutex
	pending []func()
	delays  []time.Duration
}

func (a *heldAfter) after(d time.Duration, fn func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = append(a.pending, fn)
	a.delays = append(a.delays, d)
}

func (a *heldAfter) lastDelay() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.delays[len(a.delays)-1]
}

func (a *heldAfter) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

// take removes and gives the i-th pending fn, to be run on the loop.
func (a *heldAfter) take(i int) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn := a.pending[i]
	a.pending = slices.Delete(a.pending, i, i+1)
	return fn
}

// holdCards makes the running program's card timers the test's.
func holdCards(t *testing.T, r *running) *heldAfter {
	t.Helper()
	return onLoop(r, func() *heldAfter {
		a := &heldAfter{luaHostOf: luaHostOf{r.h}}
		r.h.luaFeature().host = a
		return a
	})
}

func (r *running) cardOpen() bool {
	return onLoop(r, func() bool { return r.h.panelOpen[luaCardPanel] })
}

// ticks is the test script's tick count (its global ticks).
func (r *running) ticks() int {
	return onLoop(r, func() int {
		var v int
		fmt.Sscan(r.h.scripts.L.GetGlobal("ticks").String(), &v)
		return v
	})
}

// cardText is what the card's panel shows.
func (r *running) cardText() string {
	return onLoop(r, func() string {
		c, ok := r.h.p.Find(luaCardView)
		if !ok {
			return ""
		}
		e, ok := tuidecl.EditorOf(c)
		if !ok {
			return ""
		}
		return e.Core().Value()
	})
}

const tickingCard = `
ticks = 0
autodoc.command{ name = "tc", run = function(ed, args)
  autodoc.ui.card{ title = "ticking", text = "t", every = tonumber(args[1] or "1000"), tick = function(card)
    ticks = ticks + 1
    card:set("tick " .. ticks)
    if args[2] == "close" then card:close() end
  end }
end }`

func waitTicks(t *testing.T, r *running, n int) {
	t.Helper()
	r.s.WaitFor(t, fmt.Sprintf("%d ticks", n), func(string) bool { return r.ticks() == n })
}

// Lector r0 R1: open, take the pending timer, close, reopen, then run the old timer. Only the new
// opening's chain ticks or schedules; a chain without the opening epoch would tick twice here.
func TestACardsTicksAreOneOpeningsChain(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	held := holdCards(t, r)
	r.luaLoad(t, tickingCard)
	if err := r.luaRun("tc"); err != nil {
		t.Fatal(err)
	}
	waitTicks(t, r, 1)
	r.s.WaitForText(t, "tick 1")
	if held.count() != 1 {
		t.Fatalf("%d timers after the first tick, want 1", held.count())
	}
	old := held.take(0)
	onLoop(r, func() bool { r.h.closeDrawer(luaCardPanel); return true })
	r.s.WaitFor(t, "the card closed", func(string) bool { return !r.cardOpen() })
	onLoop(r, func() bool { r.h.keep(r.h.p.Call(luaCardPanel, "open")); return true })
	waitTicks(t, r, 2) // the reopening's chain: its first tick
	if held.count() != 1 {
		t.Fatalf("%d timers after the reopening's tick, want 1", held.count())
	}
	onLoop(r, func() bool { old(); return true }) // the first opening's timer, fired late
	if r.ticks() != 2 || held.count() != 1 {
		t.Fatalf("the old timer ticked (%d ticks) or scheduled (%d timers): two chains", r.ticks(), held.count())
	}
	next := held.take(0)
	onLoop(r, func() bool { next(); return true })
	if r.ticks() != 3 || held.count() != 1 {
		t.Fatalf("the live chain: %d ticks, %d timers; want 3 and 1", r.ticks(), held.count())
	}

	// a second close and reopen: still one chain
	stale := held.take(0)
	onLoop(r, func() bool { r.h.closeDrawer(luaCardPanel); return true })
	r.s.WaitFor(t, "closed again", func(string) bool { return !r.cardOpen() })
	onLoop(r, func() bool { r.h.keep(r.h.p.Call(luaCardPanel, "open")); return true })
	waitTicks(t, r, 4)
	onLoop(r, func() bool { stale(); return true })
	if r.ticks() != 4 || held.count() != 1 {
		t.Fatalf("after a second reopening: %d ticks, %d timers; want 4 and 1", r.ticks(), held.count())
	}
}

// A second card replaces the first: the first's handle sets nothing and its chain ends; :lua-reload
// ends the card and closes its panel; a tick that closes its card schedules nothing.
func TestACardIsReplacedEndedAndClosed(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	held := holdCards(t, r)
	r.luaLoad(t, tickingCard+`
autodoc.command{ name = "keep", run = function() first = autodoc.ui.card{ title = "first", text = "first text" } end }
autodoc.command{ name = "stale", run = function() first:set("stale text") end }`)
	if err := r.luaRun("keep"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "first text")
	if err := r.luaRun("tc"); err != nil {
		t.Fatal(err)
	}
	waitTicks(t, r, 1)
	if err := r.luaRun("stale"); err != nil {
		t.Fatal(err)
	}
	if got := r.cardText(); !strings.Contains(got, "tick 1") {
		t.Fatalf("a replaced card's set changed the panel: %q", got)
	}

	if err := r.luaRun("lua-reload"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the reload closed the card", func(string) bool { return !r.cardOpen() })
	held = holdCards(t, r) // the new engine's host

	r.luaLoad(t, tickingCard)
	if err := r.luaRun("tc 1000 close"); err != nil {
		t.Fatal(err)
	}
	waitTicks(t, r, 1)
	r.s.WaitFor(t, "the tick closed its card", func(string) bool { return !r.cardOpen() })
	if held.count() != 0 {
		t.Fatalf("a tick that closed its card scheduled %d more", held.count())
	}
}

// every under the floor ticks at the floor; nil never ticks.
func TestACardsTicksHaveAFloor(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	held := holdCards(t, r)
	r.luaLoad(t, tickingCard)
	for i, every := range []string{"0", "-5", "100"} {
		if err := r.luaRun("tc " + every); err != nil {
			t.Fatal(err)
		}
		r.s.WaitFor(t, "a timer for every "+every, func(string) bool { return held.count() == i+1 })
		if d := held.lastDelay(); d != cardEveryMin {
			t.Errorf("every %s: %v, want %v", every, d, cardEveryMin)
		}
	}
	// a tick and no every: no tick at all, and no timer (Lector PR r0 R2); then no tick and no every
	r.luaLoad(t, `autodoc.command{ name = "noevery", run = function()
  autodoc.ui.card{ text = "no every", tick = function(card) ticks = ticks + 100; card:set("ticked") end }
end }
autodoc.command{ name = "still", run = function() autodoc.ui.card{ text = "no tick" } end }`)
	before, ticksBefore := held.count(), r.ticks()
	if err := r.luaRun("noevery"); err != nil {
		t.Fatal(err)
	}
	if err := r.luaRun("still"); err != nil { // its card shown means the loop turned past any first tick
		t.Fatal(err)
	}
	r.s.WaitForText(t, "no tick")
	if held.count() != before || r.ticks() != ticksBefore {
		t.Errorf("a card without every: %d timers (were %d), ticks %d (were %d)", held.count(), before, r.ticks(), ticksBefore)
	}
}

// autodoc.app.stats answers the keys sys.stats answers, the app's pid among them.
func TestAppStatsAreKeyedAsSysStats(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	r.luaLoad(t, `
autodoc.command{ name = "keys", run = function()
  local function keys(t) local out = {} for k in pairs(t) do out[#out + 1] = k end table.sort(out) return table.concat(out, ",") end
  local app = autodoc.app.stats()
  autodoc.sys.stats(function(d, err) autodoc.notify("app " .. keys(app) .. " daemon " .. keys(d or {}) .. " " .. tostring(err)) end)
end }`)
	if err := r.luaRun("keys"); err != nil {
		t.Fatal(err)
	}
	const want = "cpu_seconds,cpus,go_heap,go_memory,goroutines,peak_rss,pid,rss,started_at,uptime,version"
	r.s.WaitFor(t, "both answered", func(string) bool { return r.noticed("app " + want + " daemon " + want + " nil") })
}

// :usage in a terminal: the tables, no chart; in a window that draws charts, two vega-lite fences
// whose JSON is a line chart of golib's subset.
func TestUsageShowsTablesAndChartsWhereDrawn(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	text := r.cardText
	if err := r.luaRun("usage"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the usage tables", func(string) bool {
		tx := text()
		// the cells as a reader reads them, whatever the padding
		return strings.Contains(tx, "### Daemon") && strings.Contains(tx, "### App") &&
			strings.Contains(strings.Join(strings.Fields(tx), " "), "| documents | 2 |")
	})
	if bad := misaligned(text()); bad != "" {
		t.Fatalf("a table's rows do not line up in a terminal:\n%s", bad)
	}
	if strings.Contains(text(), "```vega-lite") {
		t.Fatalf("a terminal's card has a chart: %s", text())
	}
	onLoop(r, func() bool { r.h.nativeViews = func() bool { return true }; return true })
	r.s.WaitFor(t, "the charts", func(string) bool { return strings.Count(text(), "```vega-lite") == 2 })
	for _, spec := range vegaFences(text()) {
		var v struct {
			Mark     string                     `json:"mark"`
			Data     struct{ Values []any }     `json:"data"`
			Encoding map[string]json.RawMessage `json:"encoding"`
		}
		if err := json.Unmarshal([]byte(spec), &v); err != nil {
			t.Fatalf("a chart is not JSON: %v\n%s", err, spec)
		}
		if v.Mark != "line" || v.Encoding["x"] == nil || v.Encoding["y"] == nil || v.Encoding["color"] == nil {
			t.Errorf("a chart outside the line subset: %s", spec)
		}
	}
}

// vegaFences are the vega-lite fences' sources in md.
func vegaFences(md string) []string {
	var out []string
	for _, part := range strings.Split(md, "```vega-lite\n")[1:] {
		if body, _, ok := strings.Cut(part, "\n```"); ok {
			out = append(out, body)
		}
	}
	return out
}
