package tui

import (
	"cmp"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"

	"github.com/yongjohnlee80/autodoc/core/procstat"
	"github.com/yongjohnlee80/autodoc/rpc"
	lua "github.com/yuin/gopher-lua"
)

// A SCRIPT'S CARD — autodoc.ui.card, autodoc.ui.charts and autodoc.app.stats (ADR 1792256101 §4.3,
// §4.4):
//
//	autodoc.ui.card{title=, text=, every=, tick=}   the luaCard panel shows text (Markdown, drawn
//	                                                Rendered in the GUI, a vega-lite fence a chart);
//	                                                answers the card's handle
//	card:set(text)     the panel shows text now
//	card:close()       the panel closes and the card ends: set and close then do nothing
//	card:open()        true while this is the panel's card and the panel is open
//	autodoc.ui.charts()   whether this window draws a vega-lite fence as a chart
//	autodoc.ui.width(s)   the terminal columns s takes, for padding text to line up
//	autodoc.app.stats()   the app's own process, keyed as sys.stats answers the daemon's
//
// One panel shows one card: a new card replaces the one shown, whose handle is then dead. tick(card)
// runs every `every` ms (at least cardEveryMin) while the panel is open. A chain of ticks belongs to
// one opening of the panel, not to the card: cardEpoch moves at every Opened, Closed and end, and a
// timer armed in an older epoch fires, finds it gone, and schedules nothing. So a close and a reopen
// before the old timer fires never leave two chains (Lector r0 R1).

const (
	luaCardPanel = "luaCard"
	luaCardView  = "luaCardView"
	luaCardType  = "autodoc.card" // the handle's metatable
)

// cardEveryMin is the shortest interval between ticks: a smaller or non-positive every is this.
const cardEveryMin = 250 * time.Millisecond

// luaCard is the one card a script shows. Loop-owned, as the engine is.
type luaCard struct {
	gen   uint64        // this card's number: a handle of a card since replaced acts on nothing
	every time.Duration // 0: no tick
	tick  *lua.LFunction
	ud    *lua.LUserData // the handle tick is given
}

// cardHost is what a card needs of the program, beside luaHost's.
type cardHost interface {
	openPanel(id string)
	closePanel(id string)
	panelOpen(id string) bool
	after(d time.Duration, fn func())
	appStats() procstat.Stats
	appVersion() string
	native() bool
}

// bindCard gives the engine ui.card, ui.charts and app.stats: ui is the engine's autodoc.ui.
func (s *scripting) bindCard(L *lua.LState, ui *lua.LTable) {
	L.SetField(ui, "card", L.NewFunction(s.luaCard))
	L.SetField(ui, "charts", L.NewFunction(s.luaCharts))
	L.SetField(ui, "width", L.NewFunction(luaWidth))
	app := L.NewTable()
	L.SetField(app, "stats", L.NewFunction(s.luaAppStats))
	L.SetField(s.eng.Global(), "app", app)
	mt := L.NewTypeMetatable(luaCardType)
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"set": s.cardSet, "close": s.cardClose, "open": s.cardIsOpen}))
	L.SetField(mt, "__metatable", lua.LString("locked"))
}

func (s *scripting) cardHost() cardHost {
	ch, _ := s.host.(cardHost)
	return ch
}

func (s *scripting) luaCard(L *lua.LState) int {
	spec := L.CheckTable(1)
	tick, _ := spec.RawGetString("tick").(*lua.LFunction)
	every := cardEvery(spec.RawGetString("every"))
	if tick == nil || every == 0 {
		tick, every = nil, 0 // no every, no tick: a chain needs an interval (Lector PR r0 R2)
	}
	h := s.cardHost()
	if h == nil {
		L.RaiseError("ui.card: this program shows no card")
	}
	s.endCard() // a card shown already is replaced: its handle no longer matches
	s.cardGen++
	c := &luaCard{gen: s.cardGen, every: every, tick: tick}
	c.ud = L.NewUserData()
	c.ud.Value = c.gen
	L.SetMetatable(c.ud, L.GetTypeMetatable(luaCardType))
	s.card = c
	s.c.Set("App.luaCardTitle", cmp.Or(lua.LVAsString(spec.RawGetString("title")), "card"))
	s.c.Set("App.luaCardText", lua.LVAsString(spec.RawGetString("text")))
	if h.panelOpen(luaCardPanel) {
		s.armCard() // open already: no Opened comes, so this card's chain starts here
	} else {
		h.openPanel(luaCardPanel) // its Opened arms the chain
	}
	L.Push(c.ud)
	return 1
}

// cardEvery is a spec's every as an interval: nil none, a number at least cardEveryMin.
func cardEvery(v lua.LValue) time.Duration {
	n, ok := v.(lua.LNumber)
	if !ok {
		return 0
	}
	return max(time.Duration(float64(n)*float64(time.Millisecond)), cardEveryMin)
}

// handleCard is the card a handle names, while it is the one shown; nil for a card since replaced
// or ended.
func (s *scripting) handleCard(L *lua.LState) *luaCard {
	ud := L.CheckUserData(1)
	gen, ok := ud.Value.(uint64)
	if !ok {
		L.ArgError(1, "a card")
	}
	if s.card == nil || s.card.gen != gen {
		return nil
	}
	return s.card
}

func (s *scripting) cardSet(L *lua.LState) int {
	text := L.CheckString(2)
	if s.handleCard(L) != nil {
		s.c.Set("App.luaCardText", text)
	}
	return 0
}

func (s *scripting) cardClose(L *lua.LState) int {
	if s.handleCard(L) != nil {
		s.resetCard()
	}
	return 0
}

func (s *scripting) cardIsOpen(L *lua.LState) int {
	h := s.cardHost()
	L.Push(lua.LBool(s.handleCard(L) != nil && h != nil && h.panelOpen(luaCardPanel)))
	return 1
}

// armCard starts the shown card's one chain for a new opening: every chain armed before is dead.
// Its first tick runs on the loop's next turn, never inside ui.card or the panel's Opened, as every
// callback of a host function does.
func (s *scripting) armCard() {
	s.cardEpoch++
	if c := s.card; c != nil && c.tick != nil && s.host != nil {
		gen, epoch := c.gen, s.cardEpoch
		s.host.post(func() { s.cardTick(gen, epoch) })
	}
}

// live reports whether a chain of card gen, armed at opening epoch, may still run.
func (s *scripting) live(gen, epoch uint64) bool {
	return s.card != nil && s.card.gen == gen && s.cardEpoch == epoch
}

// cardTick is one tick of one opening's chain: tick(card), then the next, unless the card was
// replaced or ended, or the panel closed or opened again, before the tick or during it.
func (s *scripting) cardTick(gen, epoch uint64) {
	if !s.live(gen, epoch) {
		return
	}
	c := s.card
	s.callback("ui.card's tick", c.tick, c.ud, "")
	if !s.live(gen, epoch) { // the tick closed or replaced its card, or reloaded
		return
	}
	if h := s.cardHost(); h != nil {
		h.after(c.every, func() { s.cardTick(gen, epoch) })
	}
}

// endCard ends the card shown: its handle dead, its chain with it. The panel is left as it is.
func (s *scripting) endCard() {
	s.card = nil
	s.cardEpoch++
}

// resetCard ends the card and closes its panel: card:close(), and a new engine's (bind), whose old
// card's functions are another engine's.
func (s *scripting) resetCard() {
	s.endCard()
	if h := s.cardHost(); h != nil && h.panelOpen(luaCardPanel) {
		h.closePanel(luaCardPanel)
	}
}

// luaWidth is autodoc.ui.width(s): how many terminal columns s takes, as AutoDoc measures its own
// tables (a wide character two, a combining mark none), so a script pads text to line up.
func luaWidth(L *lua.LState) int {
	L.Push(lua.LNumber(tuicore.StringWidth(L.CheckString(1))))
	return 1
}

func (s *scripting) luaCharts(L *lua.LState) int {
	h := s.cardHost()
	L.Push(lua.LBool(h != nil && h.native()))
	return 1
}

// luaAppStats is autodoc.app.stats(): the app's own process, keyed as sys.stats answers.
func (s *scripting) luaAppStats(L *lua.LState) int {
	h := s.cardHost()
	if h == nil {
		L.Push(lua.LNil)
		return 1
	}
	st := rpc.StatsOf(h.appStats(), h.appVersion(), time.Now())
	t := L.NewTable()
	for k, v := range map[string]lua.LValue{
		"pid": lua.LNumber(st.PID), "version": lua.LString(st.Version), "started_at": lua.LNumber(st.StartedAt),
		"uptime": lua.LNumber(st.Uptime), "rss": lua.LNumber(st.RSS), "peak_rss": lua.LNumber(st.PeakRSS),
		"cpu_seconds": lua.LNumber(st.CPUSeconds), "go_memory": lua.LNumber(st.GoMemory),
		"go_heap": lua.LNumber(st.GoHeap), "goroutines": lua.LNumber(st.Goroutines), "cpus": lua.LNumber(st.CPUs),
	} {
		L.SetField(t, k, v)
	}
	L.Push(t)
	return 1
}

// Panels is the Lua card's panel: at the right edge, its size kept as every panel's is. Opening it
// arms the card's chain; closing it ends that opening's chain and keeps the card for the next.
func (s *scripting) Panels() []PanelSpec {
	return []PanelSpec{{ID: luaCardPanel, View: luaCardView, Priority: 75,
		Edge: func(prefs) string { return "right" }, Places: []string{"right"},
		Size: [2]int{sidePanelMin, 90}, Length: [2]int{20, 100}, Default: sideDefault,
		Opened: func(PanelHost) { s.armCard() },
		Closed: func(h PanelHost) {
			s.cardEpoch++
			h.Keep(h.RestoreKeyboardPane("editor"))
		}}}
}

// cardState is the card panel's App sources: its heading, its text, its size.
func (s *scripting) cardState() map[string]any {
	size, length := s.c.Prefs().panelGeometry(luaCardPanel, "right")
	return map[string]any{"App.luaCardTitle": "card", "App.luaCardText": "",
		"App.luaCardSize": size, "App.luaCardLength": length}
}

// PreferencesChanged gives the card's panel the size the user left it at.
func (s *scripting) PreferencesChanged() {
	size, length := s.c.Prefs().panelGeometry(luaCardPanel, "right")
	s.c.Set("App.luaCardSize", size)
	s.c.Set("App.luaCardLength", length)
}
