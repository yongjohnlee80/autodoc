package feature

import tuicore "github.com/yongjohnlee80/golib/tui"

// CellMeasure measures in the cells a terminal draws text in: a layout off the loop has no surface
// to ask (a diagram's, a Mindmap's).
type CellMeasure struct{}

// StringWidth is s's width in cells.
func (CellMeasure) StringWidth(s string) int { return tuicore.StringWidth(s) }
