# Lua reference

Every function a Lua command can call in AutoDoc: the `autodoc` table, the editor object `ed`, and
the daemon's verbs. [Lua in AutoDoc](README.md) says where scripts live and how they load; the
[tutorial](tutorial-vim-commands.md) uses all of this step by step.

The language is Lua 5.1 (gopher-lua), with the base, table, string and math libraries
([the sandbox](#the-sandbox)).

## Positions

- **1-based.** Line 1 is the first line; column 1 is the first character.
- **A column is a character**, a grapheme cluster: `é` is one column, even written as `e` and an
  accent. Lua's `#text` counts bytes, so it equals the column count only for ASCII.
- **A range runs from its start up to, not including, its end.** `ed:text(2, 1, 2, 6)` is line 2's
  first five characters. From column 1 of line n to column 1 of line n + 1 is line n with its line
  break.
- **Positions are clamped.** A column past a line's end is its end; a line past the last is the
  last; a range given end first is put in order. `#text + 1` is therefore a safe end-of-line column.
- **In Vim's Normal mode the cursor sits on a character.** `ed:set_cursor` past a line's last
  character puts it on that character, and the cursor after an edit at a line's end moves back onto
  it. The positions an edit *returns* are exact: chain edits through them, not through
  `ed:cursor()`.

## The `autodoc` table

| Function | Does | Returns |
|---|---|---|
| `autodoc.command{name, aliases, args, help, run, complete, id, title}` | Registers a `:` command, an entry of AutoDoc's command catalog ([what it takes](#what-a-command-takes)). Runs as the file loads. | nothing |
| `autodoc.run(id, …)` | Runs a command of the catalog by its id, AutoDoc's or yours, with the rest as its arguments ([below](#autodocrunid-)). | `true`, or `false` and why |
| `autodoc.notify(message)` | Shows a notification: a toast in the corner, kept in the history (SPC h). | nothing |
| `autodoc.call(verb, params, cb)` | Calls any daemon verb by its name, with `params` a list of its params in order (`nil` or `{}` for none). `cb` is optional. | nothing; the answer goes to `cb` |
| `autodoc.current_workspace()` | The workspace in use. | a string, or `nil` before one is entered |
| `autodoc.document()` | The page in the editor ([below](#autodocdocument)). | a table |
| `autodoc.map{mode, keys, command, title}` | Binds a key to a command line ([keys and menus](#keys-and-menus)). | nothing |
| `autodoc.menu{where, title, command}` | Adds a menu row that runs a command line ([keys and menus](#keys-and-menus)). | nothing |
| `autodoc.<domain>.<verb>(…, cb)` | Calls the daemon's `<domain>.<verb>`: its params in order, then `cb`. Made when AutoDoc connects ([the daemon's verbs](#the-daemons-verbs)). | nothing; the answer goes to `cb` |

The engine's own functions, beside `command` and `notify`:

<!-- autodoc:lua-globals:start -->
<!-- generated from the script package's own tables by go generate ./tui. Do not edit. -->

| Function | Returns | Notes |
|---|---|---|
| `autodoc.now()` | number | The time, in seconds since 1970-01-01 UTC, with a fraction: `os.time()`'s, to the millisecond. |
| `autodoc.date(format, t)` | string, or a table | The time t (seconds, `now()` by default) written by format, as `os.date` does: `%Y-%m-%d`, `%H:%M`, …; `%c` by default. A format starting with `!` is in UTC, else local time; `"*t"` gives a table: `year`, `month`, `day`, `hour`, `min`, `sec`, `wday`, `yday`, `isdst`. |
| `autodoc.commands()` | list of tables | Every command, in the order they were added: `{id, name, aliases, args, help, title, source}`. `name` is its `:` name, `""` for one only a key or its id runs; `source` says whose it is (`"lua"`, `"plugin"`, `"editor"`, …). A command that takes another's `:` name finds that one here, under its id and with no `name`, and runs it with `autodoc.run`. |
| `autodoc.on(event, fn)` | nothing | Calls `fn(ed, info)` each time event happens, from now until the scripts reload. The host says which events there are ([events](#events)). A handler runs as a callback does, and its error is a notification. |

<!-- autodoc:lua-globals:end -->

```lua
-- stamp.lua: :stamp inserts today's date, and :stamp! the time too
autodoc.command{
  name = "stamp",
  help = "insert the date (with !, the time too)",
  run = function(ed, args, line, cmd)
    ed:insert(autodoc.date(cmd.bang and "%Y-%m-%d %H:%M" or "%Y-%m-%d"))
  end,
}
```

### `autodoc.document()`

The page in the editor, as a table. It can be called anywhere, a file's top level too.

| Field | What it is |
|---|---|
| `path` | its path in its workspace; `nil` for the untitled draft |
| `workspace` | the workspace it is in; `nil` for the draft, or a file outside every workspace |
| `kind` | how AutoDoc reads it: `"markdown"`, `"text"`, `"yaml"`, `"derived"` or `"registered"` (source code) |
| `derived` | a derived document's format, `"PDF"` or `"DOCX"`: its text is read-only |
| `frontmatter` | a Markdown page's frontmatter, as Lua values, read from the editor's text now: `{title = "Plan", tags = {"a", "b"}}`; `nil` without one |
| `frontmatter_error` | why the frontmatter could not be read, when it is not YAML |

```lua
-- tagged.lua: :tagged says the page's tags, from its frontmatter
autodoc.command{
  name = "tagged",
  help = "say the page's tags",
  run = function(ed)
    local doc = autodoc.document()
    local tags = doc.frontmatter and doc.frontmatter.tags
    if type(tags) ~= "table" or #tags == 0 then
      error((doc.path or "the draft") .. " has no tags", 0)
    end
    autodoc.notify((doc.path or "the draft") .. ": " .. table.concat(tags, ", "))
  end,
}
```

### `autodoc.command{…}`

| Field | Type | Required | What it is |
|---|---|---|---|
| `name` | string | yes | What you type after `:`. Letters, digits, `-` and `_`, not all digits. |
| `run` | function | yes | `run(ed, args, line, cmd)`: what the command does. |
| `aliases` | list of strings | no | Other names for it, under the same rules as `name`. |
| `args` | string | no | Its arguments, as Tab shows them: `"<term>"`, `"[depth]"`. A hint only: nothing checks it. |
| `complete` | list of strings, or function | no | What Tab offers for an argument ([below](#completing-arguments)). |
| `help` | string | no | What it does, in one line, as Tab shows it. |
| `id` | string | no | Its id in the command catalog, which `autodoc.run` and F1 name it by: `lua.<something>`. `lua.<name>` when left out. |
| `title` | string | no | What F1's COMMANDS calls it. Its `help`, or its `name`, when left out. |

`run` gets:

| Argument | What it is |
|---|---|
| `ed` | the editor object, below |
| `args` | the words after the command, a list of strings: `:hi Ada Lovelace` gives `{"Ada", "Lovelace"}` |
| `line` | the rest of the line, trimmed at its ends only: `"Ada Lovelace"`; `""` with none |
| `cmd` | how it was called: `cmd.name`, the name as typed (an alias, say), and `cmd.bang`, `true` for `:hi!` |

```lua
-- shape.lua: :shape shows every field autodoc.command takes
autodoc.command{
  name = "shape",            -- letters, digits, - and _; not all digits
  aliases = {"sh"},          -- other names
  args = "[words]",          -- what Tab shows after the name
  help = "show what run gets", -- what Tab shows after that
  run = function(ed, args, line, cmd)
    autodoc.notify("shape: " .. #args .. " args, line '" .. line .. "' (as :" .. cmd.name
      .. (cmd.bang and "!" or "") .. ")")
  end,
}
```

**Errors.** `autodoc.command` raises, and the file fails to load, for:

- no `name`, or no `run` function: `bad argument #1 to command (command needs a name and a run
  function)`;
- a name or alias outside the rules: `tui: "has space" is not a ':' name: letters, digits, - and _,
  and not all digits`;
- an `id` outside `lua.`: `tui: the Lua command file.mine: a Lua id is lua.<name>`. `lua.` is yours;
  AutoDoc's ids and the plugins' are theirs.

### What a command takes

- **Its id replaces the command with that id.** A command named `table` is `lua.table`, so yours
  replaces AutoDoc's `:table` whole, and a later file's replaces an earlier file's.
- **Each of its names, it takes from whatever had it, and only that name.** Your `term` takes `:term`
  from AutoDoc's terminal command, which keeps `:terminal` and its id (`terminal.open`); `:find` of
  yours takes `:find`, and AutoDoc's find in the page still runs by its id (`find.term`). F1, `:`
  and Tab all show the name under yours.
- **The editor's own verbs are replaced whole,** as they always were: they are the editor's, not
  catalog entries. An alias `write` replaces `:w` and `:write` both. `:w`, `:q`, `:x`, `:wq` and
  `:replace` are the editor's.

**The bang.** `:hello!` runs `hello` with `cmd.bang` `true`; `:hello` with `false`. Run by a key or
by `autodoc.run`, `cmd.bang` is `false` and `cmd.name` is the command's name.

**One undo.** Everything `run` edits is one undo step, `ed:run`'s commands included.

### Completing arguments

After the name and a space, Tab completes the word being typed from `complete`:

- **A list** is the candidates: `complete = {"flowchart", "sequence"}`. AutoDoc's `:mermaid`
  offers its diagram types this way.
- **A function** is called as `complete(ed, word, args, cmd)` each time Tab starts on a word:
  `word` is the word typed so far (`""` after a space), `args` the words before it, `cmd` as
  `run`'s. It returns a list of strings. It runs as `run` does, with 2 seconds and `ed`, so it
  can offer what is on the page.
- **Tab keeps the candidates that start with the word**, so return them all. One is filled in
  with a space after it; several are listed, and Tab and Shift+Tab go through them. With none,
  Tab shows `args` and `help`.
- **A function's error** is a notification, `<name>'s complete: <error>`, and Tab shows `args`.

```lua
-- heading.lua: :heading <name> goes to a heading of the page; Tab offers them
local function headings(ed)
  local out = {}
  for n, text in ipairs(ed:lines()) do
    local title = text:match("^#+%s+(.-)%s*$")
    if title then out[#out + 1] = {name = (title:gsub("%s+", "-")), line = n} end
  end
  return out
end

autodoc.command{
  name = "heading",
  args = "<name>",
  help = "go to a heading of the page",
  complete = function(ed, word)
    local names = {}
    for _, h in ipairs(headings(ed)) do names[#names + 1] = h.name end
    return names
  end,
  run = function(ed, args)
    for _, h in ipairs(headings(ed)) do
      if h.name == args[1] then
        ed:set_cursor(h.line, 1)
        return
      end
    end
    error("heading: no " .. (args[1] or "name") .. " on the page", 0)
  end,
}
```

A heading's spaces become `-`, so each name is one word.

### `autodoc.run(id, …)`

Runs a command of AutoDoc's command catalog by its id: AutoDoc's own (`todos.open`, `file.save`,
`terminal.toggle`), a feature's, or a Lua command (`lua.<name>`). F1's COMMANDS lists every id,
with its `:` names and keys. The arguments after the id are the command's words, as if typed after
its name: strings, numbers or booleans.

- **It returns `true`**, or **`false` and why**, and never raises, so a script can try a command
  another may not have: `unknown: <id>` for an id no command has, the command's own error, or a
  refusal. A plugin's command (`plugin.<name>.<id>`) is never Lua's to run.
- **It runs now**, on AutoDoc's loop, as a key does. A Lua command it runs gets `ed`, the editor's,
  and its edits join the running command's one undo.

```lua
-- again.lua: :again <id> [args] runs a command of the catalog by its id, and says how it went
autodoc.command{
  name = "again",
  args = "<id> [args]",
  help = "run a command by its id",
  run = function(ed, args)
    local ok, err = autodoc.run(unpack(args))
    if ok then
      autodoc.notify("again: ran " .. args[1])
    else
      autodoc.notify("again: " .. err)
    end
  end,
}
```

## The editor object, `ed`

`ed` reaches the editor inside a command's `run` and inside a daemon callback. Elsewhere (at a
file's top level, as it loads) its methods raise `the editor is only reachable inside a command's
run`.

<!-- autodoc:lua-ed:start -->
<!-- generated from the script package's own tables by go generate ./tui. Do not edit. -->

| Method | Returns | Notes |
|---|---|---|
| `ed:value()` | string | The whole text, lines joined by `"\n"`. |
| `ed:set_value(text)` | nothing | Replaces the whole text, as one edit: `u` brings the old text back. |
| `ed:line_count()` | number | How many lines. A text ending in `"\n"` has an empty last line. |
| `ed:line(n)` | string | Line n's text, without its line break. Raises `no such line` for n outside 1 to `ed:line_count()`. |
| `ed:lines()` | list of strings | Every line, a copy. |
| `ed:cursor()` | line, col | Where the cursor is. |
| `ed:set_cursor(line, col)` | nothing | Moves the cursor, clamped, and scrolls to it. |
| `ed:insert(text)` | line, col | Inserts at the cursor, and returns the position just after the text. That position is exact even when Vim's Normal mode then rests the cursor on the last character (see Positions). `"\n"`, `"\r\n"` and `"\r"` start lines. In Vim's Normal mode the text goes before the character under the cursor. |
| `ed:insert_at(line, col, text)` | line, col | `ed:insert` at a position. |
| `ed:replace(l1, c1, l2, c2, text)` | line, col | Puts text in place of the range; returns the position just after it. |
| `ed:delete(l1, c1, l2, c2)` | nothing | Removes the range. The cursor goes to its start. |
| `ed:text(l1, c1, l2, c2)` | string | The range's text, line breaks as `"\n"`. |
| `ed:selection()` | l1, c1, l2, c2, or `nil` | The selection's range. A line-wise selection (`V`) runs from column 1 of its first line to the end of its last. `nil` outside Visual mode. |
| `ed:select(l1, c1, l2, c2)` | nothing | Selects the range, as Visual mode does, with the cursor on its last character: `ed:mode()` is then `"visual"`, whatever the keys. An empty range leaves the selection as it was. |
| `ed:unselect()` | nothing | Ends the selection; the cursor stays where it is. Nothing without one. |
| `ed:find(term, opts)` | l1, c1, l2, c2, or `nil` | The next match of term after the cursor. It does not move the cursor. `opts` is optional: `{ignore_case = true, whole_word = true, backward = true, wrap = true, at_cursor = true}`. |
| `ed:find_all(term, opts)` | list of `{l1, c1, l2, c2}` | Every match of term, in order: `{}` with none. `opts` as `ed:find`'s; only `ignore_case` and `whole_word` apply. |
| `ed:replace_all(term, text, opts)` | number | Puts text in place of every match of term, as one edit, and returns how many. `opts` as `ed:find_all`'s. |
| `ed:mode()` | string | The editor's mode: `"normal"`, `"insert"`, `"replace"`, `"visual"`, `"visual_line"` or `"visual_block"`. Without Vim's keys it is `"insert"`, or `"visual"` with a selection. |
| `ed:read_only()` | boolean | Whether edits are refused: a PDF's or DOCX's derived text, say. |
| `ed:revision()` | number | Names the text as it is now: every edit and every new document gives a new one, and an undo gives back the one the text had. A callback compares the revision its command read with `ed:revision()` to know whether the page changed while it waited. |
| `ed:modified()` | boolean | Whether the text has changed since it was opened or last saved, as `:x` asks before it writes. |
| `ed:run(line)` | nothing | Runs a command line as the prompt does, without the `:`: `"12"`, `"find TODO"`, `"table 2 3"`. Raises the command's error. |

<!-- autodoc:lua-ed:end -->

**Edits.** `ed:set_value`, `ed:insert`, `ed:insert_at`, `ed:replace`, `ed:delete` and
`ed:replace_all` edit.

- **An edit ends the selection.** Read `ed:selection()` before the first edit.
- **On a read-only page** (a PDF's or DOCX's derived text) every edit raises `widget: the editor
  is read-only`, and changes nothing.

**`ed:find`.**

- Forward, the match is the first that **starts after** the cursor, so finding again from a match
  moves on. Backward (`backward = true`), it is the last that starts before the cursor.
- With `at_cursor = true` a match starting under the cursor counts too: the word the cursor is on,
  found forward from where it starts.
- With `wrap = true` it goes round from the other end; without, it is `nil` at the end.
- A term matches within one line, and literally: no patterns. `ignore_case` folds case.
  `whole_word` matches only whole words, as Vim's `*` does: where the term starts with a letter,
  digit or `_`, the match must start a word, and where it ends with one, end a word.
- `ed:find` neither marks the matches nor sets `n` and `N`. For that, `ed:run("find " .. term)`.

**`ed:run`.** `ed:run("x")` raises `widget: not an editor command: x` for a command that does not
exist. It runs AutoDoc's commands and the editor's alike, and each does what it does from the
prompt. `"find …"` is AutoDoc's find in the page: its matches are marked, `n` and `N` step through
them, and its jump to the first match comes just after your command ends.

**Arguments.** The method's own errors count `ed` as argument 1, so a method's first argument is
`#2`: `ed:line("3")` raises `bad argument #2 to line (number expected, got string)`. A position is
a number; `args` are strings, so convert them with `tonumber`. A fraction is cut to a whole number.
A text argument may be a string or a number.

## Events

`autodoc.on(event, fn)` calls `fn(ed, info)` each time event happens:

| Event | When | `info` |
|---|---|---|
| `open` | a page is set up in the editor: a file opened, or the draft after a close | `{path, workspace}`; `path` is `nil` for the draft |
| `save` | a save wrote the editor's text | `{path, workspace}` |
| `mode` | the editor's mode changed | `{mode, previous}`, as `ed:mode()` names them |
| `change` | the text changed, once typing pauses (300 ms): a burst of keys is one change | `{revision}`, as `ed:revision()` |

- **A handler runs as a callback does:** on AutoDoc's loop, with 2 seconds, `ed` reachable and its
  edits one undo step. Its error is a notification, `a Lua handler failed: …`; the other handlers
  still run, and it stays.
- **A handler's own edits do not emit `change` again,** so a `change` handler may tidy the page.
- **`:lua-reload` drops every handler** with the scripts that set them; loading them again sets
  them again.

```lua
-- lastsaved.lua: :lastsaved <path> says when a page was last saved in this run
local saved = {}
autodoc.on("save", function(ed, info)
  saved[info.path] = autodoc.date("%H:%M:%S")
end)

autodoc.command{
  name = "lastsaved",
  args = "<path>",
  help = "say when a page was last saved",
  run = function(ed, args)
    local p = args[1] or (autodoc.document().path or "")
    autodoc.notify("lastsaved: " .. p .. " " .. (saved[p] or "not saved yet"))
  end,
}
```

## Keys and menus

`autodoc.map` binds a key, and `autodoc.menu` adds a menu row. Each runs its `command` line as `:`
does: a command's name and its arguments, `"table 2 3"`. Its error is a notification.

| `autodoc.map` field | What it is |
|---|---|
| `mode` | `"normal"` or `"visual"`: a key of the page in Vim's Normal or Visual mode; `"leader"`: a letter of **SPC u**, your Lua keys' card |
| `keys` | Normal and Visual: one character, `"Q"`, or a control chord, `"<C-k>"`. Leader: a letter or digit, not `q` (it closes the card) |
| `command` | the command line it runs |
| `title` | how SPC u's card names a leader key; the command line when left out |

- **A page key runs only with nothing pending:** after a count (`3`), an operator (`d`) or an armed
  key (`f`, `m`, `q`, `"`), the key is Vim's as always. It is not repeated by `.` or recorded in a
  macro.
- **A page key replaces Vim's** for that key. Pick one you don't use (`Q`, a `<C-…>` chord).
- **`autodoc.menu`'s `where`** is `"editor"`, the page's right-click menu, or `"plugins"`, the
  Plugins menu. `title` takes `&` before the mnemonic letter: `"&Word count"`.
- **`:lua-reload` drops every key and row** with the scripts; loading them again binds them again.

```lua
-- keys.lua: :stamp on Q, on SPC u s, and in the right-click menu
autodoc.command{
  name = "stamp",
  help = "insert the date",
  run = function(ed) ed:insert(autodoc.date("%Y-%m-%d")) end,
}
autodoc.map{ mode = "normal", keys = "Q", command = "stamp" }
autodoc.map{ mode = "leader", keys = "s", command = "stamp", title = "today's date" }
autodoc.menu{ where = "editor", title = "Insert the &date", command = "stamp" }
```

## Characters: `autodoc.text`

Lua's `#s` and `string.sub` count bytes; the editor's columns count characters (grapheme clusters).
`autodoc.text` counts as the editor does, so a column worked out from a string is right for
`ed:set_cursor` and the ranges, whatever the script the text is in.

<!-- autodoc:lua-text:start -->
<!-- generated from the script package's own tables by go generate ./tui. Do not edit. -->

| Function | Returns | Notes |
|---|---|---|
| `autodoc.text.columns(s)` | number | How many characters (grapheme clusters) s has: the columns it takes in the editor. `#s` counts bytes. |
| `autodoc.text.sub(s, i, j)` | string | Characters i to j of s, both included, as `string.sub` takes bytes: `j` is the last by default, and a negative position counts from the end (`-1` the last). |
| `autodoc.text.chars(s)` | list of strings | s's characters, in order: `é` written as `e` and an accent is one. |

<!-- autodoc:lua-text:end -->

```lua
-- tail.lua: :tail <n> selects the last n characters of the cursor's line
autodoc.command{
  name = "tail",
  args = "<n>",
  help = "select the end of the line",
  run = function(ed, args)
    local l = ed:cursor()
    local cols = autodoc.text.columns(ed:line(l))
    local n = math.min(tonumber(args[1]) or 1, cols)
    if n < 1 then error("tail: an empty line", 0) end
    ed:select(l, cols - n + 1, l, cols + 1)
  end,
}
```

## Callbacks

A daemon call never waits. `autodoc.call` and the `autodoc.<domain>.<verb>` functions return at
once; the daemon's answer goes to the callback later, as `cb(result, err)`:

- **`err` is `nil`**, and `result` is the answer as Lua values. A verb that answers nothing gives
  `result` `nil`.
- **`err` is a string**, and `result` is `nil`: the daemon's code and message (`"-32061 not
  found"`), or what went wrong before the daemon answered (no connection, say).
- **The callback runs as a command does:** on AutoDoc's loop, with 2 seconds, `ed` reachable, and
  its edits one undo step of their own. The page may have changed since the call: keep
  `ed:revision()` from before the call, and compare it in the callback. Equal, the text is the one
  the command read, positions included; else read again, or give up.
- **Its error is a notification**, `a Lua callback failed: <verb>'s callback: <error>`, since no
  prompt waits for it.
- **With no callback**, a failed call is a notification: `autodoc.call(<verb>): <err>`. A call that
  succeeds is silent.

**Params.**

- `autodoc.<domain>.<verb>(…)` takes the verb's params in order. Optional ones at the end may be
  left out, or passed as `nil`. The wrong count raises at the call, naming them:
  `autodoc.search.query takes 2 to 3 params (workspace, query, opts?), then a callback`.
- `autodoc.call(verb, params, cb)` takes the params as one list: `{"kb", "a.md"}`. A table that is
  not a list raises `params is a list, the verb's params in order`. A wrong count reaches `cb` as
  the daemon's `-32602` (invalid params).
- The protocol's own verbs (`sys.hello`, `sys.shutdown`) are refused before any call: they have no
  `autodoc.sys` function, and `autodoc.call` gives `cb` the refusal.

**Values** cross as msgpack carries them:

| Lua | The daemon |
|---|---|
| `nil`, a boolean, a string | nil, a boolean, a string |
| a whole number / any other number | an integer / a float |
| a table with keys 1 to n | a list |
| any other table | a map, its keys as strings |
| `{}` | an empty map, or an empty list where the verb's param is a list |
| a function, `ed` | cannot cross: the call raises `cannot cross between Lua and Go` |

Answers come back the same way: a list is a table with keys 1 to n, a map a table with string keys.

```lua
-- peek.lua: :peek <path> shows a document's first line, read through autodoc.call
autodoc.command{
  name = "peek",
  args = "<path>",
  help = "show the first line of a document of the workspace",
  run = function(ed, args)
    local workspace = autodoc.current_workspace()
    if not workspace or not args[1] then error("peek <path>, in a workspace", 0) end
    autodoc.call("doc.read", {workspace, args[1]}, function(res, err)
      if err then
        autodoc.notify("peek: " .. err)
        return
      end
      autodoc.notify("peek: " .. (res.content:match("[^\n]*")))
    end)
  end,
}
```

## The program's own functions

Beside the daemon's verbs, AutoDoc gives scripts its clipboard, a picker and its agents. They are
there from the start, connected or not. As the daemon's do, each `cb` runs later on the loop as
`cb(result, err)`, never inside the call: a refusal known at once is a callback on the next turn.

| Domain | Functions |
|---|---|
| `agents` | `list`, `send` |
| `clipboard` | `read`, `write` |
| `ui` | `pick` |

| Call | Answers |
|---|---|
| `autodoc.clipboard.write(text)` | `ok, err` at once: the system clipboard set (a terminal's, by OSC 52) |
| `autodoc.clipboard.read(cb)` | `cb(text, err)`: `""` for an empty clipboard or one holding no text; `err` `"unsupported"` where AutoDoc cannot read it (in a terminal, its own paste is the way in), `"timeout"`, `"closed"` |
| `autodoc.ui.pick({title, items}, cb)` | a picker over `items`, each a string or `{label, detail, value}` (`detail` is previewed, `value` given to `cb`, the item itself without one): `cb(value)`; `err` `"cancelled"` (Esc), or `"busy"` while another script's picker is open |
| `autodoc.agents.list()` | at once: `{{codename, name, running, closing, instance, orphan}, …}`, the saved profiles first; a `closing` one refuses a send |
| `autodoc.agents.send(agent, text, cb)` | `text` pasted into the agent's prompt, no Enter added after it: `agent` a codename, or `{codename, instance}` for that exact run. A stopped agent is started once the user agrees. `cb(true)` once it is typed; `err` `"cancelled"`, `"stale"` (that run is gone), `"unavailable"`, or why |

A paste is as the terminal's own: an agent CLI that asked for bracketed paste gets the text whole,
held at its prompt for Enter; elsewhere its newlines are Enter, so its earlier lines are submitted
as they arrive and only its last waits. AutoDoc's own `agents.lua` uses these: `:agent-buffer` sends the
page, and `:agent-forward` the selection, else the clipboard, to the agent running (with several,
or none, a picker asks which; one not running is started once you agree).

## The daemon's verbs

Each verb the daemon offers scripts is a function `autodoc.<domain>.<verb>`. They are made from
the daemon's own list (`sys.contracts`) each time AutoDoc connects, so they are this daemon's
verbs; until it has connected, `autodoc.<domain>` is `nil`. `autodoc --contracts` prints each
verb's params and result, and `autodoc --contracts 'todos.*' --format md` one domain's, as
tables. The codes an `err` carries are listed at the end of `--contracts`.

| Domain | Verbs |
|---|---|
| `doc` | `outline`, `read`, `read_raw`, `remove`, `rename`, `validate`, `write`, `write_raw` |
| `embedding` | `add`, `cancel_switch`, `log`, `model_context`, `models`, `providers`, `remove`, `update`, `usage`, `use` |
| `file` | `locate`, `read`, `read_raw`, `write`, `write_raw` |
| `graph` | `backlinks`, `links`, `neighborhood`, `resolve`, `unresolved` |
| `index` | `changes`, `documents`, `list`, `models`, `purge_model`, `reindex`, `status` |
| `preference` | `list`, `set` |
| `ranker` | `add`, `list`, `log`, `models`, `remove`, `update`, `usage`, `use`, `window` |
| `search` | `query` |
| `sys` | `capabilities`, `contracts`, `events` |
| `todos` | `ack`, `add`, `assign`, `changes`, `claim`, `get`, `list`, `migrate_refs`, `move_store`, `open`, `refresh`, `register`, `release`, `remove`, `remove_malformed`, `resolve`, `scaffold`, `set_dir`, `set_var`, `status`, `stores`, `update`, `vars` |
| `workspace` | `add`, `configure`, `embedding_policy`, `focus`, `list`, `remove`, `rename`, `section_size`, `set_patterns`, `set_provider`, `set_schema`, `set_text_extensions` |

The ones a command reaches for most:

| Call | Answers |
|---|---|
| `autodoc.search.query(workspace, query, opts?, cb)` | `{hits = {{path, breadcrumb, snippet, line_start, …}, …}, …}`; `opts` such as `{limit = 5}` |
| `autodoc.doc.read(workspace, path, cb)` | `{content, version}` |
| `autodoc.doc.outline(workspace, path, cb)` | `{version, headings = {{level, text, line, …}, …}}` |
| `autodoc.graph.backlinks(workspace, path, options?, cb)` | `{{path, raw, kind, …}, …}`: the documents that link to it |
| `autodoc.workspace.list(cb)` | `{{name, root, state, …}, …}` |
| `autodoc.todos.add(store, task, cb)` | the task filed, with its `id` (Help › Tutorial…, page 9) |

## Packages and capabilities

A package (`lua/<name>/` with a `package.toml`, [Lua in AutoDoc](README.md#packages-scripts-to-share))
runs **gated**: each kind of call below needs its capability, granted by you once. Without it, the
call answers `nil` and `needs <capability>: not granted`, and does nothing.

| Capability | Lets a package |
|---|---|
| `workspace.read` | call the daemon's reading verbs: `autodoc.search`, `autodoc.doc.read`, `autodoc.graph`, … |
| `workspace.write` | call its writing verbs: write, rename and remove documents and tasks |
| `settings` | change AutoDoc's settings: workspaces, models, rankers, preferences |
| `clipboard` | `autodoc.clipboard.read` and `.write` |
| `agents` | `autodoc.agents.list` and `.send` |
| `commands` | run commands: `autodoc.run`, `ed:run`, and the keys and menu rows it binds (`autodoc.map`, `autodoc.menu`: they register either way, and say they need it when pressed) |

- **Every daemon verb says which of the first three it needs** (its effect, in `autodoc
  --contracts`). A verb whose effect the daemon does not say (an older daemon) is refused to a
  package whatever it was granted.
- **A package declares what it needs** in `package.toml`'s `requires`, or as it loads,
  `autodoc.requires{"workspace.read"}`. AutoDoc asks after the scripts load.
- **Its own calls are its own:** a function a package defines runs with the package's grants
  wherever it runs from (a callback, an event, a key).
- **`require(name)`** loads the package's `name.lua` or `name/init.lua` once; `a.b` is `a/b.lua`.

## The sandbox

| There | Not there |
|---|---|
| `string`, `table`, `math` | `os`, `io`, `coroutine`, `debug`, `package` |
| `assert`, `error`, `pcall`, `xpcall`, `select`, `type`, `tostring`, `tonumber` | `require`, `module` |
| `pairs`, `ipairs`, `next`, `unpack`, `rawget`, `rawset`, `rawequal` | `load`, `loadstring`, `dofile`, `loadfile` |
| `setmetatable`, `getmetatable`, `collectgarbage` | `getfenv`, `setfenv`, `newproxy`, `_printregs` |
| `print`: a notification, its values joined by tabs, as `autodoc.notify` | `os.time`, `os.date`: `autodoc.now` and `autodoc.date` instead |
| `_G`, `_VERSION` | |

**The deadline.** A command's `run`, a callback and a file's loading each have 2 seconds. Past
them the script is stopped: `<command>: the script ran past 2s and was stopped`.

**Errors.** `error("text")` puts the file and line first (`wc.lua:7: text`); `error("text", 0)`
does not. A command's error shows in the prompt as `<command>: <error>`, and a file's that failed
to load in a notification as `<file>: <error>`, neither with Lua's stack trace.

- A table raised as the error is said by its `__tostring`, else its `message` field, else its
  fields: `error({code = 7})` is `an error {code = 7}`.
- `error(nil)` is `an error with no message`.
