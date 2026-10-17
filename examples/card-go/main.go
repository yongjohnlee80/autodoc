// card-go is AutoDoc's native-kit example (plugin/card): a card with a field, a list, a tree and
// buttons, in about a hundred lines and no drawing code. Install it from a local checkout with
// `go build -o bin/autodoc-card-go .` here, or let AutoDoc run [install] build; see plugin.toml.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/autodoc/plugin/card"
)

// people is the card: its state, its controls, and the Card that draws them.
type people struct {
	plugin.Base // Close, which this card does not need
	peer        *plugin.Peer
	card        *card.Card

	names   []string
	name    card.Input
	list    card.List
	teams   card.Tree
	buttons card.Buttons
	status  string
}

func newPeople() *people {
	p := &people{names: []string{"Ann", "Bob", "Chloé"}}
	p.name = card.Input{Label: "add someone", OnEnter: p.add}
	p.list = card.List{
		Item:    func(i int) string { return p.names[i] },
		OnEnter: func(i int) { p.status = "picked " + p.names[i] },
	}
	p.teams = card.Tree{
		Roots: []*card.Node{
			{Text: "design", Children: []*card.Node{{Text: "Ann", Leaf: true}}},
			{Text: "docs", Empty: "(nobody yet)"},
		},
		OnEnter: func(n *card.Node) bool {
			if n.Leaf {
				p.status = n.Text + " is on a team"
				return true
			}
			return false // a team: Enter opens or closes it
		},
	}
	p.buttons = card.Buttons{Labels: []string{"Add", "Clear"}, OnPress: func(i int) {
		if i == 0 {
			p.add()
			return
		}
		p.names, p.status = nil, "cleared"
	}}
	p.card = card.New(p.page, card.Options{})
	return p
}

// page is what the card shows now: asked for afresh on every draw.
func (p *people) page() card.Page {
	p.list.Len = len(p.names)
	return card.Page{
		Title:   fmt.Sprintf("People (%d)", len(p.names)),
		Header:  []card.Block{card.Line(card.Title, "People")},
		Body:    []card.Block{&p.name, card.Blank(), &p.list, card.Blank(), card.Line(card.Dim, "teams"), &p.teams},
		Buttons: &p.buttons,
		Keys:    "Tab: move · Enter: add, pick, open · q: close",
		Status:  p.status,
	}
}

func (p *people) add() {
	if n := p.name.Value(); n != "" {
		p.names = append(p.names, n)
		p.status = "added " + n
		p.name.Clear()
	}
}

// The Handler's methods, forwarded to the Card; the card keeps q to close, where no field has it.
func (p *people) Open(peer *plugin.Peer, o plugin.Open) { p.peer = peer; p.card.Open(peer, o) }
func (p *people) Resize(w, h int)                       { p.card.Resize(w, h) }
func (p *people) Theme(t plugin.Theme)                  { p.card.Theme(t) }
func (p *people) Focus(focused bool)                    { p.card.Focus(focused) }
func (p *people) Pointer(at plugin.Pointer)             { p.card.Pointer(at) }
func (p *people) Key(k plugin.Key) {
	if !p.card.Key(k) && k.Key == "q" && !k.Ctrl && !k.Alt {
		_ = p.peer.Close()
	}
}

func main() {
	if err := plugin.Serve(context.Background(), newPeople()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
