package app

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

// TestChooseDerivesItsChecksFromTheTable: the refusals name the table's own modes and flags, and a
// mode's argument, flags after it included, is handed to it.
func TestChooseDerivesItsChecksFromTheTable(t *testing.T) {
	table := func() (*flag.FlagSet, *bytes.Buffer, []mode, []companion) {
		var out bytes.Buffer
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(&out)
		fs.Bool("a", false, "")
		fs.Bool("b", false, "")
		fs.Bool("c", false, "")
		fs.String("x", "", "")
		fs.Bool("y", false, "")
		fs.Bool("z", false, "")
		modes := []mode{
			{flag: "a", oneOf: []string{"y", "z"}, run: func(string) int { return 10 }},
			{flag: "b", arg: true, run: func(string) int { return 11 }},
			{flag: "c", arg: true, flagsAfter: true, run: func(string) int { return 12 }},
		}
		return fs, &out, modes, []companion{{flags: []string{"x"}, modes: []string{"b", "c"}}}
	}
	for _, c := range []struct {
		args []string
		ok   bool
		run  int
		arg  string
		says string
	}{
		{args: []string{"-a"}, ok: true, run: 10},
		{args: []string{"-b", "w"}, ok: true, run: 11, arg: "w"},
		{args: []string{"-c", "f", "-x", "v"}, ok: true, run: 12, arg: "f"},
		{args: []string{"-b", "w", "-x", "v"}, says: "unexpected arguments: [w -x v] (only --b and --c take one argument)"},
		{args: []string{"-c", "f", "g"}, says: "unexpected arguments: [g]"},
		{args: []string{"-a", "w"}, says: "unexpected arguments: [w]"},
		{args: []string{"-a", "-x", "v"}, says: "--x is for --b and --c"},
		{args: []string{"-x", "v", "-b"}, ok: true, run: 11},
		{args: []string{"-a", "-b", "w"}, says: "autodoc: --a and --b are separate modes: give one\n"},
		{args: []string{"-c", "f", "-a", "-b"}, says: "autodoc: --a, --b and --c are separate modes: give one\n"},
		{args: []string{"-a", "-y"}, ok: true, run: 10},
		{args: []string{"-a", "-z", "-y"}, says: "autodoc: --a takes one of --y and --z, not both\n"},
		{args: nil, says: "Usage of t"},
	} {
		fs, out, modes, companions := table()
		m, arg, _, ok := choose(fs, c.args, modes, companions)
		if ok != c.ok || ok && (m.run(arg) != c.run || arg != c.arg) || !strings.Contains(out.String(), c.says) {
			t.Errorf("%v: ok %v, arg %q, said %q; want ok %v running %d with %q, saying %q", c.args, ok, arg, out.String(), c.ok, c.run, c.arg, c.says)
		}
	}
}

func TestList(t *testing.T) {
	for in, want := range map[string]string{"": "", "a": "a", "a b": "a and b", "a b c": "a, b and c"} {
		if got := list(strings.Fields(in)); got != want {
			t.Errorf("list(%q) = %q, want %q", in, got, want)
		}
	}
}
