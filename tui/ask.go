package tui

import (
	"cmp"
)

// ASK — the one yes-or-no question (Confirm.qml) a feature asks before it does what it cannot
// undo: Core.Ask. The question says what goes and what stays; No is the first button, so Enter
// alone does nothing; Yes runs the asker's OnYes once, on the loop.

// Question is a yes-or-no question. One is asked at a time: a question asked while another is
// open replaces it, and the first's OnCancel runs instead of OnYes.
type Question struct {
	Title string // the card's title, itself a question: "remove the provider?"
	Text  string // what goes, and what stays
	Yes   string // the button that does it, its mnemonic marked: "&Yes, remove it"; "" is "&Yes"
	No    string // the button that does nothing; "" is "&No"
	OnYes func() // run once, on the loop, when Yes is chosen
	// OnCancel finishes an accepted asynchronous request when no Yes arrives.
	// No, Escape, close, replacement and normal host shutdown run it on the loop.
	// Stopped is a fallback for failures that skip normal teardown; in that case
	// it runs after the loop on the stopping goroutine and must not post UI work.
	OnCancel func()
	// Other, when set, is a third button between No and Yes, its mnemonic marked ("&Audit…"):
	// OnOther runs once when it is chosen, and neither OnYes nor OnCancel does, so what was asked
	// lives on (a candidate's Audit…, candidate.go).
	Other   string
	OnOther func()
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
	c             Core
	seq           uint64 // the question asked: a newer one, or an answer, moves it
	yes           func() // the asked question's OnYes; nil once answered
	cancel        func()
	other         func() // its OnOther
	pending       bool
	answerClosing bool // accepted/rejected already answered; its following closed is notification only
	stopped       bool
	// onIdle hears that the card is free again, once an answer left no question open: a queue of
	// questions waiting their turn (the Lua grants, luagate.go) asks its next one then
	onIdle func()
}

func newAsker(c Core) *asker { return &asker{c: c} }

func (*asker) Name() string { return "ask" }

func (*asker) State() map[string]any {
	return map[string]any{"App.askTitle": "", "App.askText": "", "App.askYes": "&Yes", "App.askNo": "&No",
		"App.askOther": "", "App.askOtherShown": false}
}

func (a *asker) Commands() map[string]Command {
	return map[string]Command{
		"App.askAccepted":    none(a.accepted),
		"App.askRejected":    none(a.rejected),
		"App.askClosed":      none(a.closed),
		"App.askOtherChosen": none(a.otherChosen),
	}
}

// ask shows q, replacing any question still open.
func (a *asker) ask(q Question) Asked {
	previous := a.cancel
	a.answered() // retire the old question before its callback may re-enter Ask
	tk := a.c.Ticket(&a.seq, TicketLocal)
	if previous != nil {
		previous()
	}
	if a.stopped || !tk.Current() {
		if q.OnCancel != nil {
			q.OnCancel()
		}
		return Asked{}
	}
	a.yes, a.other = q.OnYes, q.OnOther
	a.cancel, a.pending = q.OnCancel, true
	a.c.Set("App.askTitle", q.Title)
	a.c.Set("App.askText", q.Text)
	a.c.Set("App.askYes", cmp.Or(q.Yes, a.c.Tr("autodoc.question.yes")))
	a.c.Set("App.askNo", cmp.Or(q.No, a.c.Tr("autodoc.question.no")))
	a.c.Set("App.askOther", q.Other)
	a.c.Set("App.askOtherShown", q.Other != "")
	a.c.Open("confirm")
	return Asked{say: func(text string) {
		if tk.Current() {
			a.c.Set("App.askText", text)
		}
	}}
}

// accepted runs the question's OnYes, once.
func (a *asker) accepted() {
	if !a.pending {
		return
	}
	yes := a.yes
	a.answerClosing = true
	a.answered()
	if yes != nil {
		yes()
	}
	a.idle()
}

// otherChosen runs the question's OnOther, once; the card closes without OnCancel. A question
// with no third button has nothing to choose, and stays asked.
func (a *asker) otherChosen() {
	if !a.pending || a.other == nil {
		return
	}
	other := a.other
	a.answerClosing = true
	a.answered()
	if other != nil {
		other()
	}
	a.idle()
}

// idle tells onIdle the card is free, when the answer's callback asked nothing new.
func (a *asker) idle() {
	if !a.pending && !a.stopped && a.onIdle != nil {
		a.onIdle()
	}
}

func (a *asker) rejected() {
	if !a.pending {
		return
	}
	a.answerClosing = true
	a.cancelCurrent()
}

// closed also covers programmatic close, which raises no rejected signal.
// An accepted/rejected signal's following closed must not cancel a question
// its callback opened in the meantime.
func (a *asker) closed() {
	if a.answerClosing {
		a.answerClosing = false
		return
	}
	a.cancelCurrent()
}

func (a *asker) cancelCurrent() {
	cancel := a.cancel
	a.answered()
	if cancel != nil {
		cancel()
	}
	a.idle()
}

// Stopping retires pending work while the host is stopping. Stopped is the
// existing post-loop fallback; a loop-owned stopping hook can call this earlier.
func (a *asker) Stopping() {
	a.stopped = true
	a.cancelCurrent()
}
func (a *asker) Stopped() { a.Stopping() }

// answered ends the question: a late Say, or a second accept, does nothing.
func (a *asker) answered() {
	a.seq++
	a.yes, a.other = nil, nil
	a.cancel, a.pending = nil, false
}
