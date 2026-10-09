package tui

import (
	"fmt"
	"slices"
	"strconv"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// FONTS AND ZOOM — the GUI's window draws in fonts it can change while it runs: the cell font (the
// monospace everything on the grid is drawn in) at a size, the prose font (the Rendered view's text
// and the HTML preview's), and a zoom over all of it. Options › Fonts and zoom… chooses them, as
// Preferences does, each at once; Ctrl+= and Ctrl+- step the zoom, Ctrl+0 resets it, and the
// page's right-click menu has the steps. They are kept in the store as gui.font.cell,
// gui.font.size, gui.font.prose and gui.zoom. A terminal draws in its own font: there, none of
// this shows (ADR 1791500773).

// FontControl is a window's fonts: --gui's backend (golib gui.Backend) has them, a terminal does not.
type FontControl interface {
	SetFont(typeface string, size float32)
	SetProseFont(family string)
	SetZoom(pct int)
	// Grid is the window's grid now, in cells, with the generation of the fonts that measured it;
	// FontGeneration is the generation of the fonts last set (golib's gui.Backend has both).
	Grid() (tuicore.Size, uint64)
	FontGeneration() uint64
}

const (
	prefCellFont  = "gui.font.cell"
	prefFontSize  = "gui.font.size"
	prefProseFont = "gui.font.prose"
	prefZoom      = "gui.zoom"
)

// zoomSteps are the zoom's steps, in percent: Options and the right-click menu offer them, and
// Ctrl+= and Ctrl+- move between them.
var zoomSteps = []int{100, 125, 150, 175, 200}

// fontSizes are the cell font's sizes the Fonts dialog offers; defaultFontSize is the window's own.
var fontSizes = []int{10, 11, 12, 13, 14, 15, 16, 18, 20, 22, 24}

const defaultFontSize = 14

// readFontPrefs reads the fonts and the zoom; a value out of range is the default's.
func readFontPrefs(p *prefs, m map[string]any) {
	str := func(k string) string { s, _ := m[k].(string); return s }
	p.cellFont, p.proseFont = str(prefCellFont), str(prefProseFont)
	if n, err := strconv.Atoi(str(prefFontSize)); err == nil && slices.Contains(fontSizes, n) {
		p.fontSize = n
	}
	if n, err := strconv.Atoi(str(prefZoom)); err == nil && slices.Contains(zoomSteps, n) {
		p.zoom = n
	}
}

// zoomOf is p's zoom: 100 unless set.
func zoomOf(p prefs) int { return max(p.zoom, 100) }

// fontState is what the document reads of the fonts: the zoom's radio set, the dialog's choices.
func fontState(p prefs) map[string]any {
	st := map[string]any{"App.zoomText": strconv.Itoa(zoomOf(p)) + "%"}
	size := p.fontSize
	if size == 0 {
		size = defaultFontSize
	}
	st["App.fontSizeIndex"] = max(slices.Index(fontSizes, size), 0)
	st["App.zoomIndex"] = max(slices.Index(zoomSteps, zoomOf(p)), 0)
	return st
}

// appliedFonts is what the window was last told, so a preference applied again tells it nothing.
type appliedFonts struct {
	cell, prose string
	size, zoom  int
	set         bool
}

// applyFonts tells the window the fonts p chooses, those that changed. A zoom the window cannot
// hold (a stored 200% on a smaller screen) is drawn at the largest step it can, and said; the
// store keeps the choice for a window that can.
func (h *Host) applyFonts(p prefs) {
	if h.fonts == nil {
		return
	}
	want := appliedFonts{cell: p.cellFont, prose: p.proseFont, size: p.fontSize, zoom: zoomOf(p), set: true}
	if want.cell == "" {
		want.cell = h.defaultCellFont
	}
	if want.size == 0 {
		want.size = defaultFontSize
	}
	if !h.fontsFit(want.size, want.zoom) {
		chosen := want.zoom
		for want.zoom > zoomSteps[0] && !h.fontsFit(want.size, want.zoom) {
			want.zoom = zoomSteps[max(slices.Index(zoomSteps, want.zoom), 1)-1]
		}
		h.notify(fmt.Sprintf("zoom %d%% does not fit this window: drawn at %d%%", chosen, want.zoom))
	}
	was := h.fontsApplied
	// what a grid of each generation is drawn at: every call below starts one, and a frame may
	// measure any of them, the ones between included
	cur := was
	if !was.set {
		cur = appliedFonts{size: defaultFontSize, zoom: 100} // the window's own, at its first generation
		h.fontGens = map[uint64]appliedFonts{h.fonts.FontGeneration(): cur}
	}
	told := func() { h.fontGens[h.fonts.FontGeneration()] = cur }
	if !was.set || want.cell != was.cell || want.size != was.size {
		h.fonts.SetFont(want.cell, float32(want.size))
		cur.cell, cur.size = want.cell, want.size
		told()
	}
	if !was.set && want.prose != "" || was.set && want.prose != was.prose {
		h.fonts.SetProseFont(want.prose)
		cur.prose = want.prose
		told()
	}
	if !was.set && want.zoom != 100 || was.set && want.zoom != was.zoom {
		h.fonts.SetZoom(want.zoom)
		cur.zoom = want.zoom
		told()
	}
	h.fontsApplied = want
}

// openFonts is Options › Fonts and zoom…: the installed families listed, the current ones chosen.
func (h *Host) openFonts() {
	if h.fonts == nil {
		h.notify("fonts and zoom are the window's: in a terminal, its own settings choose them")
		return
	}
	mono, all := h.familyList(true), h.familyList(false)
	h.cellFonts, h.proseFonts = append([]string{""}, mono...), append([]string{""}, all...)
	h.cellFontModel.Reset(fontRows(h.cellFonts))
	h.proseFontModel.Reset(fontRows(h.proseFonts))
	h.set("App.cellFontIndex", max(slices.Index(h.cellFonts, h.prefs.cellFont), 0))
	h.set("App.proseFontIndex", max(slices.Index(h.proseFonts, h.prefs.proseFont), 0))
	h.open("fonts")
}

// familyList is the installed families, mono only or all; none where the system cannot list them.
func (h *Host) familyList(mono bool) []string {
	if h.families == nil {
		return nil
	}
	return h.families(mono)
}

// fontModel is a family chooser's model before the families are listed: the default alone, so the
// chooser shows "(the default)" from the start, not a blank field waiting for Options › Fonts.
func fontModel() *tuidecl.ListModel {
	m := tuidecl.NewListModel("key", "label")
	m.Reset(fontRows([]string{""}))
	return m
}

// fontRows are a family chooser's rows: the default first, then each family.
func fontRows(families []string) []rowOf {
	rows := make([]rowOf, len(families))
	for i, f := range families {
		label := f
		if f == "" {
			label = "(the default)"
		}
		rows[i] = rowOf{"key": strconv.Itoa(i), "label": label}
	}
	return rows
}

func (h *Host) setCellFontIndex(i int) {
	if i >= 0 && i < len(h.cellFonts) {
		f := h.cellFonts[i]
		h.setPref(prefCellFont, f, func(p *prefs) { p.cellFont = f })
	}
}

func (h *Host) setProseFontIndex(i int) {
	if i >= 0 && i < len(h.proseFonts) {
		f := h.proseFonts[i]
		h.setPref(prefProseFont, f, func(p *prefs) { p.proseFont = f })
	}
}

func (h *Host) setFontSizeIndex(i int) {
	if i >= 0 && i < len(fontSizes) {
		n := fontSizes[i]
		if h.fonts != nil && n > h.fontsApplied.size && !h.fontsFit(n, h.fontsApplied.zoom) {
			h.notify(h.tooLarge(fmt.Sprintf("size %d", n)))
			h.set("App.fontSizeIndex", max(slices.Index(fontSizes, h.fontsApplied.size), 0))
			return
		}
		h.setPref(prefFontSize, strconv.Itoa(n), func(p *prefs) { p.fontSize = n })
	}
}

// setZoom sets the zoom to a step; one that is not a step is ignored, and so is one larger than the
// window can hold, which would leave only the too-small screen and its Quit.
func (h *Host) setZoom(pct int) {
	if h.fonts == nil || !slices.Contains(zoomSteps, pct) {
		return
	}
	if pct > h.fontsApplied.zoom && !h.fontsFit(h.fontsApplied.size, pct) {
		h.notify(h.tooLarge(fmt.Sprintf("zoom %d%%", pct)))
		return
	}
	h.setPref(prefZoom, strconv.Itoa(pct), func(p *prefs) { p.zoom = pct })
}

// fontsFit reports whether the window would still hold the screen's minimum (80 × 20, main.qml)
// with the cells at size and pct: the grid shrinks as the cells grow, in proportion near enough,
// and a cell is kept spare for the rounding.
//
// The grid is judged by the fonts that measured it, by its generation: the window may still report
// a grid of fonts set before the last change, measured before it and published after. A
// generation not recorded is judged by the smallest fonts recorded, which predicts the fewest
// columns. Before the window's first frame there is nothing to judge by, and it fits.
func (h *Host) fontsFit(size, pct int) bool {
	grid, gen := h.fonts.Grid()
	if grid.W <= 0 || grid.H <= 0 || h.p == nil {
		return true
	}
	now, ok := h.fontGens[gen]
	if !ok {
		now = appliedFonts{size: defaultFontSize, zoom: 100}
		for _, f := range h.fontGens {
			if f.size*f.zoom < now.size*now.zoom {
				now = f
			}
		}
	}
	for g := range h.fontGens { // what the window will not report again
		if g < gen {
			delete(h.fontGens, g)
		}
	}
	scale := float64(now.size*now.zoom) / float64(size*pct)
	min := h.p.App().MinimumSize()
	return (min.W <= 0 || int(float64(grid.W)*scale) > min.W) && (min.H <= 0 || int(float64(grid.H)*scale) > min.H)
}

// tooLarge says why a size was refused.
func (h *Host) tooLarge(what string) string {
	min := h.p.App().MinimumSize()
	return fmt.Sprintf("%s is too large for this window: it would be smaller than %d × %d; enlarge the window first", what, min.W, min.H)
}

func (h *Host) setZoomIndex(i int) {
	if i >= 0 && i < len(zoomSteps) {
		h.setZoom(zoomSteps[i])
	}
}

// zoomWindow is Ctrl+= ("in") and Ctrl+- ("out").
func (h *Host) zoomWindow(dir string) {
	switch dir {
	case "in":
		h.zoomStep(1)
	case "out":
		h.zoomStep(-1)
	}
}

// zoomStep is the next step in (+1) or out (-1), stopping at the ends.
func (h *Host) zoomStep(dir int) {
	i := slices.Index(zoomSteps, zoomOf(h.prefs)) + dir
	if i >= 0 && i < len(zoomSteps) {
		h.setZoom(zoomSteps[i])
	}
}

// zoomMenu is the right-click menu's Zoom: the steps, the current one checked. The GUI's only.
func (h *Host) zoomMenu() widget.MenuItemModel {
	rows := make([]widget.MenuItemModel, len(zoomSteps))
	for i, pct := range zoomSteps {
		label := "&" + strconv.Itoa(i+1) + ". " + strconv.Itoa(pct) + "%" // a digit each, as Editor mode's rows
		if pct == zoomOf(h.prefs) {
			label += " •"
		}
		rows[i] = menuCommand(widget.ItemID("autodoc.zoom."+strconv.Itoa(pct)), label,
			widget.CoreMenuAction{ID: "autodoc.zoom", Run: func(*widget.EditorCore) { h.setZoom(pct) }})
	}
	zoom := widget.NewSubmenu("autodoc.zoom", "Zoom", rows)
	zoom.Label, zoom.Hotkey, zoom.HotkeyIdx = tuicore.ParseMnemonic("&Zoom")
	return zoom
}

func fontSizeLabels() []string {
	out := make([]string, len(fontSizes))
	for i, n := range fontSizes {
		out[i] = strconv.Itoa(n)
	}
	return out
}

func zoomLabels() []string {
	out := make([]string, len(zoomSteps))
	for i, n := range zoomSteps {
		out[i] = strconv.Itoa(n) + "%"
	}
	return out
}

// menuCommand is a right-click row whose text marks its access key with &, as the menu bar's do.
// Its key is never h, j, k or l: in the menu those move as the arrows do, and a row's own key
// would win over them.
func menuCommand(id widget.ItemID, text string, action tuicore.Action) widget.MenuItemModel {
	label, key, at := tuicore.ParseMnemonic(text)
	row := widget.NewCommand(id, label, action)
	row.Hotkey, row.HotkeyIdx = key, at
	return row
}
