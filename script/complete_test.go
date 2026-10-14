package script

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// complete is golib's Command.Complete: a list as it is (strings and numbers), a function called
// with ed, the word, the words before it and cmd. A function's error, or an answer that is not a
// list, is said and offers nothing; a complete of another type refuses the command.
func TestCompleteOffersArguments(t *testing.T) {
	e, core, said := engine(t, "alpha\nbeta")
	if err := e.Load("c.lua", []byte(`
editor.command{ name = "fixed", complete = {"one", 2, true, "three"}, run = function() end }
editor.command{ name = "live", aliases = {"lv"}, run = function() end,
  complete = function(ed, word, args, cmd)
    return {ed:line(1), word, table.concat(args, "+"), cmd.name, tostring(cmd.bang)}
  end }
editor.command{ name = "boom", run = function() end, complete = function() error("no list", 0) end }
editor.command{ name = "odd", run = function() end, complete = function() return "a string" end }
`)); err != nil {
		t.Fatal(err)
	}
	complete := func(name string, x widget.CompleteContext) []string {
		t.Helper()
		c, ok := core.Commands().Lookup(name)
		if !ok || c.Complete == nil {
			t.Fatalf("%s has no Complete", name)
		}
		return c.Complete(x)
	}
	if got := complete("fixed", widget.CompleteContext{}); strings.Join(got, " ") != "one 2 three" {
		t.Fatalf("a list: %q", got)
	}
	got := complete("live", widget.CompleteContext{Core: core, Name: "lv", Bang: true, Args: []string{"a", "b"}, Word: "be"})
	if strings.Join(got, "|") != "alpha|be|a+b|lv|true" {
		t.Fatalf("a function: %q", got)
	}
	*said = nil
	if got := complete("boom", widget.CompleteContext{}); got != nil || strings.Join(*said, "|") != "boom's complete: no list" {
		t.Fatalf("an error: %q, said %q", got, *said)
	}
	*said = nil
	if got := complete("odd", widget.CompleteContext{}); got != nil || strings.Join(*said, "|") != "odd's complete: answered a string, not a list of strings" {
		t.Fatalf("not a list: %q, said %q", got, *said)
	}
	if c, _ := core.Commands().Lookup("w"); c.Complete != nil {
		t.Fatal("a command without complete got one")
	}
	err := e.Load("bad.lua", []byte(`editor.command{ name = "bad", complete = 3, run = function() end }`))
	if err == nil || !strings.Contains(err.Error(), "complete is a list of strings or a function, not a number") {
		t.Fatalf("complete = 3: %v", err)
	}
}
