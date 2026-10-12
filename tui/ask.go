package tui

import (
	"cmp"
)

// ASK — the one yes-or-no question (Confirm.qml) a feature asks before it does what it cannot
// undo: Core.Ask. The question says what goes and what stays; No is the first button, so Enter
// alone does nothing; Yes runs the asker's OnYes once, on the loop.

// Question is a yes-or-no question. One is asked at a time: a question asked while another is
// open replaces it, and the first's OnYes never runs.
type Question struct {
	Title string // the card's title, itself a question: "remove the provider?"
	Text  string // what goes, and what stays
	Yes   string // the button that does it, its mnemonic marked: "&Yes, remove it"; "" is "&Yes"
	No    string // the button that does nothing; "" is "&No"
	OnYes func() // run once, on the loop, when Yes is chosen
}

// Asked is a question while it is asked.
type Asked struct{ say func(text string) }

// Say rewrites the question's text while it is still the one asked: what an answer counted after
// the card opened. A newer question, or an answer given, makes it a no-op.
func (a Asked) Say(text string) {
	if a.say != nil {
		a.say(text)
	}
}

// ask is Core.Ask, for the host's own code that is not a feature yet.
func (h *Host) ask(q Question) Asked { return h.asker.ask(q) }

// asker is the feature that owns the card.
type asker struct {
	c   Core
	seq uint64 // the question asked: a newer one, or an answer, moves it
	yes func() // the asked question's OnYes; nil once answered
}

func newAsker(c Core) *asker { return &asker{c: c} }

func (*asker) Name() string { return "ask" }

func (*asker) State() map[string]any {
	return map[string]any{"App.askTitle": "", "App.askText": "", "App.askYes": "&Yes", "App.askNo": "&No"}
}

func (a *asker) Commands() map[string]Command {
	return map[string]Command{
		"App.askAccepted": none(a.accepted),
		"App.askRejected": none(a.rejected),
	}
}

// ask shows q, replacing any question still open.
func (a *asker) ask(q Question) Asked {
	tk := a.c.Ticket(&a.seq, TicketLocal)
	a.yes = q.OnYes
	a.c.Set("App.askTitle", q.Title)
	a.c.Set("App.askText", q.Text)
	a.c.Set("App.askYes", cmp.Or(q.Yes, "&Yes"))
	a.c.Set("App.askNo", cmp.Or(q.No, "&No"))
	a.c.Open("confirm")
	return Asked{say: func(text string) {
		if tk.Current() {
			a.c.Set("App.askText", text)
		}
	}}
}

// accepted runs the question's OnYes, once.
func (a *asker) accepted() {
	yes := a.yes
	a.answered()
	if yes != nil {
		yes()
	}
}

func (a *asker) rejected() { a.answered() }

// answered ends the question: a late Say, or a second accept, does nothing.
func (a *asker) answered() {
	a.seq++
	a.yes = nil
}
