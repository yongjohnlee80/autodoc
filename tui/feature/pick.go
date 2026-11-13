package feature

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// THE PICKER — one core for every list a filter narrows: open a document, name the draft, the
// outline, the search. Its layout is one QML file, dialogs/Picker.qml: a filter field over the
// list on the left, the item under the cursor on the right, the buttons beneath. Each picker is a
// pick[T] over its own items, named for Picker.qml's handlers, which call the App.pick* commands
// with that name; its own file says what it lists and what choosing does.
//
// A new picker is a pick[T] a Feature returns from Picks() (feature.go), its sources in the
// feature's State(), and a Picker { name: … } use: a component file of its own
// (dialogs/FileOpen.qml) or an inline use in main.qml. The registry merges every feature's picks,
// refusing a name two claim, and serves the App.pick* commands over them.

// pane is what a picker's preview shows: a title, the text, and the byte the cursor is at.
type Pane struct {
	Title, Text string
	At          int
}

// pick is one picker: the items it holds, the rows it shows of them, its preview and its choice.
// Every method runs on the loop.
type Pick[T any] struct {
	Core   Core
	Dialog string // the Dialog's id, which choosing closes: "fileOpen"
	// name is the picker's to Picker.qml's handlers (App.pickFilter(name, text)); its preview binds
	// App.<name>PreviewTitle, App.<name>PreviewText and App.<name>PreviewAt
	Name string
	List *Rows[T] // the rows shown: the items the filter keeps
	All  []T      // every item the filter looks through

	// field, when set, is the App source the filter field binds: open empties it
	Field string
	// status, when set, is the App source of the list's title: "<noun> (shown of all)", noun in
	// English, shown in the UI's language (nounText)
	Status, Noun string
	// match is the text of an item the filter looks in, case aside
	Match func(T) string
	// query, when set, is a filter of the picker's own (the search's, by the daemon): it shows what
	// it finds with show
	Query func(text string)

	// preview, on the loop, is the work that reads an item for the preview; the work runs off the
	// loop, the latest asked for winning. An answer's error is why the item could not be read: its
	// pane says so. nil: no preview.
	Preview func(T) func(context.Context) AnswerOf[Pane]
	// shown, when set, runs on the loop just before a current preview shows (the search's Rendered
	// switch)
	Shown func(T, AnswerOf[Pane])
	// empty is what the preview shows when no row is: nil, an empty pane
	Empty func() Pane
	Scope TicketScope // what makes a preview's answer stale, besides a newer one
	seq   uint64

	// choose takes an item: accept on a row, the dialog closed first
	Choose func(T)
	// enter, when set, is what Enter on a row does instead of choosing (the draft's name takes the
	// row's folder)
	Enter func(T)
	// submit, when set, is what accept does with the field's text instead of choosing a row (the
	// draft's name creates the file)
	Submit func(text string)
	// cancel, when set, runs when the dialog is dismissed without a choice
	Cancel func()
	// lost, when set, runs when the dialog closed with no choice: Esc (after cancel), or a close()
	// from elsewhere, which answers nothing. answering is that a choice is under way, so the
	// dialog's closed, which comes before choose runs, is not lost.
	Lost      func()
	answering bool
}

// open opens the picker over its items, the field emptied and every item listed.
func (p *Pick[T]) Open() {
	if p.Field != "" {
		p.Core.Set(p.Field, "\x00") // moved away first, so an empty field twice still empties it
		p.Core.Set(p.Field, "")
	}
	p.Filter("")
	p.Core.Open(p.Dialog)
}

// setAll replaces the items the filter looks through; the rows change at the next filter.
func (p *Pick[T]) SetAll(items []T) { p.All = items }

// filter shows the items whose text holds text (case and the spaces around it aside), and previews
// the first.
func (p *Pick[T]) Filter(text string) {
	if p.Query != nil {
		p.Query(text)
		return
	}
	text = strings.ToLower(strings.TrimSpace(text))
	var kept []T
	for _, x := range p.All {
		if strings.Contains(strings.ToLower(p.Match(x)), text) {
			kept = append(kept, x)
		}
	}
	p.Show(kept)
}

// show lists items as the rows, says how many in the list's title, and previews the first.
func (p *Pick[T]) Show(items []T) {
	p.List.Reset(items)
	if p.Status != "" {
		p.Core.Set(p.Status, p.Core.Trf("autodoc.text.pick.of", p.nounText(), len(items), len(p.All)))
	}
	if x, ok := p.List.At(0); ok {
		p.PreviewItem(x)
		return
	}
	p.showEmpty()
}

// nounText is the picker's noun in the UI's language: the pickers' own words; any other as written.
func (p *Pick[T]) nounText() string {
	switch p.Noun {
	case "buffers":
		return p.Core.Tr("autodoc.text.pick.buffers")
	case "files":
		return p.Core.Tr("autodoc.text.pick.files")
	case "headings":
		return p.Core.Tr("autodoc.text.pick.headings")
	case "items":
		return p.Core.Tr("autodoc.text.pick.items")
	case "agents":
		return p.Core.Tr("autodoc.text.pick.agents")
	case "statuses":
		return p.Core.Tr("autodoc.text.pick.statuses")
	case "plugins and packages":
		return p.Core.Tr("autodoc.text.pick.plugins_and_packages")
	}
	return p.Noun
}

// move previews row i: the list's cursor landed on it.
func (p *Pick[T]) Move(i int) {
	if x, ok := p.List.At(i); ok {
		p.PreviewItem(x)
	}
}

// previewItem shows x in the preview once it is read, unless something newer was asked for.
func (p *Pick[T]) PreviewItem(x T) {
	if p.Preview == nil {
		return
	}
	tk := p.Core.Ticket(&p.seq, p.Scope)
	work := p.Preview(x)
	p.Core.Go(func(ctx context.Context) any { return work(ctx) }, func(v any) {
		if !tk.Current() {
			return
		}
		a := v.(AnswerOf[Pane])
		if p.Shown != nil {
			p.Shown(x, a)
		}
		p.showPane(a.Value)
	})
}

// showEmpty shows the preview of no row; a preview still being read is dropped.
func (p *Pick[T]) showEmpty() {
	p.Core.Ticket(&p.seq, p.Scope)
	var pn Pane
	if p.Empty != nil {
		pn = p.Empty()
	}
	p.showPane(pn)
}

// showPane puts pn in the preview, the cursor at its byte: the position is moved away first, so the
// same number on another text still moves it.
func (p *Pick[T]) showPane(pn Pane) {
	at := min(max(pn.At, 0), len(pn.Text))
	prefix := "App." + p.Name + "Preview"
	p.Core.Set(prefix+"Title", pn.Title)
	p.Core.Set(prefix+"At", -1)
	p.Core.Set(prefix+"Text", pn.Text)
	p.Core.Set(prefix+"At", CursorAt(pn.Text, at))
}

// enterRow is Enter on row i: it chooses the row, or does the picker's own enter with it.
func (p *Pick[T]) EnterRow(i int) {
	x, ok := p.List.At(i)
	if !ok {
		return
	}
	if p.Enter != nil {
		p.Enter(x)
		return
	}
	p.take(x)
}

// accept is the picker's accept (its button, or Enter in its field), row i under the list's cursor
// and text in the field: the row is chosen, or the text submitted.
func (p *Pick[T]) Accept(i int, text string) {
	if p.Submit != nil {
		p.Submit(text)
		return
	}
	if x, ok := p.List.At(i); ok {
		p.take(x)
	}
}

// take closes the picker and chooses x: a preview still being read is dropped first, since a
// Dialog closed by close() emits neither accepted nor rejected, so dismiss does not run.
func (p *Pick[T]) take(x T) {
	p.Core.Ticket(&p.seq, p.Scope)
	p.answering = true
	p.Core.Close(p.Dialog)
	if p.Choose != nil {
		p.Choose(x)
	}
}

// closedDialog is the picker's dialog closed, however: after a choice, nothing more; else lost
// hears it.
func (p *Pick[T]) closedDialog() {
	if p.answering {
		p.answering = false
		return
	}
	if p.Lost != nil {
		p.Lost()
	}
}

// dismiss is the picker closed without a choice: a preview still being read is dropped.
func (p *Pick[T]) Dismiss() {
	p.Core.Ticket(&p.seq, p.Scope)
	if p.Cancel != nil {
		p.Cancel()
	}
}

// The preview of a workspace's file, which the file pickers and the search share.

// picker is a pick of any item, as the App.pick* commands reach it by its name.
type Picker interface {
	Filter(text string)
	Move(i int)
	EnterRow(i int)
	Accept(i int, text string)
	Dismiss()
	closedDialog()
}

// cursorAt is byte at of text as the editor's cursorPosition counts it: characters (grapheme
// clusters) before it, a line break (\r\n, \r or \n) one, as the editor splits them. A byte
// inside a character is that character's start. A count of runes lands late after a combining
// mark or a joined emoji.
func CursorAt(text string, at int) int {
	n, end := 0, 0
	for c := range tuicore.Graphemes(text) {
		if end += len(c); end > at {
			break
		}
		n++
	}
	return n
}

// pickCommands are Picker.qml's handlers: each names its picker first, one of picks.
func PickCommands(picks map[string]Picker) map[string]Command {
	named := func(name string) (Picker, error) {
		if p, ok := picks[name]; ok {
			return p, nil
		}
		return nil, ArgError(fmt.Sprintf("takes a picker's name, not %q", name))
	}
	row := func(what string, v qml.SpecValue) (int, error) {
		n, err := strconv.Atoi(v.Raw)
		if err != nil {
			return 0, ArgError(fmt.Sprintf("takes %s, not %s", what, v.Raw))
		}
		return n, nil
	}
	nameAndRow := func(fn func(Picker, int)) Command {
		const what = "a picker's name and a row"
		return Command{Args: []qml.SpecValueKind{ArgStr, ArgNum}, What: what, Run: func(a []qml.SpecValue) error {
			p, err := named(a[0].Raw)
			if err != nil {
				return err
			}
			n, err := row(what, a[1])
			if err != nil {
				return err
			}
			fn(p, n)
			return nil
		}}
	}
	return map[string]Command{
		"App.pickFilter": {Args: []qml.SpecValueKind{ArgStr, ArgStr}, What: "a picker's name and a filter",
			Run: func(a []qml.SpecValue) error {
				p, err := named(a[0].Raw)
				if err == nil {
					p.Filter(a[1].Raw)
				}
				return err
			}},
		"App.pickMove":   nameAndRow(Picker.Move),
		"App.pickChoose": nameAndRow(Picker.EnterRow),
		"App.pickAccept": {Args: []qml.SpecValueKind{ArgStr, ArgNum, ArgStr}, What: "a picker's name, a row and the field's text",
			Run: func(a []qml.SpecValue) error {
				const what = "a picker's name, a row and the field's text"
				p, err := named(a[0].Raw)
				if err != nil {
					return err
				}
				n, err := row(what, a[1])
				if err != nil {
					return err
				}
				p.Accept(n, a[2].Raw)
				return nil
			}},
		"App.pickCancel": {Args: []qml.SpecValueKind{ArgStr}, What: "a picker's name",
			Run: func(a []qml.SpecValue) error {
				p, err := named(a[0].Raw)
				if err == nil {
					p.Dismiss()
				}
				return err
			}},
		"App.pickClosed": {Args: []qml.SpecValueKind{ArgStr}, What: "a picker's name",
			Run: func(a []qml.SpecValue) error {
				p, err := named(a[0].Raw)
				if err == nil {
					p.closedDialog()
				}
				return err
			}},
	}
}
