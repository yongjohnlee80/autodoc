package tui

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
type pane struct {
	title, text string
	at          int
}

// pick is one picker: the items it holds, the rows it shows of them, its preview and its choice.
// Every method runs on the loop.
type pick[T any] struct {
	c      Core
	dialog string // the Dialog's id, which choosing closes: "fileOpen"
	// name is the picker's to Picker.qml's handlers (App.pickFilter(name, text)); its preview binds
	// App.<name>PreviewTitle, App.<name>PreviewText and App.<name>PreviewAt
	name string
	list *rows[T] // the rows shown: the items the filter keeps
	all  []T      // every item the filter looks through

	// field, when set, is the App source the filter field binds: open empties it
	field string
	// status, when set, is the App source of the list's title: "<noun> (shown of all)"
	status, noun string
	// match is the text of an item the filter looks in, case aside
	match func(T) string
	// query, when set, is a filter of the picker's own (the search's, by the daemon): it shows what
	// it finds with show
	query func(text string)

	// preview, on the loop, is the work that reads an item for the preview; the work runs off the
	// loop, the latest asked for winning. An answer's error is why the item could not be read: its
	// pane says so. nil: no preview.
	preview func(T) func(context.Context) answerOf[pane]
	// shown, when set, runs on the loop just before a current preview shows (the search's Rendered
	// switch)
	shown func(T, answerOf[pane])
	// empty is what the preview shows when no row is: nil, an empty pane
	empty func() pane
	scope TicketScope // what makes a preview's answer stale, besides a newer one
	seq   uint64

	// choose takes an item: accept on a row, the dialog closed first
	choose func(T)
	// enter, when set, is what Enter on a row does instead of choosing (the draft's name takes the
	// row's folder)
	enter func(T)
	// submit, when set, is what accept does with the field's text instead of choosing a row (the
	// draft's name creates the file)
	submit func(text string)
	// cancel, when set, runs when the dialog is dismissed without a choice
	cancel func()
}

// open opens the picker over its items, the field emptied and every item listed.
func (p *pick[T]) open() {
	if p.field != "" {
		p.c.Set(p.field, "\x00") // moved away first, so an empty field twice still empties it
		p.c.Set(p.field, "")
	}
	p.filter("")
	p.c.Open(p.dialog)
}

// setAll replaces the items the filter looks through; the rows change at the next filter.
func (p *pick[T]) setAll(items []T) { p.all = items }

// filter shows the items whose text holds text (case and the spaces around it aside), and previews
// the first.
func (p *pick[T]) filter(text string) {
	if p.query != nil {
		p.query(text)
		return
	}
	text = strings.ToLower(strings.TrimSpace(text))
	var kept []T
	for _, x := range p.all {
		if strings.Contains(strings.ToLower(p.match(x)), text) {
			kept = append(kept, x)
		}
	}
	p.show(kept)
}

// show lists items as the rows, says how many in the list's title, and previews the first.
func (p *pick[T]) show(items []T) {
	p.list.Reset(items)
	if p.status != "" {
		p.c.Set(p.status, fmt.Sprintf("%s (%d of %d)", p.noun, len(items), len(p.all)))
	}
	if x, ok := p.list.At(0); ok {
		p.previewItem(x)
		return
	}
	p.showEmpty()
}

// move previews row i: the list's cursor landed on it.
func (p *pick[T]) move(i int) {
	if x, ok := p.list.At(i); ok {
		p.previewItem(x)
	}
}

// previewItem shows x in the preview once it is read, unless something newer was asked for.
func (p *pick[T]) previewItem(x T) {
	if p.preview == nil {
		return
	}
	tk := p.c.Ticket(&p.seq, p.scope)
	work := p.preview(x)
	p.c.Go(func(ctx context.Context) any { return work(ctx) }, func(v any) {
		if !tk.Current() {
			return
		}
		a := v.(answerOf[pane])
		if p.shown != nil {
			p.shown(x, a)
		}
		p.showPane(a.v)
	})
}

// showEmpty shows the preview of no row; a preview still being read is dropped.
func (p *pick[T]) showEmpty() {
	p.c.Ticket(&p.seq, p.scope)
	var pn pane
	if p.empty != nil {
		pn = p.empty()
	}
	p.showPane(pn)
}

// showPane puts pn in the preview, the cursor at its byte: the position is moved away first, so the
// same number on another text still moves it.
func (p *pick[T]) showPane(pn pane) {
	at := min(max(pn.at, 0), len(pn.text))
	prefix := "App." + p.name + "Preview"
	p.c.Set(prefix+"Title", pn.title)
	p.c.Set(prefix+"At", -1)
	p.c.Set(prefix+"Text", pn.text)
	p.c.Set(prefix+"At", cursorAt(pn.text, at))
}

// enterRow is Enter on row i: it chooses the row, or does the picker's own enter with it.
func (p *pick[T]) enterRow(i int) {
	x, ok := p.list.At(i)
	if !ok {
		return
	}
	if p.enter != nil {
		p.enter(x)
		return
	}
	p.take(x)
}

// accept is the picker's accept (its button, or Enter in its field), row i under the list's cursor
// and text in the field: the row is chosen, or the text submitted.
func (p *pick[T]) accept(i int, text string) {
	if p.submit != nil {
		p.submit(text)
		return
	}
	if x, ok := p.list.At(i); ok {
		p.take(x)
	}
}

// take closes the picker and chooses x.
func (p *pick[T]) take(x T) {
	p.c.Close(p.dialog)
	if p.choose != nil {
		p.choose(x)
	}
}

// dismiss is the picker closed without a choice: a preview still being read is dropped.
func (p *pick[T]) dismiss() {
	p.c.Ticket(&p.seq, p.scope)
	if p.cancel != nil {
		p.cancel()
	}
}

// The preview of a workspace's file, which the file pickers and the search share.

// readPreview is the work that reads file p of the workspace for a picker's preview, the cursor at
// byte at: through the daemon, off the loop. held is a hit of a document another backend's build
// indexed, which says so when this one cannot read it.
func (h *Host) readPreview(p string, at int, held bool) func(context.Context) answerOf[pane] {
	ws := h.ws
	return func(ctx context.Context) answerOf[pane] {
		res, err := h.call(ctx, "doc.read", ws, p)
		if err != nil {
			why := wireMessage(err)
			if held {
				// a held hit this backend cannot read (a PDF under a community build): say why
				why = "held: this backend's build does not read this file; it was indexed by another (" + why + ")"
			}
			return answerOf[pane]{v: pane{title: p + " · " + why}, err: err}
		}
		b, _ := asMap(res)["content"].([]byte)
		return answerOf[pane]{v: pane{title: p, text: string(b), at: at}}
	}
}

// picker is a pick of any item, as the App.pick* commands reach it by its name.
type picker interface {
	filter(text string)
	move(i int)
	enterRow(i int)
	accept(i int, text string)
	dismiss()
}

// cursorAt is byte at of text as the editor's cursorPosition counts it: characters (grapheme
// clusters) before it, a line break (\r\n, \r or \n) one, as the editor splits them. A byte
// inside a character is that character's start. A count of runes lands late after a combining
// mark or a joined emoji.
func cursorAt(text string, at int) int {
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
func pickCommands(picks map[string]picker) map[string]Command {
	named := func(name string) (picker, error) {
		if p, ok := picks[name]; ok {
			return p, nil
		}
		return nil, argError(fmt.Sprintf("takes a picker's name, not %q", name))
	}
	row := func(what string, v qml.SpecValue) (int, error) {
		n, err := strconv.Atoi(v.Raw)
		if err != nil {
			return 0, argError(fmt.Sprintf("takes %s, not %s", what, v.Raw))
		}
		return n, nil
	}
	nameAndRow := func(fn func(picker, int)) Command {
		const what = "a picker's name and a row"
		return Command{Args: []qml.SpecValueKind{argStr, argNum}, What: what, Run: func(a []qml.SpecValue) error {
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
		"App.pickFilter": {Args: []qml.SpecValueKind{argStr, argStr}, What: "a picker's name and a filter",
			Run: func(a []qml.SpecValue) error {
				p, err := named(a[0].Raw)
				if err == nil {
					p.filter(a[1].Raw)
				}
				return err
			}},
		"App.pickMove":   nameAndRow(picker.move),
		"App.pickChoose": nameAndRow(picker.enterRow),
		"App.pickAccept": {Args: []qml.SpecValueKind{argStr, argNum, argStr}, What: "a picker's name, a row and the field's text",
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
				p.accept(n, a[2].Raw)
				return nil
			}},
		"App.pickCancel": {Args: []qml.SpecValueKind{argStr}, What: "a picker's name",
			Run: func(a []qml.SpecValue) error {
				p, err := named(a[0].Raw)
				if err == nil {
					p.dismiss()
				}
				return err
			}},
	}
}
