package app

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// mode is one of the binary's modes, asked for by its flag. run's table lists them; the checks made
// before any mode runs are derived from it, so a new mode is one row and touches no check.
type mode struct {
	flag string
	// asked is whether the mode was asked for; nil: its flag was given
	asked func() bool
	// arg: the mode takes one argument after the flags (a workspace, a verb's params, a file)
	arg bool
	// flagsAfter: flags may follow the argument too (autodoc --contracts todos.* --format md)
	flagsAfter bool
	// run runs the mode with its argument ("" for none) and is the process's exit code
	run func(arg string) int
}

// companion is flags that only some modes read: given without one of them asked, they are refused.
type companion struct {
	flags []string
	modes []string
	// says is the refusal; "" says "--<flag> is for --<mode>"
	says string
}

// choose parses args and picks the mode to run, with its argument: one mode, two asked together
// are refused. ok false is a refusal, already said on fs's output, and the exit code to return.
func choose(fs *flag.FlagSet, args []string, modes []mode, companions []companion) (m mode, arg string, code int, ok bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return mode{}, "", 0, false
		}
		return mode{}, "", 2, false
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	asked := func(m mode) bool {
		if m.asked != nil {
			return m.asked()
		}
		return given[m.flag]
	}
	byFlag := map[string]mode{}
	var takers []string
	for _, m := range modes {
		byFlag[m.flag] = m
		if m.arg {
			takers = append(takers, "--"+m.flag)
		}
	}
	positional := fs.Args()
	// shown is what a refusal names: what is left after a reparse, else every argument
	rest, shown, took := positional, positional, false
	for _, m := range modes {
		if !m.arg || !asked(m) || len(positional) == 0 {
			continue
		}
		arg, rest, took = positional[0], positional[1:], true
		if m.flagsAfter {
			if err := fs.Parse(rest); err != nil {
				return mode{}, "", 2, false
			}
			fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
			rest, shown = fs.Args(), fs.Args()
		}
		break
	}
	if len(rest) > 0 || !took && len(positional) > 0 {
		fmt.Fprintf(fs.Output(), "autodoc: unexpected arguments: %v (only %s take one argument)\n", shown, list(takers))
		return mode{}, "", 2, false
	}
	var chosen []string
	for _, m := range modes {
		if asked(m) {
			chosen = append(chosen, "--"+m.flag)
		}
	}
	if len(chosen) > 1 {
		fmt.Fprintf(fs.Output(), "autodoc: %s are separate modes: give one\n", list(chosen))
		return mode{}, "", 2, false
	}
	for _, c := range companions {
		stray := ""
		for _, f := range c.flags {
			if given[f] {
				stray = f
				break
			}
		}
		if stray == "" || anyAsked(c.modes, byFlag, asked) {
			continue
		}
		says := c.says
		if says == "" {
			says = fmt.Sprintf("--%s is for %s", stray, list(dashed(c.modes)))
		}
		fmt.Fprintln(fs.Output(), "autodoc:", says)
		return mode{}, "", 2, false
	}
	for _, m := range modes {
		if asked(m) {
			return m, arg, 0, true
		}
	}
	fs.Usage()
	return mode{}, "", 2, false
}

func anyAsked(names []string, byFlag map[string]mode, asked func(mode) bool) bool {
	for _, n := range names {
		if asked(byFlag[n]) {
			return true
		}
	}
	return false
}

func dashed(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "--" + n
	}
	return out
}

// list is names as prose: "a", "a and b", "a, b and c".
func list(names []string) string {
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
