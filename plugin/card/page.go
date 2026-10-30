// Package card is AutoDoc's native UI kit for dialog and card plugins
// (docs/decisions/plugins.md#the-native-card-kit): a plugin describes what its card shows as a Page of blocks and controls, and a Card fits it to the size
// AutoDoc gives, draws it in the theme's colours, and handles the keys, the focus and the pointer.
//
// The controls (Input, Buttons, List, Tree) are values the plugin keeps between draws; the Card
// asks for the page afresh on every draw, so the screen is always the plugin's state:
//
//	type myPlugin struct {
//		peer *plugin.Peer
//		card *card.Card
//		name card.Input
//		ok   card.Buttons
//	}
//
//	func (p *myPlugin) page() card.Page {
//		return card.Page{
//			Title:   "Hello",
//			Header:  []card.Block{card.Line(card.Title, "Who are you?")},
//			Body:    []card.Block{&p.name},
//			Buttons: &p.ok,
//			Keys:    "Tab: move · Enter: greet · q: close",
//		}
//	}
//
// A plugin forwards its Handler's methods to the Card (Open, Resize, Theme, Focus, Pointer, and
// Key, keeping the keys the Card does not use). The Card's methods run on the Handler's goroutine
// only, as the plugin's own state does.
package card

import "github.com/yongjohnlee80/autodoc/plugin"

// Page is what the card shows now, built by the plugin on every draw.
type Page struct {
	Title   string   // host.title, sent only when it changes; "" sends nothing
	Header  []Block  // always shown, from the top
	Body    []Block  // under it; scrolls to keep the focused control's row in view
	Buttons *Buttons // the bar under the rule; nil for none
	Keys    string   // the hint line, in Dim
	Status  string   // what was done last, in Plain
	Problem string   // what went wrong, in Problem
}

// Block is one part of a page. Only the kit makes them: Text, Line, Blank, and the controls.
type Block interface{ block() }

// Control is a block that takes focus: *Input, *Buttons, *List, *Tree.
type Control interface {
	Block
	control()
}

// Role is a kind of text, which the theme gives its style.
type Role int

const (
	Plain   Role = iota // as the card's text
	Dim                 // a hint, a path, a note
	Title               // a heading
	Good                // what went right
	Problem             // what went wrong
)

// style is the role's style. No role names a background, so text sits on AutoDoc's panel.
func (r Role) style() plugin.Style {
	switch r {
	case Dim:
		return plugin.Style{FG: "gray"}
	case Title:
		return plugin.Style{Bold: true}
	case Good:
		return plugin.Style{FG: "green"}
	case Problem:
		return plugin.Style{FG: "red"}
	}
	return plugin.Style{}
}

type textBlock struct {
	role Role
	s    string
	one  bool // one row, elided; else wrapped
}

func (textBlock) block() {}

// Text is a paragraph in r, wrapped at spaces to the card's width; a word wider than a row breaks
// after its last / or -, else at the row's end. A newline starts a new row.
func Text(r Role, s string) Block { return textBlock{role: r, s: s} }

// Line is one row in r, cut with … at the card's width.
func Line(r Role, s string) Block { return textBlock{role: r, s: s, one: true} }

type blank struct{}

func (blank) block() {}

// Blank is an empty row.
func Blank() Block { return blank{} }
