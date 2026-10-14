package script

import (
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// print says what it prints where notify does, tostring's way and tab-joined; the sandbox has no
// getfenv, setfenv, newproxy or _printregs.
func TestPrintNotifiesAndTheSandboxIsTight(t *testing.T) {
	e, core, said := engine(t, "")
	got := probe(t, e, core, said, `
editor.command{ name = "p", run = function(ed)
  print("a", 1, nil, true, setmetatable({}, {__tostring = function() return "T" end}))
  print()
  print(tostring(getfenv) .. tostring(setfenv) .. tostring(newproxy) .. tostring(_printregs))
end }`, "p")
	if strings.Join(got, "|") != "a\t1\tnil\ttrue\tT||nilnilnilnil" {
		t.Fatalf("printed %q", got)
	}
}

// now and date read the engine's clock: date writes os.date's conversions, local or ("!") UTC, and
// "*t" a table; a conversion it does not know is the script's error.
func TestNowAndDate(t *testing.T) {
	at := time.Date(2026, 3, 7, 21, 5, 9, 250e6, time.UTC) // a Saturday
	zone := time.FixedZone("KST", 9*3600)
	core := widget.NewEditorCore()
	var said []string
	e := New(core, WithNotify(func(s string) { said = append(said, s) }), WithClock(func() time.Time { return at }, zone))
	t.Cleanup(e.Close)
	got := probe(t, e, core, &said, `
editor.command{ name = "d", run = function(ed)
  editor.notify(string.format("%.3f", editor.now()))
  editor.notify(editor.date("!%Y-%m-%d %H:%M:%S %a %A %b %B %y %e %I %p %j %w %u %F %T %R %D %Z %z %% %s"))
  editor.notify(editor.date("%Y-%m-%d %H:%M %Z %z"))
  editor.notify(editor.date("!%c"))
  editor.notify(editor.date("!%Y-%m-%d", 0))
  local tb = editor.date("*t")
  editor.notify(table.concat({tb.year, tb.month, tb.day, tb.hour, tb.min, tb.sec, tb.wday, tb.yday, tostring(tb.isdst)}, " "))
  editor.notify(editor.date())
end }`, "d")
	want := []string{
		"1772917509.250",
		"2026-03-07 21:05:09 Sat Saturday Mar March 26  7 09 PM 066 6 6 2026-03-07 21:05:09 21:05 03/07/26 UTC +0000 % 1772917509",
		"2026-03-08 06:05 KST +0900", // local time is the clock's zone
		"Sat Mar  7 21:05:09 2026",
		"1970-01-01",
		"2026 3 8 6 5 9 1 67 false", // Sunday the 8th in KST: wday 1
		"Sun Mar  8 06:05:09 2026",  // %c by default, local
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if err := e.Load("bad.lua", []byte(`editor.command{ name = "bad", run = function(ed, args) editor.date(args[1]) end }`)); err != nil {
		t.Fatal(err)
	}
	for line, want := range map[string]string{"bad %Q": "%Q is not a conversion date knows", "bad 100%": "a format ending in %"} {
		if err := core.RunCommand(line); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf(":%s: %v; want %q", line, err, want)
		}
	}
}

// commands lists the editor's registry, or a host's list in its place.
func TestCommandsListsTheRegistryOrTheHosts(t *testing.T) {
	e, core, said := engine(t, "")
	const src = `
editor.command{ name = "ls", aliases = {"l"}, args = "[x]", help = "list", run = function(ed)
  for _, c in ipairs(editor.commands()) do
    editor.notify(table.concat({c.id, c.name, table.concat(c.aliases, ","), c.args, c.help, c.title, c.source}, "/"))
  end
end }`
	got := probe(t, e, core, said, src, "ls")
	if !containsLine(got, "/ls/l/[x]/list//") || !containsLine(got, "/w/write//save//") {
		t.Fatalf("the registry: %q", got)
	}
	e.SetCommandList(func() []CommandInfo {
		return []CommandInfo{{ID: "x.y", Name: "y", Aliases: []string{"yy"}, Args: "<a>", Help: "h", Title: "T", Source: "feature"}, {ID: "x.z"}}
	})
	got = probe(t, e, core, said, "", "ls")
	if strings.Join(got, "|") != "x.y/y/yy/<a>/h/T/feature|x.z//////" {
		t.Fatalf("the host's list: %q", got)
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// ed:revision names the text: a callback that kept it can tell an edit made meanwhile, and an undo
// gives the old one back.
func TestEdRevisionTellsAChange(t *testing.T) {
	e, core, said := engine(t, "one")
	src := `
editor.command{ name = "keep", run = function(ed) kept = ed:revision() end }
editor.command{ name = "same", run = function(ed) editor.notify(tostring(ed:revision() == kept)) end }`
	probe(t, e, core, said, src, "keep")
	steps := []struct {
		do   func()
		want string
	}{
		{func() {}, "true"},
		{func() { _, _ = core.Insert(widget.TextPos{}, "x") }, "false"},
		{func() { undo(core) }, "true"},
		{func() { core.SetValue("one") }, "false"}, // the same text as a new document
	}
	for i, s := range steps {
		s.do()
		if got := probe(t, e, core, said, "", "same"); strings.Join(got, "|") != s.want {
			t.Fatalf("step %d: %q; want %s", i, got, s.want)
		}
	}
}
