package tui

// THE HOST'S SIDE OF THE FEATURES (feature/feature.go): how the host assembles its features and lends
// each the Core it reaches the host through (core{h}), and the host's own part of the program
// (legacy). The features' side, what they implement and what they may use, is feature/feature.go's.

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/autodoc/tui/system"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/i18n"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
)

// assembleFeatures is initial program construction, before views can run.
// A failed schema or service binding must not publish the candidate registry.
func (h *Host) assembleFeatures(fs ...feature.Feature) error {
	previous, defs := h.reg, h.prefs.panelDefs
	r, err := feature.NewRegistryWithPanels(func(specs []feature.PanelSpec) { h.prefs.panelDefs = specs }, fs...)
	if err != nil {
		h.prefs.panelDefs = defs
		return err
	}
	h.reg = r
	if err := bindPanels(r.Panels, core{h}); err != nil {
		h.reg, h.prefs.panelDefs = previous, defs
		return err
	}
	return nil
}

// features are the program's parts, in the order the registry merges them.
func (h *Host) features() []feature.Feature {
	h.asker = feature.NewAsker(core{h})
	h.asker.OnIdle = func() { h.p.Post(h.askLuaGrants) } // a Lua grant waiting for the card (luagate.go)
	fs := []feature.Feature{pageFind{h}, legacy{h}, h.terminals, h.agents, h.crew, h.crewInbox, h.asker, h.noticeLog, h.recent, h.tiles, h.explorer, h.outline, h.rel, h.ai, h.plugins, system.NewStartup(core{h}, h.startupSeams(), h.aboutInfo.Version, h.canUpdate), newTodos(core{h}), h.luaFeature()}
	fs = append(fs, newRegistryBrowse(h))                                                                                             // Plugins › Browse the registry… (registrybrowse.go)
	fs = append(fs, h.cands)                                                                                                          // the installs' candidates, and Audit… (candidate.go)
	fs = append(fs, newCellNotePage(h))                                                                                               // a note's Rendered view on a terminal (notepage.go)
	fs = append(fs, newCardPage(h))                                                                                                   // a card's Rendered view on a terminal (cardpage.go)
	fs = append(fs, &htmlPaneCells{h: h})                                                                                             // the HTML preview pane on a terminal (htmlpane_cells.go)
	fs = append(fs, h.relationViewFeatures()...)                                                                                      // the GUI's GlobeMap and Mindmap (relationmap.go)
	fs = append(fs, system.NewProfiling(core{h}, h.profiler, func(ctx context.Context, p string) error { return h.browser(ctx, p) })) // System › Profiling (profiling.go)
	fs = append(fs, newCrewBridges(core{h}, h.crew))                                                                                  // System › Crew Bridges (crewbridges.go)
	fs = append(fs, h.inboxView)                                                                                                      // SPC a i, :inbox (crewinboxview.go)
	fs = append(fs, h.grantsView)                                                                                                     // Go › Agent permissions, :permissions (grants.go)
	fs = append(fs, h.moreFeatures...)
	// the preferences every feature declares, read over their defaults until the store's arrive;
	// their choosers are one feature of their own
	h.prefRows = nil
	for _, f := range fs {
		if p, ok := f.(feature.Prefs); ok {
			h.prefRows = append(h.prefRows, p.Prefs()...)
		}
	}
	for _, s := range h.prefRows {
		if s.Value(h.prefs) == "" { // a feature's own row: the host's defaults hold the rest
			s.Reset(&h.prefs)
		}
	}
	h.keys = feature.KeyCatalog(fs)
	h.keys.Tr = h.trOr // the cards and help in the UI's language (language.go)
	return append(fs, prefChoosers{h})
}

// legacy is the host as it is: everything not yet a feature of its own.
type legacy struct{ h *Host }

// HostOwned marks legacy as the host's own (feature/feature.go).
func (legacy) HostOwned() {}

func (legacy) Name() string { return "legacy" }

func (l legacy) State() map[string]any { return l.h.state() }

func (l legacy) Commands() map[string]feature.Command { return l.h.commands() }

func (l legacy) Prefs() []feature.PrefSpec { return legacyPrefRows(l.h, l.h.gui) }

// Listed is the workspace's files listed again: File › Open and the draft's name list them.
func (l legacy) Listed() {
	l.h.openPick.SetAll(l.h.files.Paths)
	l.h.newPick.SetAll(l.h.files.Paths)
}

// Picks are the host's own pickers.
func (l legacy) Picks() map[string]feature.Picker {
	return map[string]feature.Picker{"search": l.h.searchPick, "open": l.h.openPick, "new": l.h.newPick, "buffers": l.h.bufferPick}
}

// core is the host as a feature sees it.
type core struct{ h *Host }

func (c core) Set(name string, v any) { c.h.set(name, v) }

func (c core) Open(id string) { c.h.open(id) }

func (c core) Close(id string) { c.h.closeDialog(id) }

func (c core) Notify(msg string) { c.h.notify(msg) }

func (c core) Tr(id string) string { return c.h.tr(id) }

func (c core) Trf(id string, args ...any) string {
	return c.h.trf(id, args...)
}

func (c core) WidgetsChanged() { c.h.refreshDock() }

func (c core) HidePanel(id string) { c.h.hidePanel(id); c.h.focusEditor() }

func (c core) Ask(q feature.Question) feature.Asked { return c.h.ask(q) }

func (c core) AskWhenFree(q feature.Question) { c.h.asker.AskWhenFree(q) }

func (c core) Workspace() string { return c.h.ws }

func (c core) Prefs() feature.PrefsView { return c.h.prefs }

// LayoutRows is Layout › for the window id (feature.LayoutRows): the host's tiling windows'.
func (c core) LayoutRows(id string, row feature.RowMaker) []widgetcore.MenuItemModel {
	return layoutRows(c, id, row)
}

// Env is the program's environment as it is now (seams.go): a test may change it while the
// program runs.
func (c core) Env() procenv.Env { return c.h.seams.env }

// startupSeams is startup's part of the program's seams: the latest release's address, and the
// first run's two questions, read from the seams when startup asks.
func (h *Host) startupSeams() system.StartupSeams {
	return system.StartupSeams{
		LatestURL:          h.seams.latestURL,
		VimQuestionPending: func(p feature.PrefsView) bool { return vimQuestionPending(&h.seams, p) },
		AgentSetupPending:  func(p feature.PrefsView) bool { return agentSetupPending(&h.seams, p) },
	}
}

func (c core) OpenFile(abs string) { c.h.openAbsolute(abs) }

func (c core) Failed(what string, err error) { c.h.failed(what, err) }

func (c core) Focus(id string) { c.h.keep(c.h.p.Call(id, "forceActiveFocus")) }

func (c core) Call(ctx context.Context, verb string, params ...any) (any, error) {
	return c.h.call(ctx, verb, params...) // the one send path: a gated authority on ctx is bounded there
}

func (c core) Go(fn func(context.Context) any, then func(any)) { do(c.h, fn, then) }

func (c core) Bell() { c.h.ringBell() }

// SetText sets the text of the layout's text area id (an id inside a component's instance is
// scoped: crewreply.go's crewReplyText). A layout without it is said, never passed over.
func (c core) SetText(id, text string) {
	if c.h.p == nil {
		return
	}
	ta, ok := tuidecl.FindAs[*widgetcore.TextArea](c.h.p, id)
	if !ok {
		c.h.keep(fmt.Errorf("the layout has no text area %q", id))
		return
	}
	ta.SetValue(text)
}

func (c core) Ticket(seq *uint64, scope feature.TicketScope) feature.Ticket {
	return c.h.ticket(seq, scope)
}

func (c core) OpenRef(ws, path string) { c.h.openIn(ws, path) }

func (c core) OpenAbsolute(abs string) { c.h.openAbsolute(abs) }

func (c core) Say(msg string) { c.h.say(msg) }

func (c core) Files() feature.FileList { return c.h.files }

func (c core) Workspaces() []feature.Workspace {
	list := make([]feature.Workspace, 0, c.h.workspaces.Len())
	for _, w := range c.h.workspaces.All() {
		list = append(list, workspaceOf(w))
	}
	return list
}

// workspaceOf is w as a feature reads it (Workspace).
func workspaceOf(w wsInfo) feature.Workspace {
	return feature.Workspace{ID: w.id, Name: w.name, Root: w.root, State: w.state, Include: w.include, Exclude: w.exclude,
		SectionTokens: w.sectionTokens, Schema: w.schema.path, TextExtensions: w.textExtensions}
}

func (c core) View(id, method string, args ...any) error { return c.h.p.Call(id, method, args...) }

func (c core) TogglePanel(id string) { c.h.togglePanel(id) }

func (c core) TogglePanelTab(id string, tab int) { c.h.togglePanelTab(id, tab) }

func (c core) SearchModelsChanged(ranker bool) {
	if ranker {
		c.h.loadStageRanker(true) // which searches again, once
		return
	}
	c.h.refreshSearch()
}

func (c core) Post(fn func()) { c.h.p.Post(fn) }

func (c core) HasFocus(id string) bool {
	comp, ok := c.h.p.Find(id)
	return ok && c.h.p.App().FocusWithin(comp)
}

func (c core) Page() feature.Page { return c.h.pageNow() }

func (c core) Crumbs(parts []string) { c.h.crumbs = parts; c.h.showTitle() }

func (c core) GoToLine(line int) {
	c.h.core.SetLine(line, 0)
	c.h.keep(c.h.p.Call("editor", "forceActiveFocus"))
}

func (c core) SetPref(name, value string) {
	for _, row := range c.h.prefRows {
		if row.Name == name {
			c.h.setRowPref(row, value)
			return
		}
	}
}

// ticket is Core.Ticket, for the host's own answers too: every stale-answer guard is one.
func (h *Host) ticket(seq *uint64, scope feature.TicketScope) feature.Ticket {
	var n uint64
	if seq != nil {
		*seq++
		n = *seq
	}
	// the page is a buffer and its generation: buffers count their generations alike, so the same
	// number in another buffer is another page
	gen, epoch, file, page := h.session.Gen(), h.epoch, h.file.gen, h.active
	return feature.NewTicket(func() bool {
		if scope == feature.TicketLocal {
			return seq == nil || *seq == n
		}
		if seq != nil && *seq != n || h.session.Gen() != gen {
			return false
		}
		switch scope {
		case feature.TicketSession:
			return true
		case feature.TicketWorkspace:
			return h.epoch == epoch
		}
		return h.epoch == epoch && h.active == page && h.file.gen == file
	})
}

func (c core) Keep(err error) { c.h.keep(err) }

func (c core) Focused(id string) bool { return c.h.paneHasKeyboard(id) }

func (c core) PanelOpen(id string) bool { return c.h.panelOpen[id] }

func (c core) OpenDrawer(id string) { c.h.openDrawer(id) }

func (c core) CloseDrawer(id string) { c.h.closeDrawer(id) }

func (c core) DockPanel(id string) { c.h.dockPanel(id) }

func (c core) KeepPanelGeometry(id, dimension string, percent int) {
	c.h.keepPanelGeo(id, dimension, percent)
}

func (c core) ActiveWorkspace() (feature.Workspace, bool) {
	w, ok := c.h.activeWorkspaceInfo()
	return workspaceOf(w), ok
}
func (c core) ActiveWorkspaceInfo() (feature.Workspace, bool) {
	w, ok := c.h.activeWorkspaceInfo()
	return workspaceOf(w), ok
}

func (c core) SetFormText(name, value string) { c.h.setField(name, value) }

func (c core) DaemonAddress() string { return c.h.session.Address() }

func (c core) StoreID() string { return c.h.session.StoreID() }

func (c core) ConfigureTerminal(id, command, dir string, args ...string) error {
	view, ok := c.h.p.Find(id)
	if !ok {
		return fmt.Errorf("no terminal view %q", id)
	}
	t, ok := view.(interface {
		SetCommand(string, ...string)
		SetDir(string)
	})
	if !ok {
		return fmt.Errorf("view %q is not a configurable terminal", id)
	}
	t.SetCommand(command, args...)
	t.SetDir(dir)
	return nil
}

// ask is Core.Ask, for the host's own code that is not a feature yet.
func (h *Host) ask(q feature.Question) feature.Asked { return h.asker.Ask(q) }

// MenuRows is the menus' added rows, the scripts' and the plugins' (feature/menurows.go).
func (c core) MenuRows() *feature.MenuRows { return &c.h.luaFeature().rows }

// BACKGROUND WORK — off the loop, and back onto it.
//
// Nothing that talks to the daemon runs on the UI loop. do runs fn on its own goroutine under the
// host's context and applies its result on the loop with then. The result says which generation it
// was issued under; then decides whether it still applies. A late answer to a question nobody is
// asking any more is dropped there, never here.
func do[T any](h *Host, fn func(context.Context) T, then func(T)) {
	ctx := h.ctx
	go func() {
		// a panic here is on a goroutine of its own, where no loop boundary reaches: kept, said, and
		// handed to then as the failure it is where its answer can carry one (crash.go)
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				if ctx.Err() == nil {
					h.p.Post(func() { backgroundPanicked(h, rec, stack, then) })
				}
			}
		}()
		v := fn(ctx)
		if ctx.Err() != nil {
			return // the program stopped; nobody is waiting
		}
		h.p.Post(func() { then(v) })
	}()
}

// The kit's values that come from the host's embedded files, set once as the package starts.
func init() {
	feature.MainIDs = mainQMLIDs()
	feature.MenuMessage = englishMenuMessage
}

// mainQMLIDs are the ids the embedded main.qml declares outside its generated cards (mainIDs).
func mainQMLIDs() map[string]bool {
	src, err := fs.ReadFile(qmlFiles, "main.qml")
	if err != nil {
		panic("tui: embedded main.qml: " + err.Error())
	}
	s := string(src)
	if a, b := strings.Index(s, feature.CardsBegin), strings.Index(s, feature.CardsEnd); a >= 0 && b > a {
		s = s[:a] + s[b:]
	}
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bid:\s*([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(s, -1) {
		ids[m[1]] = true
	}
	return ids
}

// englishMenuMessage is the catalog's message for a menu row's English label, when it has one
// (menuMessage).
func englishMenuMessage(label string) (tuicore.Message, bool) {
	id := menuLabelID(label)
	if t, ok := englishCatalog().Lookup(i18n.English, id); ok && t == label {
		return tuicore.Msg(id), true
	}
	return tuicore.Message{}, false
}
