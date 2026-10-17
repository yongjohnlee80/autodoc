package card

import "github.com/yongjohnlee80/autodoc/plugin"

// Pointer is a left press or a wheel step on the card (protocol 6), read against the last draw.
// It returns true when the card used it, and has drawn.
//
//	press, left, on an Input's row        focus it
//	press, left, on a button              focus the Buttons on it, and OnPress it
//	press, left, on a List or Tree row    focus it, and select that row
//	wheel                                 the focused List's or Tree's selection, 3 rows a step
//	                                      (48 at most); with neither focused, the body scrolls
func (c *Card) Pointer(at plugin.Pointer) bool {
	if at.X < 0 || at.X >= c.w || at.Y < 0 || at.Y >= len(c.hits) || at.Count < 1 {
		return false
	}
	ln := c.hits[at.Y].line
	switch {
	case at.Kind == "press" && at.Button == "left":
		return c.press(ln, at.X-1) // the content starts one cell in
	case at.Kind == "wheel" && (at.Button == "up" || at.Button == "down"):
		step := min(at.Count*3, 48)
		if at.Button == "up" {
			step = -step
		}
		return c.wheel(step)
	}
	return false
}

func (c *Card) press(ln line, x int) bool {
	switch ctl := ln.ctl.(type) {
	case *Input:
		c.focus = ctl
	case *Buttons:
		for _, b := range ln.btns {
			if x >= b.x0 && x < b.x1 {
				c.focus, ctl.at = ctl, b.i
				ctl.press(b.i)
				c.Draw()
				return true
			}
		}
		return false
	case *List:
		c.focus, ctl.Sel = ctl, ln.item
	case *Tree:
		rows := ctl.rows()
		if ln.item < 0 || ln.item >= len(rows) {
			return false
		}
		c.focus = ctl
		ctl.selectRow(rows[ln.item])
	default:
		return false
	}
	c.Draw()
	return true
}

func (c *Card) wheel(step int) bool {
	switch ctl := c.focus.(type) {
	case *List:
		ctl.Sel += step
		ctl.clamp()
	case *Tree:
		rows := ctl.rows()
		if len(rows) == 0 {
			return false
		}
		ctl.selectRow(rows[min(max(ctl.index(rows)+step, 0), len(rows)-1)])
	default:
		c.from = max(c.from+step, 0) // fit keeps it in range
	}
	c.Draw()
	return true
}
