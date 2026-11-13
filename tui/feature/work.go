package feature

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"unsafe"
)

// coreDo is do for a feature, through its Core: fn off the loop, then on it with fn's result, typed.
func CoreDo[T any](c Core, fn func(context.Context) T, then func(T)) {
	c.Go(func(ctx context.Context) any { return fn(ctx) }, func(v any) {
		if p, panicked := v.(ErrPanicked); panicked {
			// fn panicked: then gets the failure in its own answer, never a zero answer that reads as
			// success (an empty clone, a finished update)
			if t, ok := Failed[T](p); ok {
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

// AnswerOf is a background answer: a value, or why there is none. An answer of several values is
// a struct of its own; one value and its error is this.
type AnswerOf[T any] struct {
	Value T
	err   error
}

// Answered is a value and its error as an answerOf.
func Answered[T any](v T, err error) AnswerOf[T] { return AnswerOf[T]{Value: v, err: err} }

// AnswerFailed is an answer of no value: why there is none.
func AnswerFailed[T any](err error) AnswerOf[T] { return AnswerOf[T]{err: err} }

// Err is why the answer has no value; nil when it has one. The field stays unexported: failed (below)
// finds every answer's err by its name.
func (a AnswerOf[T]) Err() error { return a.err }

// errPanicked is a background job's panic, as the error its caller is handed.
type ErrPanicked struct {
	Value any
	Path  string
}

// failed is a background answer carrying err, the failure its job ended in: err itself where the
// answer is an error or any (a Core.Go answer), else the answer's own err field (answerOf, and every
// struct a job answers with: TestEveryBackgroundAnswerCanCarryAFailure holds each do and coreDo to
// one). The field is unexported, set through its address; its type is checked first. ok is false
// for an answer with none, which that test keeps from being written.
func Failed[T any](err error) (v T, ok bool) {
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

// panicText is what a log or a notice says of a panic's value: the runtime's own message (cut to
// panicTextMax characters), or only its type, its message withheld (tui/crash.go's top).
func PanicText(v any) string {
	if re, ok := v.(runtime.Error); ok && runtimeAuthored(v) {
		s := re.Error()
		if r := []rune(s); len(r) > PanicTextMax {
			s = string(r[:PanicTextMax]) + "…"
		}
		return s
	}
	return fmt.Sprintf("a %T (its message withheld: it may hold the page's text or what was typed)", v)
}

// runtimeAuthored is whether the Go runtime itself raised v: its type is declared in package runtime
// (an index out of range, a nil dereference, a failed type assertion). runtime.Error alone is an
// open interface any type can implement, carrying whatever text it likes; no other package can
// declare a type in runtime.
func runtimeAuthored(v any) bool {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t != nil && t.PkgPath() == "runtime"
}

func (e ErrPanicked) Error() string {
	s := "it panicked: " + PanicText(e.Value)
	if e.Path != "" {
		s += " — details in " + e.Path
	}
	return s
}

// panicTextMax is the characters of a panic's message a log and a notice keep.
const PanicTextMax = 500
