package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// eventsLua records every event as a line of the global events; the first change's handler edits
// the page, which must not emit change again.
const eventsLua = `
events = {}
local function log(name)
  return function(ed, info)
    local parts = {name}
    for _, k in ipairs({"path", "workspace", "mode", "previous"}) do
      if info[k] then parts[#parts + 1] = k .. "=" .. info[k] end
    end
    if info.revision then parts[#parts + 1] = "rev" end
    events[#events + 1] = table.concat(parts, " ")
  end
end
for _, e in ipairs({"open", "save", "mode"}) do autodoc.on(e, log(e)) end
local changes = 0
autodoc.on("change", function(ed, info)
  changes = changes + 1
  log("change")(ed, info)
  if changes == 1 then ed:insert_at(1, 1, "#") end -- the handler's own edit
end)`

// TestLuaHearsThePagesEvents: open (a file opened), mode (Insert and back), change (a burst of
// typing is one change, and the handler's own edit is none) and save (:w) reach a script's
// handlers, in order, with their info.
func TestLuaHearsThePagesEvents(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := attached(t, d)
	r.luaLoad(t, eventsLua)
	events := func() string {
		return onLoop(r, func() string {
			if err := r.h.scripts.L.DoString(`events_text = table.concat(events, "; ")`); err != nil {
				return err.Error()
			}
			return r.h.scripts.L.GetGlobal("events_text").String()
		})
	}
	wait := func(what, want string) {
		t.Helper()
		r.s.WaitFor(t, what, func(string) bool { return strings.Contains(events(), want) })
	}

	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	wait("open", "open path=a.md workspace=kb")

	r.keys(t, key('i'))
	wait("insert mode", "mode mode=insert")
	// keys half the pause apart, the burst longer than the pause: one change, from the last key
	for _, k := range "abc" {
		r.keys(t, decltest.Type(string(k))...)
		time.Sleep(luaChangeWait / 2)
	}
	r.keys(t, esc())
	wait("normal mode", "mode mode=normal previous=insert")
	wait("one change", "change rev")
	time.Sleep(2 * luaChangeWait) // a second change, had the burst or the handler's edit made one, would be in
	if n := strings.Count(events(), "change rev"); n != 1 {
		t.Fatalf("%d changes for one burst of typing: %s", n, events())
	}
	if !strings.HasPrefix(r.editorText(), "#") {
		t.Fatalf("the change handler's edit is not in the page: %q", r.editorText())
	}

	r.command(t, "w")
	wait("save", "save path=a.md workspace=kb")
}

// TestARegistrationRefreshKeepsAPendingChange: readPage runs again on an open page when the
// daemon's registrations arrive; that is no new page: open is not emitted again, and a change
// pending from an edit just before it is still emitted.
func TestARegistrationRefreshKeepsAPendingChange(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := attached(t, d)
	r.luaLoad(t, eventsLua)
	events := func() string {
		return onLoop(r, func() string {
			if err := r.h.scripts.L.DoString(`events_text = table.concat(events, "; ")`); err != nil {
				return err.Error()
			}
			return r.h.scripts.L.GetGlobal("events_text").String()
		})
	}
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "open", func(string) bool { return strings.Contains(events(), "open path=a.md") })
	onLoop(r, func() bool {
		_, _ = r.h.core.Insert(widget.TextPos{}, "x") // an edit: change pending
		r.h.edited()
		r.h.readPage() // registrations arriving within the pause
		return true
	})
	r.s.WaitFor(t, "the pending change", func(string) bool { return strings.Contains(events(), "change rev") })
	if n := strings.Count(events(), "open path=a.md"); n != 1 {
		t.Fatalf("open emitted %d times: %s", n, events())
	}
}
