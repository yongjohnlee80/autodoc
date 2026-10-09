package tui

import (
	"slices"
	"strconv"

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

// applyFonts tells the window the fonts p chooses, those that changed.
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
	was := h.fontsApplied
	if !was.set || want.cell != was.cell || want.size != was.size {
		h.fonts.SetFont(want.cell, float32(want.size))
	}
	if !was.set && want.prose != "" || was.set && want.prose != was.prose {
		h.fonts.SetProseFont(want.prose)
	}
	if !was.set && want.zoom != 100 || was.set && want.zoom != was.zoom {
		h.fonts.SetZoom(want.zoom)
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
		h.setPref(prefFontSize, strconv.Itoa(n), func(p *prefs) { p.fontSize = n })
	}
}

// setZoom sets the zoom to a step; one that is not a step is ignored.
func (h *Host) setZoom(pct int) {
	if h.fonts == nil || !slices.Contains(zoomSteps, pct) {
		return
	}
	h.setPref(prefZoom, strconv.Itoa(pct), func(p *prefs) { p.zoom = pct })
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
		label := strconv.Itoa(pct) + "%"
		if pct == zoomOf(h.prefs) {
			label = "• " + label
		}
		rows[i] = widget.NewCommand(widget.ItemID("autodoc.zoom."+strconv.Itoa(pct)), label,
			widget.CoreMenuAction{ID: "autodoc.zoom", Run: func(*widget.EditorCore) { h.setZoom(pct) }})
	}
	return widget.NewSubmenu("autodoc.zoom", "Zoom", rows)
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
