package tui

import "context"

// BACKGROUND WORK — off the loop, and back onto it.
//
// Nothing that talks to the daemon runs on the UI loop. do runs fn on its own goroutine under the
// host's context and applies its result on the loop with then. The result says which generation it
// was issued under; then decides whether it still applies. A late answer to a question nobody is
// asking any more is dropped there, never here.
func do[T any](h *Host, fn func(context.Context) T, then func(T)) {
	ctx := h.ctx
	go func() {
		v := fn(ctx)
		if ctx.Err() != nil {
			return // the program stopped; nobody is waiting
		}
		h.p.Post(func() { then(v) })
	}()
}

// answerOf is a background answer: a value, or why there is none. An answer of several values is
// a struct of its own; one value and its error is this.
type answerOf[T any] struct {
	v   T
	err error
}

// answered is a value and its error as an answerOf.
func answered[T any](v T, err error) answerOf[T] { return answerOf[T]{v: v, err: err} }
