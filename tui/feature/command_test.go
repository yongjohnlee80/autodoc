package feature

import (
	"errors"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// The registry checks a command's arguments and names it in every refusal of them; the command's
// own errors pass through as they are.
func TestACommandIsCheckedAndNamedByTheRegistry(t *testing.T) {
	s := func(v string) qml.SpecValue { return qml.SpecValue{Kind: qml.SpecValueString, Raw: v} }
	n := func(v string) qml.SpecValue { return qml.SpecValue{Kind: qml.SpecValueNumber, Raw: v} }
	b := qml.SpecValue{Kind: qml.SpecValueBool, Raw: "true"}

	var got []any
	row := NumberAndString("a row and an action", func(i int, a string) { got = []any{i, a} }).Handler("App.rowAction")
	if err := row([]qml.SpecValue{n("2"), s("open")}); err != nil || got[0] != 2 || got[1] != "open" {
		t.Fatalf("(2, open): %v %v", got, err)
	}
	for _, c := range []struct {
		args []qml.SpecValue
		want string
	}{
		{[]qml.SpecValue{s("2"), s("open")}, "App.rowAction takes a row and an action"}, // the kind numberAndString never checked
		{[]qml.SpecValue{n("2")}, "App.rowAction takes a row and an action"},
		{[]qml.SpecValue{n("2.5"), s("open")}, "App.rowAction takes a row and an action, not 2.5"},
	} {
		got = nil
		if err := row(c.args); err == nil || err.Error() != c.want || got != nil {
			t.Errorf("%v: %v (want %q), ran with %v", c.args, err, c.want, got)
		}
	}

	if err := None(func() {}).Handler("App.save")([]qml.SpecValue{b}); err == nil || err.Error() != "App.save takes no arguments, and was given 1" {
		t.Errorf("an argument to none: %v", err)
	}
	if err := OneBool(func(bool) {}).Handler("App.wrote")([]qml.SpecValue{s("yes")}); err == nil || err.Error() != "App.wrote takes true or false" {
		t.Errorf("a string for a bool: %v", err)
	}

	own := errors.New("the row is gone")
	pick := OneIndex(func(tuidecl.Index) error { return own }).Handler("App.picked")
	if err := pick([]qml.SpecValue{{Obj: tuidecl.Index{}}}); !errors.Is(err, own) || err.Error() != own.Error() {
		t.Errorf("the command's own error: %v", err)
	}
	if err := pick([]qml.SpecValue{n("3")}); err == nil || err.Error() != "App.picked takes a row's index, not 3" {
		t.Errorf("a number for an index: %v", err)
	}
	place := StringAndFourNumbers("a panel and its place", func(string, float64, float64, float64, float64) {}).Handler("App.placed")
	if err := place([]qml.SpecValue{s("agent"), n("1"), n("2"), n("x"), n("4")}); err == nil || err.Error() != "App.placed takes a panel and its place, not x" {
		t.Errorf("a malformed fraction: %v", err)
	}
	for _, c := range []Command{OneString("a", func(string) {}), TwoStrings("b", func(string, string) {}),
		FourStrings("c", func(_, _, _, _ string) {}), FiveStrings("d", func(_, _, _, _, _ string) {})} {
		args := make([]qml.SpecValue, len(c.Args))
		for i := range args {
			args[i] = s("x")
		}
		if err := c.Handler("App.x")(args); err != nil {
			t.Errorf("%s: %v", c.What, err)
		}
	}
}
