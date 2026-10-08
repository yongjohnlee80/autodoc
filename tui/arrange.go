package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// ARRANGE PANEL — Go › Arrange panel (SPC L): the panel with the keyboard, else the one opened
// last, moved and resized by keys. A small card holds the keyboard meanwhile (views/Arrange.qml),
// so Escape reaches it and not the drawer, which would close. It is one change: the steps show at
// once but nothing is written until Enter keeps them; Escape puts the panel back as it was, the
// store untouched.

// arrangeState is an arrangement in progress.
type arrangeState struct {
	panel string
	begin *[4]int // its floating rectangle when it began; nil: it was docked
	rect  *[4]int // where the last step left it; nil until a step lands
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
		h.notify("no panel is open to arrange: open one first (the Go menu)")
		return
	}
	a := &arrangeState{panel: panel}
	if r, ok := h.prefs.panelFloat[panel]; ok {
		a.begin = &r
	}
	h.arrange = a
	h.set("App.arrangeTitle", "arrange "+panelTitle(panel))
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

// arrangeKeep is Enter: the last step's rectangle is written, once.
func (h *Host) arrangeKeep() {
	a := h.arrange
	h.arrange = nil
	if a != nil && a.rect != nil {
		h.panelPlaced(a.panel, a.rect[0], a.rect[1], a.rect[2], a.rect[3])
	}
}

// arrangeCancel is Escape, or the card closed any other way: the panel goes back to where it
// began, through the properties the drawer binds; nothing was stored.
func (h *Host) arrangeCancel() {
	a := h.arrange
	h.arrange = nil
	if a == nil || a.rect == nil {
		return
	}
	// docked when it began: floating false is the change the drawer sees; floating, its
	// rectangle's percentages are
	h.showFloat(a.panel, a.begin)
}

// panelTitle is how a panel is named to the user.
func panelTitle(panel string) string {
	if panel == "htmlPane" {
		return "the preview"
	}
	return "the " + panel
}
