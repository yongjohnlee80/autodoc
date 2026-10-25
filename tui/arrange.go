package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// ARRANGE PANEL — Go › Arrange panel (SPC L): the panel with the keyboard, else the one opened
// last, moved and resized by keys. A panel tiled now is its tile's to place, and refuses a move, so
// Arrange says so and leaves it. A small card holds the keyboard meanwhile (views/Arrange.qml),
// so Escape reaches it and not the drawer, which would close. It is one change: the steps show at
// once but nothing is written until Enter keeps them; Escape puts the panel back as it was, the
// store untouched.

// arrangeState is an arrangement in progress.
type arrangeState struct {
	panel string
	begin *[4]float64 // its floating rectangle when it began; nil: it was docked
	rect  *[4]float64 // where the last step left it; nil until a step lands
}

// arrangeSteps are the keys' steps, in cells: a move, or a resize.
var arrangeSteps = map[string]tuicore.Action{
	"left": widget.WindowMoveByAction{DX: -2}, "right": widget.WindowMoveByAction{DX: 2},
	"up": widget.WindowMoveByAction{DY: -1}, "down": widget.WindowMoveByAction{DY: 1},
	"narrower": widget.WindowResizeByAction{DW: -2}, "wider": widget.WindowResizeByAction{DW: 2},
	"shorter": widget.WindowResizeByAction{DH: -1}, "taller": widget.WindowResizeByAction{DH: 1},
}

// arrangePanel begins an arrangement.
func (h *Host) arrangePanel() {
	panel := h.panelWithKeyboard()
	if panel == "" {
		h.notify(h.tr("autodoc.notice.arrange.no_panel_is_open_to_arrange_open_one_first_the_g"))
		return
	}
	if h.tiledNow(panel) {
		h.notify(h.trf("autodoc.notice.arrange.s_is_tiled_layout_float_lets_it_be_arranged", h.panelTitle(panel)))
		return
	}
	a := &arrangeState{panel: panel}
	if r, ok := h.prefs.panelFloat[panel]; ok {
		a.begin = &r
	}
	h.arrange = a
	h.set("App.arrangeTitle", h.trf("autodoc.text.arrange.arrange", h.panelTitle(panel)))
	h.keep(h.p.Call("arrange", "open"))
}

// arrangeStep is one key of an arrangement.
//
// The step is the application's, not the keyboard's: the card holding the keyboard is modal, and
// the drawer behind it refuses input-derived window actions (its moveBy and resizeBy methods), so
// the step goes to the drawer's window core as a programmatic action.
func (h *Host) arrangeStep(step string) {
	action, ok := arrangeSteps[step]
	if h.arrange == nil || !ok {
		return
	}
	c, found := h.p.Find(h.arrange.panel)
	w, isWindow := c.(interface{ WindowBehavior() *widget.WindowCore })
	if !found || !isWindow || w.WindowBehavior() == nil {
		return
	}
	w.WindowBehavior().HandleAction(tuicore.ActionInvocation{Action: action, Origin: tuicore.OriginProgrammatic})
}

// arrangeKeep is Enter: the last step's rectangle is written, once. The card closes next
// (arrangeCancel), which gives the panel the keyboard.
func (h *Host) arrangeKeep() {
	a := h.arrange
	h.arrange = nil
	if a == nil {
		return
	}
	h.arranged = a.panel
	if a.rect != nil {
		h.panelPlaced(a.panel, a.rect[0], a.rect[1], a.rect[2], a.rect[3])
	}
}

// arrangeCancel is the card closed: after Enter (arrangeKeep), or by Escape or any other way, when
// the panel goes back to where it began, through the properties the drawer binds, nothing stored.
// Either way the arrangement ends with the keyboard in the panel it arranged, which comes forward by
// it: the user's own Arrange asked for that window.
func (h *Host) arrangeCancel() {
	a := h.arrange
	h.arrange = nil
	panel := h.arranged
	h.arranged = ""
	if a != nil {
		panel = a.panel
		if a.rect != nil {
			// docked when it began: floating false is the change the drawer sees; floating, its
			// rectangle's percentages are
			h.showFloat(a.panel, a.begin)
		}
	}
	if panel != "" {
		// after the card's own close puts the keyboard back where it was
		h.p.Post(func() { h.focusWindow(panel) })
	}
}

// panelTitle is how a panel is named to the user, in the UI's language.
func (h *Host) panelTitle(panel string) string { return h.panelTitleIn(h.trf, panel) }

// panelTitleIn is panelTitle in trf's language: h.trf, or inEnglish{}.Trf for a name that is no screen's
// (a script's autodoc.widgets()).
func (h *Host) panelTitleIn(trf func(string, ...any) string, panel string) string {
	if panel == "htmlPane" {
		return trf("autodoc.text.arrange.the_preview")
	}
	for _, c := range h.plugins.cardOrder {
		if cardPanelID(c.name) == panel {
			return c.entry.label() // a card plugin's panel: its title
		}
	}
	return thePanel(trf, panel)
}

// thePanel is a panel's plain title, "the <name>", in trf's language: a panel's own when it has no
// other, and what a widget's row leaves out as saying no more than its name (withRowText).
func thePanel(trf func(string, ...any) string, name string) string {
	return trf("autodoc.text.arrange.the", name)
}
