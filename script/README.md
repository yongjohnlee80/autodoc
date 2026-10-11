# script

Editor commands written in Lua, for any program with a golib Editor
(`github.com/yongjohnlee80/golib/tui/widget` or `golib/gui/widget`).

A script registers verbs of the editor's command line: its `:` prompt (VIM Commands, with Tab
completion) and `EditorCore.RunCommand`. The script edits through the editor's core functions, and
each command's edits undo as one step. AutoDoc uses this package for its own commands (`table`,
`mermaid`) and for the user's commands in `~/.config/autodoc/lua/`. Another program imports the
same package.

```go
import "github.com/yongjohnlee80/autodoc/script"

eng := script.New(editor.Core(),
	script.WithGlobal("myapp"),      // scripts call myapp.command{…}; "editor" by default
	script.WithNotify(statusLine),   // where myapp.notify(…) goes
	script.WithTimeout(time.Second)) // a runaway script is stopped (2 s by default)
defer eng.Close()

// a host function scripts can call: myapp.workspace()
eng.SetFunction("workspace", func(L *lua.LState) int { L.Push(lua.LString(ws)); return 1 })

errs := eng.LoadDir(os.DirFS(configDir), "lua") // every lua/*.lua, in name order
```

Everything runs on the program's UI loop, as everything that touches the editor does.

## A script

```lua
myapp.command{
  name = "upper", aliases = {"up"}, args = "[line]", help = "upper-case a line",
  run = function(ed, args, line)
    local n = tonumber(args[1]) or ed:cursor()
    ed:replace(n, 1, n, #ed:line(n) + 1, ed:line(n):upper())
    myapp.notify("upper-cased line " .. n)
  end,
}
```

- `args` is the words after the verb, and `line` is the rest of the line as typed.
- A command with the name of one already registered replaces it: the editor's own, or an earlier
  script's.
- `name`, `args` and `help` are what Tab completion shows.

## The editor object

Positions are 1-based: line 1 is the first line, and column 1 is the first character (a grapheme
cluster). A range runs from its start up to, not including, its end.

| Method | Does |
|---|---|
| `ed:value()` / `ed:set_value(text)` | the whole text; replace it, as one edit |
| `ed:line_count()`, `ed:line(n)`, `ed:lines()` | read lines |
| `ed:cursor()` / `ed:set_cursor(line, col)` | where the cursor is; move it |
| `ed:insert(text)` / `ed:insert_at(line, col, text)` | insert; returns the line and column after it |
| `ed:replace(l1, c1, l2, c2, text)`, `ed:delete(l1, c1, l2, c2)` | edit a range |
| `ed:text(l1, c1, l2, c2)` | a range's text |
| `ed:selection()` | the selection's range, or `nil` |
| `ed:find(term, {ignore_case=, backward=, wrap=})` | the next match from the cursor, or `nil` |
| `ed:run(line)` | run a command line, as the prompt does |

## Safety

- Scripts get Lua's `base`, `table`, `string` and `math` libraries. They get no `os`, no `io`, and
  no `dofile`, `load`, `require` or modules.
- An error, whether the script's own or the deadline's, becomes the command's error. The editor
  shows it in its prompt; it never crashes the program.
- A script file that fails to load is one error from `LoadDir`, and the other files still load.

The editor functions underneath are documented in golib's `tui/widget/EDITOR_CORE.md`.
