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
| `autodoc.command{name, aliases, args, help, run}` | Registers a `:` command, replacing any command with one of its names. Runs as the file loads. | nothing |
| `autodoc.notify(message)` | Shows a notification: a toast in the corner, kept in the history (SPC h). | nothing |
| `autodoc.call(verb, params, cb)` | Calls any daemon verb by its name, with `params` a list of its params in order (`nil` or `{}` for none). `cb` is optional. | nothing; the answer goes to `cb` |
| `autodoc.current_workspace()` | The workspace in use. | a string, or `nil` before one is entered |
| `autodoc.<domain>.<verb>(…, cb)` | Calls the daemon's `<domain>.<verb>`: its params in order, then `cb`. Made when AutoDoc connects ([the daemon's verbs](#the-daemons-verbs)). | nothing; the answer goes to `cb` |

### `autodoc.command{…}`

| Field | Type | Required | What it is |
|---|---|---|---|
| `name` | string | yes | What you type after `:`. Letters, digits, `-` and `_`, not all digits. |
| `run` | function | yes | `run(ed, args, line)`: what the command does. |
| `aliases` | list of strings | no | Other names for it, under the same rules as `name`. |
| `args` | string | no | Its arguments, as Tab shows them: `"<term>"`, `"[depth]"`. A hint only: nothing checks it. |
| `help` | string | no | What it does, in one line, as Tab shows it. |

`run` gets:

| Argument | What it is |
|---|---|
| `ed` | the editor object, below |
| `args` | the words after the command, a list of strings: `:hi Ada Lovelace` gives `{"Ada", "Lovelace"}` |
| `line` | the rest of the line, trimmed at its ends only: `"Ada Lovelace"`; `""` with none |

```lua
-- shape.lua: :shape shows every field autodoc.command takes
autodoc.command{
  name = "shape",            -- letters, digits, - and _; not all digits
  aliases = {"sh"},          -- other names
  args = "[words]",          -- what Tab shows after the name
  help = "show what run gets", -- what Tab shows after that
  run = function(ed, args, line)
    autodoc.notify("shape: " .. #args .. " args, line '" .. line .. "'")
  end,
}
```

**Errors.** `autodoc.command` raises, and the file fails to load, for:

- no `name`, or no `run` function: `bad argument #1 to command (command needs a name and a run
  function)`;
- a name or alias outside the rules: `widget: "has space" is not a valid editor command name`.

**What a command replaces.** A command replaces every command that has one of its names or aliases,
with all of that command's names. An alias `write` replaces `:w`, `:w`'s other name.

**The bang.** `:hello!` runs `hello`; the `!` is dropped, and `run` cannot tell it was typed.

**One undo.** Everything `run` edits is one undo step, `ed:run`'s commands included.

## The editor object, `ed`

`ed` reaches the editor inside a command's `run` and inside a daemon callback. Elsewhere (at a
file's top level, as it loads) its methods raise `the editor is only reachable inside a command's
run`.

| Method | Returns | Notes |
|---|---|---|
| `ed:value()` | string | The whole text, lines joined by `"\n"`. |
| `ed:set_value(text)` | nothing | Replaces the whole text, as one edit: `u` brings the old text back. |
| `ed:line_count()` | number | How many lines. A text ending in `"\n"` has an empty last line. |
| `ed:line(n)` | string | Line n's text, without its line break. Raises `no such line` for n outside 1 to `ed:line_count()`. |
| `ed:lines()` | list of strings | Every line, a copy. |
| `ed:cursor()` | line, col | Where the cursor is. |
| `ed:set_cursor(line, col)` | nothing | Moves the cursor, clamped, and scrolls to it. |
| `ed:insert(text)` | line, col | Inserts at the cursor, and returns the position just after the text, where the cursor goes. `"\n"`, `"\r\n"` and `"\r"` start lines. In Vim's Normal mode the text goes before the character under the cursor. |
| `ed:insert_at(line, col, text)` | line, col | `ed:insert` at a position. |
| `ed:replace(l1, c1, l2, c2, text)` | line, col | Puts text in place of the range; returns the position just after it. |
| `ed:delete(l1, c1, l2, c2)` | nothing | Removes the range. The cursor goes to its start. |
| `ed:text(l1, c1, l2, c2)` | string | The range's text, line breaks as `"\n"`. |
| `ed:selection()` | l1, c1, l2, c2, or `nil` | The selection's range. A line-wise selection (`V`) runs from column 1 of its first line to the end of its last. `nil` outside Visual mode. |
| `ed:find(term, opts)` | l1, c1, l2, c2, or `nil` | The next match of term after the cursor. It does not move the cursor. `opts` is optional: `{ignore_case = true, backward = true, wrap = true}`. |
| `ed:run(line)` | nothing | Runs a command line as the prompt does, without the `:`: `"12"`, `"find TODO"`, `"table 2 3"`. Raises the command's error. |

**Edits.** `ed:set_value`, `ed:insert`, `ed:insert_at`, `ed:replace` and `ed:delete` edit.

- **An edit ends the selection.** Read `ed:selection()` before the first edit.
- **On a read-only page** (a PDF's or DOCX's derived text) every edit raises `widget: the editor
  is read-only`, and changes nothing.

**`ed:find`.**

- Forward, the match is the first that **starts after** the cursor, so finding again from a match
  moves on. Backward (`backward = true`), it is the last that starts before the cursor.
- With `wrap = true` it goes round from the other end; without, it is `nil` at the end.
- A term matches within one line, and literally: no patterns. `ignore_case` folds case.
- `ed:find` neither marks the matches nor sets `n` and `N`. For that, `ed:run("find " .. term)`.

**`ed:run`.** `ed:run("x")` raises `widget: not an editor command: x` for a command that does not
exist. It runs AutoDoc's commands and the editor's alike, and each does what it does from the
prompt. `"find …"` is AutoDoc's find in the page: its matches are marked, `n` and `N` step through
them, and its jump to the first match comes just after your command ends.

**Arguments.** The method's own errors count `ed` as argument 1, so a method's first argument is
`#2`: `ed:line("3")` raises `bad argument #2 to line (number expected, got string)`. A position is
a number; `args` are strings, so convert them with `tonumber`. A fraction is cut to a whole number.
A text argument may be a string or a number.

## Callbacks

A daemon call never waits. `autodoc.call` and the `autodoc.<domain>.<verb>` functions return at
once; the daemon's answer goes to the callback later, as `cb(result, err)`:

- **`err` is `nil`**, and `result` is the answer as Lua values. A verb that answers nothing gives
  `result` `nil`.
- **`err` is a string**, and `result` is `nil`: the daemon's code and message (`"-32061 not
  found"`), or what went wrong before the daemon answered (no connection, say).
- **The callback runs as a command does:** on AutoDoc's loop, with 2 seconds, `ed` reachable, and
  its edits one undo step of their own. The page may have changed since the call: read positions
  again.
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
| `todos` | `ack`, `add`, `assign`, `changes`, `claim`, `get`, `list`, `move_store`, `open`, `refresh`, `register`, `release`, `remove`, `remove_malformed`, `resolve`, `scaffold`, `set_dir`, `set_var`, `status`, `stores`, `update`, `vars` |
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

## The sandbox

| There | Not there |
|---|---|
| `string`, `table`, `math` | `os`, `io`, `coroutine`, `debug`, `package` |
| `assert`, `error`, `pcall`, `xpcall`, `select`, `type`, `tostring`, `tonumber` | `require`, `module` |
| `pairs`, `ipairs`, `next`, `unpack`, `rawget`, `rawset`, `rawequal` | `load`, `loadstring`, `dofile`, `loadfile` |
| `setmetatable`, `getmetatable`, `getfenv`, `setfenv`, `collectgarbage`, `newproxy` | |
| `print`: to AutoDoc's standard output, not the page; use `autodoc.notify` | |
| `_G`, `_VERSION` | |

**The deadline.** A command's `run`, a callback and a file's loading each have 2 seconds. Past
them the script is stopped: `<command>: the script ran past 2s and was stopped`.

**Errors.** `error("text")` puts the file and line first (`wc.lua:7: text`); `error("text", 0)`
does not. A command's error shows in the prompt as `<command>: <error>`.
