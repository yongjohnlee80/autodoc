package tui

import (
	"context"
	"reflect"
	"unsafe"
)

// coreDo is do for a feature, through its Core: fn off the loop, then on it with fn's result, typed.
func coreDo[T any](c Core, fn func(context.Context) T, then func(T)) {
	c.Go(func(ctx context.Context) any { return fn(ctx) }, func(v any) {
		if p, panicked := v.(errPanicked); panicked {
			// fn panicked: then gets the failure in its own answer, never a zero answer that reads as
			// success (an empty clone, a finished update)
			if t, ok := failed[T](p); ok {
				then(t)
			} else {
				c.Notify(c.Trf("autodoc.notice.work.a_background_job_failed", p.Error()))
			}
			return
		}
		t, _ := v.(T) // a nil error comes back as nil, not a failed assertion
		then(t)
	})
}

// answerOf is a background answer: a value, or why there is none. An answer of several values is
// a struct of its own; one value and its error is this.
type answerOf[T any] struct {
	v   T
	err error
}

// answered is a value and its error as an answerOf.
func answered[T any](v T, err error) answerOf[T] { return answerOf[T]{v: v, err: err} }

// errPanicked is a background job's panic, as the error its caller is handed.
type errPanicked struct {
	value any
	path  string
}

// failed is a background answer carrying err, the failure its job ended in: err itself where the
// answer is an error or any (a Core.Go answer), else the answer's own err field (answerOf, and every
// struct a job answers with: TestEveryBackgroundAnswerCanCarryAFailure holds each do and coreDo to
// one). The field is unexported, set through its address; its type is checked first. ok is false
// for an answer with none, which that test keeps from being written.
func failed[T any](err error) (v T, ok bool) {
	if t, isT := any(err).(T); isT {
		return t, true
	}
	rv := reflect.ValueOf(&v).Elem()
	if rv.Kind() != reflect.Struct {
		return v, false
	}
	f := rv.FieldByName("err")
	if !f.IsValid() || f.Type() != reflect.TypeFor[error]() {
		return v, false
	}
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(&err).Elem())
	return v, true
}
