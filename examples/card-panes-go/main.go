// card-panes-go is AutoDoc's native-kit example of panes (plugin/card): a card of two titled
// panes, a library's list on the left and an editor on the right. F7 and Shift+F7 move between
// them, F8 maximizes one, and Tab stays inside the focused pane. Install it from a local checkout
// with `go build -o bin/autodoc-card-panes-go .` here, or let AutoDoc run [install] build; see
// plugin.toml.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/autodoc/plugin/card"
)

// library is the card: its books, its controls, the panes and the Card that draws them.
type library struct {
	plugin.Base // Close, which this card does not need
	peer        *plugin.Peer
	card        *card.Card
	panes       card.Split

	books   []string
	list    card.List
	name    card.Input
	buttons card.Buttons
	status  string
}

func newLibrary() *library {
	l := &library{books: []string{"Dune", "Emma", "Ulysses"}}
	l.list = card.List{Item: func(i int) string { return l.books[i] }}
	l.name = card.Input{Label: "rename to", OnEnter: l.rename}
	l.buttons = card.Buttons{Labels: []string{"Rename"}, OnPress: func(int) { l.rename() }}
	l.panes = card.Split{
		Ratio: 0.4,
		A:     card.Pane{Title: "Library", Page: l.libraryPage},
		B:     card.Pane{Title: "Editor", Page: l.editorPage},
	}
	l.card = card.NewPanes(&l.panes, card.Options{})
	return l
}

// libraryPage is the left pane: the books.
func (l *library) libraryPage() card.Page {
	l.list.Len = len(l.books)
	return card.Page{Body: []card.Block{&l.list}, Keys: "F7: the editor · F8: maximize"}
}

// editorPage is the right pane: the selected book, renamed with the field and the button.
func (l *library) editorPage() card.Page {
	book := "(none)"
	if l.list.Len > 0 {
		book = l.books[l.list.Sel]
	}
	return card.Page{
		Header:  []card.Block{card.Line(card.Title, "editing: "+book)},
		Body:    []card.Block{&l.name},
		Buttons: &l.buttons,
		Keys:    "Tab: field, button · Shift+F7: the library",
		Status:  l.status,
	}
}

func (l *library) rename() {
	if n := l.name.Value(); n != "" && l.list.Len > 0 {
		l.status = fmt.Sprintf("renamed %s to %s", l.books[l.list.Sel], n)
		l.books[l.list.Sel] = n
		l.name.Clear()
	}
}

// The Handler's methods, forwarded to the Card; the card keeps q to close, where no field has it.
func (l *library) Open(peer *plugin.Peer, o plugin.Open) { l.peer = peer; l.card.Open(peer, o) }
func (l *library) Resize(w, h int)                       { l.card.Resize(w, h) }
func (l *library) Theme(t plugin.Theme)                  { l.card.Theme(t) }
func (l *library) Focus(focused bool)                    { l.card.Focus(focused) }
func (l *library) Pointer(at plugin.Pointer)             { l.card.Pointer(at) }
func (l *library) Key(k plugin.Key) {
	if !l.card.Key(k) && k.Key == "q" && !k.Ctrl && !k.Alt {
		_ = l.peer.Close()
	}
}

func main() {
	if err := plugin.Serve(context.Background(), newLibrary()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
