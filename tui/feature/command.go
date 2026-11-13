package feature

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/yongjohnlee80/golib/decl"
	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// COMMANDS — one App.* command: the arguments it takes and what it runs. A feature files each
// under its full name ("App.openTodos"); the registry checks the arguments against Args before Run
// sees them, and puts the name into every refusal, so no command repeats it.

// Command is one command the document may invoke.
type Command struct {
	// Args are the kinds it takes, in order; none: no arguments. qml.SpecValueInvalid takes any
	// value (a view's row Index), which Run checks itself.
	Args []qml.SpecValueKind
	What string // the arguments in words, for a refusal: "a panel and its size and length"
	// Run runs it with Args checked. A refusal of a value (an argError) gets the command's name in
	// front; any other error is the command's own, passed on as it is.
	Run  func(args []qml.SpecValue) error
	Help string // what it does, for a catalog of the commands
}

// argError is a command refusing an argument's value, said after its name: "takes a row, not 1.5".
type ArgError string

func (e ArgError) Error() string { return string(e) }

// handler is c as golib calls it under name: its arguments checked, each refusal naming it.
func (c Command) Handler(name string) decl.HandlerFunc {
	return func(args []qml.SpecValue) error {
		defer renamePanic(name) // a panic says which command it came out of (tui/crash.go)
		if len(c.Args) == 0 && len(args) > 0 {
			return fmt.Errorf("%s takes no arguments, and was given %d", name, len(args))
		}
		if len(args) != len(c.Args) {
			return fmt.Errorf("%s takes %s", name, c.What)
		}
		for i, k := range c.Args {
			if k != qml.SpecValueInvalid && args[i].Kind != k {
				return fmt.Errorf("%s takes %s", name, c.What)
			}
		}
		err := c.Run(args)
		var ae ArgError
		if errors.As(err, &ae) {
			return fmt.Errorf("%s %s", name, ae)
		}
		return err
	}
}

// The commands by the arguments they take.

const (
	ArgStr = qml.SpecValueString
	ArgNum = qml.SpecValueNumber
)

func None(fn func()) Command {
	return Command{Run: func([]qml.SpecValue) error { fn(); return nil }}
}

// twoStringsAndBool takes two strings and true or false: a panel, a Drawer's changed operation and
// its maximized flag.
func TwoStringsAndBool(what string, fn func(string, string, bool)) Command {
	return Command{Args: []qml.SpecValueKind{ArgStr, ArgStr, qml.SpecValueBool}, What: what,
		Run: func(a []qml.SpecValue) error { fn(a[0].Raw, a[1].Raw, a[2].Raw == "true"); return nil }}
}

// oneBool takes true or false: an Editor signal's.
func OneBool(fn func(bool)) Command {
	return Command{Args: []qml.SpecValueKind{qml.SpecValueBool}, What: "true or false",
		Run: func(a []qml.SpecValue) error { fn(a[0].Raw == "true"); return nil }}
}

func OneString(what string, fn func(string)) Command {
	return Command{Args: []qml.SpecValueKind{ArgStr}, What: what,
		Run: func(a []qml.SpecValue) error { fn(a[0].Raw); return nil }}
}

func TwoStrings(what string, fn func(string, string)) Command {
	return Command{Args: []qml.SpecValueKind{ArgStr, ArgStr}, What: what,
		Run: func(a []qml.SpecValue) error { fn(a[0].Raw, a[1].Raw); return nil }}
}

func ThreeStrings(what string, fn func(string, string, string)) Command {
	return Command{Args: []qml.SpecValueKind{ArgStr, ArgStr, ArgStr}, What: what,
		Run: func(a []qml.SpecValue) error { fn(a[0].Raw, a[1].Raw, a[2].Raw); return nil }}
}

func FourStrings(what string, fn func(a, b, c, d string)) Command {
	return Command{Args: []qml.SpecValueKind{ArgStr, ArgStr, ArgStr, ArgStr}, What: what,
		Run: func(a []qml.SpecValue) error { fn(a[0].Raw, a[1].Raw, a[2].Raw, a[3].Raw); return nil }}
}

func FiveStrings(what string, fn func(a, b, c, d, e string)) Command {
	return Command{Args: []qml.SpecValueKind{ArgStr, ArgStr, ArgStr, ArgStr, ArgStr}, What: what,
		Run: func(a []qml.SpecValue) error { fn(a[0].Raw, a[1].Raw, a[2].Raw, a[3].Raw, a[4].Raw); return nil }}
}

// oneNumber takes a whole number.
func OneNumber(what string, fn func(int)) Command {
	return Command{Args: []qml.SpecValueKind{ArgNum}, What: what, Run: func(a []qml.SpecValue) error {
		n, err := strconv.Atoi(a[0].Raw)
		if err != nil {
			return ArgError(fmt.Sprintf("takes %s, not %s", what, a[0].Raw))
		}
		fn(n)
		return nil
	}}
}

// numberAndString takes a whole number and a string: a row and what to do with it.
func NumberAndString(what string, fn func(int, string)) Command {
	return Command{Args: []qml.SpecValueKind{ArgNum, ArgStr}, What: what, Run: func(a []qml.SpecValue) error {
		n, err := strconv.Atoi(a[0].Raw)
		if err != nil {
			return ArgError(fmt.Sprintf("takes %s, not %s", what, a[0].Raw))
		}
		fn(n, a[1].Raw)
		return nil
	}}
}

// numberAndTwoStrings takes a whole number and two strings: a row and two of a form's fields.
func NumberAndTwoStrings(what string, fn func(int, string, string)) Command {
	return Command{Args: []qml.SpecValueKind{ArgNum, ArgStr, ArgStr}, What: what, Run: func(a []qml.SpecValue) error {
		n, err := strconv.Atoi(a[0].Raw)
		if err != nil {
			return ArgError(fmt.Sprintf("takes %s, not %s", what, a[0].Raw))
		}
		fn(n, a[1].Raw, a[2].Raw)
		return nil
	}}
}

// stringAndTwoNumbers takes a name and two whole numbers: a panel and its size and length.
func StringAndTwoNumbers(what string, fn func(string, int, int)) Command {
	return Command{Args: []qml.SpecValueKind{ArgStr, ArgNum, ArgNum}, What: what, Run: func(a []qml.SpecValue) error {
		x, err1 := strconv.Atoi(a[1].Raw)
		y, err2 := strconv.Atoi(a[2].Raw)
		if err1 != nil || err2 != nil {
			return ArgError(fmt.Sprintf("takes %s, not %s and %s", what, a[1].Raw, a[2].Raw))
		}
		fn(a[0].Raw, x, y)
		return nil
	}}
}

// stringAndFourNumbers takes a name and four numbers, fractions allowed: a panel's placement.
func StringAndFourNumbers(what string, fn func(string, float64, float64, float64, float64)) Command {
	return Command{Args: []qml.SpecValueKind{ArgStr, ArgNum, ArgNum, ArgNum, ArgNum}, What: what, Run: func(a []qml.SpecValue) error {
		var n [4]float64
		for i, v := range a[1:] {
			f, err := strconv.ParseFloat(v.Raw, 64)
			if err != nil {
				return ArgError(fmt.Sprintf("takes %s, not %s", what, v.Raw))
			}
			n[i] = f
		}
		fn(a[0].Raw, n[0], n[1], n[2], n[3])
		return nil
	}}
}

// oneIndex takes a view's row Index, as a tree's signal gives it.
func OneIndex(fn func(tuidecl.Index) error) Command {
	return Command{Args: []qml.SpecValueKind{qml.SpecValueInvalid}, What: "a row's index", Run: func(a []qml.SpecValue) error {
		ix, ok := a[0].Obj.(tuidecl.Index)
		if !ok {
			return ArgError("takes a row's index, not " + a[0].Raw)
		}
		return fn(ix)
	}}
}

// namedPanic is a panic and the command it came out of, named on its way up to golib's boundary.
type NamedPanic struct {
	Name  string
	Value any
}

func (p NamedPanic) String() string { return fmt.Sprint(p.Value) }

// renamePanic, deferred in a command, names a panic coming out of it and raises it again, its stack
// kept, for golib's boundary to contain or not: a command does not contain its own panics, since
// only golib knows whether going on is safe (its tree part way through a change). The innermost
// name stays: a catalog entry run by an App.* command is the entry's.
func renamePanic(name string) {
	if rec := recover(); rec != nil {
		if _, named := rec.(NamedPanic); !named {
			rec = NamedPanic{Name: name, Value: rec}
		}
		panic(rec)
	}
}
