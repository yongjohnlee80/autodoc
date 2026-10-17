package card

import (
	"slices"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// Key is a key typed into the card. It returns true when the card used it, and has drawn; false
// leaves it to the plugin: its own letters, q to close (never while an Input has the keys, where q
// is text).
//
//	any control      Tab / Shift+Tab     the next / previous control, wrapping
//	Input            text                inserted at the cursor (an IME commit or a paste, whole)
//	                 Backspace Delete Left Right Home End    edit, move the cursor
//	                 Ctrl+U              Clear
//	                 Up / Down           the previous / next control
//	                 Enter               OnEnter, else the next control
//	Buttons          Left / Right        the previous / next button
//	                 Enter / Space       OnPress
//	                 Up / Down           the previous / next control
//	List, Tree       Up k, Down j        the selection
//	                 PageUp Ctrl+U, PageDown Ctrl+D          half the card's height
//	                 Home g, End G       the first, the last row
//	List             Enter               OnEnter(Sel); nothing on an empty list
//	Tree             Right l             open (OnOpen first); on an open node, its first row
//	                 Left h              close; on a closed node or a leaf, the row holding it
//	                 Enter               OnEnter; when it returns false, open or close
func (c *Card) Key(k plugin.Key) bool {
	ctls := controls(c.page())
	c.settle(ctls)
	if len(ctls) == 0 {
		return false
	}
	if k.Key == "Tab" && !k.Ctrl && !k.Alt {
		dir := 1
		if k.Shift {
			dir = -1
		}
		c.cycle(ctls, dir)
		c.Draw()
		return true
	}
	var r keyResult
	switch ctl := c.focus.(type) {
	case *Input:
		r = ctl.key(k)
	case *Buttons:
		r = ctl.key(k)
	case *List:
		r = ctl.key(k, c.halfPage())
	case *Tree:
		r = ctl.key(k, c.halfPage())
	}
	switch r {
	case unused:
		return false
	case prevCtl:
		c.cycle(ctls, -1)
	case nextCtl:
		c.cycle(ctls, +1)
	}
	c.Draw()
	return true
}

// halfPage is how far PageUp and PageDown move: half the card's height.
func (c *Card) halfPage() int { return max(c.h/2, 1) }

// cycle moves the focus dir controls on among ctls, wrapping.
func (c *Card) cycle(ctls []Control, dir int) {
	i := slices.Index(ctls, c.focus)
	n := len(ctls)
	c.focus = ctls[((i+dir)%n+n)%n]
}
