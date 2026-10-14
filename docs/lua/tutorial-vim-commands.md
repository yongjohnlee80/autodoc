# Tutorial: your own Vim commands

This tutorial builds `:` commands in Lua, one step at a time. Each step is a whole script: paste it
into a file in `~/.config/autodoc/lua/`, run `:lua-reload`, and run it. By the end you have a word
count, a table of contents, a command that links to the workspace's documents, and your own
version of a built-in.

[Lua in AutoDoc](README.md) says where scripts live and how they load. The
[Lua reference](reference.md) lists every function used here.

**Before you start.**

- Make the folder: `mkdir -p ~/.config/autodoc/lua`.
- Open the command line with **:** in Vim's Normal mode, or **Ctrl+;** in any mode.
- AutoDoc reads the folder when it starts. **After each edit, quit (`:q`) and start it again.**
- If nothing seems to happen, **SPC h** shows the notifications, load errors among them.

## 1. A first command: `:hello`

```lua
-- hello.lua: :hello says hello
autodoc.command{
  name = "hello",
  run = function(ed)
    autodoc.notify("Hello from Lua")
  end,
}
```

Save it as `~/.config/autodoc/lua/hello.lua`, run `:lua-reload`, then type `:hello` and Enter. A
notification says "Hello from Lua".

- **`autodoc.command{…}`** registers a command. It runs once, when the file loads.
- **`name`** is what you type after `:`.
- **`run`** is what the command does. It runs each time you enter the command, and gets the editor
  as its first argument, `ed`. This one does not use it yet.
- **`autodoc.notify(text)`** shows a notification. Use it, not `print`: `print` writes to
  AutoDoc's standard output, not to the page.

## 2. Arguments, aliases, help and Tab

```lua
-- hello.lua, step 2: :hello [name], also :hi
autodoc.command{
  name = "hello",
  aliases = {"hi"},
  args = "[name]",
  help = "say hello to someone",
  run = function(ed, args, line)
    local who = line ~= "" and line or "world"
    autodoc.notify("Hello, " .. who .. " (" .. #args .. " words)")
  end,
}
```

Replace `hello.lua` with this, `:lua-reload`, and try `:hello`, `:hello Ada` and `:hi Ada Lovelace`.

- **`aliases`** are other names for the command: `:hi` runs it too.
- **`args`** and **`help`** are what Tab shows. Type `:he` and Tab: the line completes to
  `hello `, and the prompt shows `hello [name] — say hello to someone`. With several candidates,
  Tab lists them all, and Tab and Shift+Tab go through them.
- **`run` gets three arguments.** `ed` is the editor. `args` is the words after the command, a
  list: `:hi Ada Lovelace` gives `{"Ada", "Lovelace"}`. `line` is the rest of the line as typed,
  trimmed at its ends only: `"Ada Lovelace"`. Use `args` for words and `line` for free text.
- **Tab completes names, not arguments.** `args` is a hint for the reader; nothing checks what is
  typed against it. Check the arguments yourself in `run`.

## 3. Reading the buffer

```lua
-- where.lua: :where says where the cursor is, and what is selected
autodoc.command{
  name = "where",
  help = "the cursor's line and column, and the selection",
  run = function(ed)
    local line, col = ed:cursor()
    local msg = "line " .. line .. " of " .. ed:line_count() .. ", column " .. col
      .. ": " .. ed:line(line)
    local l1, c1, l2, c2 = ed:selection()
    if l1 then
      msg = msg .. "; selected " .. l1 .. "," .. c1 .. " to " .. l2 .. "," .. c2
        .. ": " .. ed:text(l1, c1, l2, c2)
    end
    autodoc.notify(msg)
  end,
}
```

Put the cursor somewhere and run `:where`. Then select a word with `v` and `e`, and run it again.

- **`ed:cursor()`** returns two numbers, the line and the column.
- **`ed:line(n)`** is line n's text, and **`ed:line_count()`** how many lines there are.
- **`ed:selection()`** returns four numbers, the selection's start and end, or `nil` when nothing
  is selected. Test the first one, as `if l1 then` does.
- **`ed:text(l1, c1, l2, c2)`** is the text between two positions, line breaks included.

**Positions are 1-based.** Line 1 is the first line, and column 1 the first character. A range runs
from its start up to, **not including**, its end. On the line `beta gamma`, the word `gamma` is
columns 6 to 11:

```text
b e t a   g a m m a
1 2 3 4 5 6 7 8 9 10   (11 is just past the end)
```

So `ed:text(2, 6, 2, 11)` on that line is `gamma`, and `ed:selection()` with `gamma` selected
returns `2, 6, 2, 11`.

**A column is a character, not a byte.** `é` is one column. Lua's `#text` counts bytes, so it is
the column count only for plain ASCII; `autodoc.text.columns(text)` counts as the editor does
([the reference](reference.md#characters-autodoctext)). A column past a line's end means its end,
so `#text + 1` is always a safe "end of the line": it is never short of it.

## 4. Editing, and one undo for each command

```lua
-- edit.lua: :quote, :callout and :squeeze
autodoc.command{
  name = "quote",
  help = "make the selected lines (or the cursor's) a Markdown quote",
  run = function(ed)
    local first, _, last = ed:selection()
    if not first then
      first = ed:cursor()
      last = first
    end
    for n = first, last do
      local text = ed:line(n)
      ed:replace(n, 1, n, #text + 1, "> " .. text)
    end
  end,
}

autodoc.command{
  name = "callout",
  args = "[type]",
  help = "start an Obsidian callout under the cursor's line",
  run = function(ed, args)
    local kind = (args[1] or "note"):upper()
    local n = ed:cursor()
    local line, col = ed:insert_at(n, #ed:line(n) + 1, "\n\n> [!" .. kind .. "]\n> ")
    ed:set_cursor(line, col)
  end,
}

autodoc.command{
  name = "squeeze",
  help = "collapse each run of blank lines into one",
  run = function(ed)
    local removed = 0
    -- from the bottom up, so the lines still to visit keep their numbers
    for n = ed:line_count() - 1, 1, -1 do
      if ed:line(n):match("^%s*$") and ed:line(n + 1):match("^%s*$") then
        ed:delete(n, 1, n + 1, 1)
        removed = removed + 1
      end
    end
    autodoc.notify("squeeze: " .. removed .. " blank lines removed")
  end,
}
```

Select three lines with `V` and `j j`, then run `:quote`. Press `u` once: every line comes back.

- **`ed:replace(l1, c1, l2, c2, text)`** puts text in place of a range. `:quote` replaces each
  whole line, from column 1 to `#text + 1`, with the line quoted.
- **`ed:insert_at(line, col, text)`** inserts at a position. `"\n"` in the text starts a new line.
  It returns the line and column just after the inserted text, which `:callout` hands to
  `ed:set_cursor`. In Vim's Normal mode the cursor rests on the line's last character there: `A`
  types after it.
- **`ed:insert(text)`** inserts at the cursor. In Vim's Normal mode the cursor sits *on* a
  character, and the text goes before it, as `i` would type it.
- **`ed:delete(l1, c1, l2, c2)`** removes a range. From column 1 of one line to column 1 of the
  next is the whole line with its line break.
- **Edit from the bottom up** when you delete or add lines in a loop: the lines above an edit keep
  their numbers.

**One command, one undo.** Whatever a command edits, `u` undoes it in one step: all the lines
`:quote` changed, all the runs `:squeeze` removed. You need do nothing for this; the command line
runs each command as one edit.

**An edit ends the selection.** After the first edit, `ed:selection()` is `nil`. Read the selection
first, as `:quote` does, then edit.

## 5. Finding, and running other commands

```lua
-- next.lua: :next <term> goes to the next match; :tasks finds the open tasks
autodoc.command{
  name = "next",
  args = "<term>",
  help = "go to the next match, wrapping at the end",
  run = function(ed, args, line)
    if line == "" then error("next <term>", 0) end
    local l1, c1 = ed:find(line, {ignore_case = line == line:lower(), wrap = true})
    if not l1 then error("no " .. line .. " on the page", 0) end
    ed:set_cursor(l1, c1)
  end,
}

autodoc.command{
  name = "tasks",
  help = "find the open tasks, - [ ]; n and N go on",
  run = function(ed)
    ed:run("find - [ ]")
  end,
}
```

Run `:next beta` a few times: the cursor goes from match to match, and back to the first after the
last.

- **`ed:find(term, opts)`** returns the next match after the cursor as four numbers, or `nil`. It
  does not move the cursor: `:next` moves it with `ed:set_cursor`.
- **The match starts after the cursor.** A match the cursor is on is not found again, so calling
  `:next` again moves on. `{at_cursor = true}` counts a match starting under the cursor too.
- **`opts`** is optional: `ignore_case`, `backward` (the match before the cursor) and `wrap` (go
  round from the other end). Like `:find`, `:next` ignores case when the term is all lower case.
- **A term matches within one line.** It cannot hold a line break.
- **`ed:run(line)`** runs a command line, as if you typed it: `ed:run("12")` goes to line 12, and
  `ed:run("find - [ ]")` is AutoDoc's own find, so its matches are marked and `n` and `N` step
  through them. If that command fails, `ed:run` raises its error, and yours fails with it.
- **`autodoc.run(id, …)`** runs any of AutoDoc's commands by its id, those with no `:` name too:
  `autodoc.run("todos.open")` opens the todos, as SPC x does. It returns `true`, or `false` and why,
  and never raises. F1's COMMANDS lists the ids ([the reference](reference.md#autodocrunid-)).
- **`error(message, 0)`** refuses: the prompt shows `next: no beta on the page` and reopens with
  your line, so you can fix it.

## 6. Two useful commands: `:wc` and `:toc`

`:wc` counts the words, lines and characters of the selection, or of the whole page when nothing
is selected.

```lua
-- wc.lua: :wc counts the selection's words, or the page's
local function count(n, unit)
  return n .. " " .. unit .. (n == 1 and "" or "s")
end

autodoc.command{
  name = "wc",
  help = "count the words, lines and characters of the selection or the page",
  run = function(ed)
    local text, what = ed:value(), "the page"
    local l1, c1, l2, c2 = ed:selection()
    if l1 then
      text, what = ed:text(l1, c1, l2, c2), "the selection"
    end
    local words = 0
    for _ in text:gmatch("%S+") do
      words = words + 1
    end
    local _, breaks = text:gsub("\n", "")
    local chars = autodoc.text.columns(text)
    autodoc.notify(what .. ": " .. count(words, "word") .. ", " .. count(breaks + 1, "line")
      .. ", " .. count(chars, "character"))
  end,
}
```

- **`ed:value()`** is the whole page as one string, lines joined by `"\n"`.
- **`gmatch` and `gsub`** come from Lua's string library: `gsub` returns how many it replaced as
  its second value, which makes it a counter.
- **`autodoc.text.columns`** counts the characters as the editor does. `#text` would count bytes,
  and `é` is two; written as `e` and an accent, it is three bytes and still one character.

`:toc` inserts a table of contents under the cursor's line: a list of the page's headings, `##`
and deeper, as Obsidian heading links.

```lua
-- toc.lua: :toc [depth] inserts a table of contents of the page's headings
local fence = string.rep("`", 3) -- a code fence: headings inside one are not headings

autodoc.command{
  name = "toc",
  args = "[depth]",
  help = "insert a table of contents, ## to depth (3 by default)",
  run = function(ed, args)
    local depth = tonumber(args[1]) or 3
    local items, in_code = {}, false
    for _, text in ipairs(ed:lines()) do
      if text:match("^%s*" .. fence) or text:match("^%s*~~~") then
        in_code = not in_code
      elseif not in_code then
        local hashes, title = text:match("^(#+)%s+(.-)%s*#*%s*$")
        if hashes and #hashes >= 2 and #hashes <= depth then
          items[#items + 1] = string.rep("  ", #hashes - 2) .. "- [[#" .. title .. "]]"
        end
      end
    end
    if #items == 0 then error("no ## headings to list", 0) end
    local n = ed:cursor()
    ed:insert_at(n, #ed:line(n) + 1, "\n\n" .. table.concat(items, "\n"))
  end,
}
```

Put the cursor on the page's title and run `:toc`, or `:toc 2` for the `##` headings alone.

- **`ed:lines()`** is every line, a list you can walk with `ipairs`.
- **It skips code blocks**, where a `#` line is code, not a heading.
- **`[[#Heading]]`** is Obsidian's link to a heading of the same note. For GitHub's style, build
  `[Heading](#heading)` instead.

## 7. Asking the daemon: `:link`

`:link <term>` searches the workspace and inserts a `[[wikilink]]` to the best match at the cursor.

```lua
-- link.lua: :link <term> inserts a [[wikilink]] to the workspace's best match
autodoc.command{
  name = "link",
  args = "<term>",
  help = "search the workspace and link to the top hit",
  run = function(ed, args, line)
    if line == "" then error("link <term>", 0) end
    local workspace = autodoc.current_workspace()
    if not workspace then error("enter a workspace first", 0) end
    local rev, l, c = ed:revision(), ed:cursor() -- the page as run found it
    autodoc.search.query(workspace, line, {limit = 1}, function(res, err)
      if err then
        autodoc.notify("link: the search failed: " .. err)
        return
      end
      local hit = res.hits[1]
      if not hit then
        autodoc.notify("link: nothing matches " .. line)
        return
      end
      local target = hit.path:gsub("%.md$", "")
      if ed:revision() == rev then
        ed:insert_at(l, c, "[[" .. target .. "]]") -- the page is as it was: where run was asked
      else
        ed:insert("[[" .. target .. "]]") -- it changed: where the cursor is now
      end
      autodoc.notify("link: [[" .. target .. "]]")
    end)
  end,
}
```

Put the cursor where the link should go and run `:link kestrel`.

- **`autodoc.search.query(workspace, query, opts, callback)`** is the daemon's `search.query` verb.
  Every verb the daemon offers scripts is a function like it, under `autodoc.<domain>`: its
  params in order, then the callback. `autodoc --contracts 'search.*' --format md` lists a
  domain's verbs, their params and what they answer.
- **The call returns at once.** The daemon's answer comes later, to the callback, as
  `function(result, err)`. By then `run` has finished, so the work happens in the callback.
- **Check `err` first.** It is `nil`, or a string: the daemon's code and message, such as
  `"-32060 no such workspace"`. Without the check, `res` is `nil` and `res.hits` fails.
- **The callback runs as a command does.** It has the same 2 seconds, it can use `ed`, and its
  edits are one undo step of their own.
- **The page may have changed meanwhile.** You may have typed, or opened another document.
  `ed:revision()` names the text: `run` keeps it, and the callback compares. The same revision is
  the same text, so the position `run` saved still holds; another means it may not, and `:link`
  inserts at the cursor instead.
- **`autodoc.current_workspace()`** is the workspace in use, or `nil` before you enter one. Most
  verbs take it first.
- **The result is Lua tables.** `res.hits` is a list; each hit has a `path`, relative to the
  workspace's root. A wikilink names a note by its path without `.md`.

## 8. Replacing a built-in, and naming

A command with the name of one of AutoDoc's Lua commands replaces it: both are `lua.table`. This
file replaces AutoDoc's own `:table` with one that also takes the header's names:
`:table Name Role Team`.

```lua
-- table.lua: AutoDoc's :table, also taking the header's names
local function row(cells)
  return "| " .. table.concat(cells, " | ") .. " |"
end

autodoc.command{
  name = "table",
  args = "<cols> <rows> | <header>…",
  help = "insert a Markdown table: a size, or the header's names",
  run = function(ed, args)
    local heads, rows = {}, 1
    if #args == 0 or tonumber(args[1]) then
      for c = 1, tonumber(args[1]) or 3 do
        heads[c] = "Column" .. c
      end
      rows = tonumber(args[2]) or 3
    else
      heads = args
    end
    local dashes, blanks = {}, {}
    for c = 1, #heads do
      dashes[c], blanks[c] = "-----", ""
    end
    local out = {row(heads), row(dashes)}
    for _ = 1, rows do
      out[#out + 1] = row(blanks)
    end
    local n = ed:cursor()
    if ed:line(n) == "" then
      ed:insert_at(n, 1, table.concat(out, "\n"))
    else
      ed:insert_at(n, #ed:line(n) + 1, "\n\n" .. table.concat(out, "\n"))
    end
  end,
}
```

- **Your files load after AutoDoc's**, so any file name replaces `:table`. Name it for what it is.
- **A name of yours that another command has moves to yours, and only that name.** A `term` of
  yours takes `:term`, and AutoDoc's `:terminal` still opens the terminal. The editor's own verbs
  are the exception, replaced whole: an alias of yours named `write` takes away `:w`, since `write`
  is one of `:w`'s names. Run `:` and Tab, or F1's COMMANDS, to see the names in use.
- **A name is letters, digits, `-` and `_`**, and is not all digits: `:12` goes to line 12. A name
  with a space or a dot fails to load.
- **Between your own files, the later name wins.** Files load in name order: `20-table.lua` after
  `10-table.lua`.

**Sharing a helper between files.** There is no `require`, but every file shares one Lua state: a
global one file sets, the files after it see. Give the helpers' file a name that sorts first, and
keep everything else `local`.

```lua
-- 00-helpers.lua: helpers the later files share; 00- makes it load first
my = my or {}

-- my.under puts text under the cursor's line, after a blank line
function my.under(ed, text)
  local n = ed:cursor()
  return ed:insert_at(n, #ed:line(n) + 1, "\n\n" .. text)
end
```

```lua
-- 50-rule.lua: :rule puts a horizontal rule under the line, with 00-helpers.lua's my.under
autodoc.command{
  name = "rule",
  help = "a horizontal rule under the cursor's line",
  run = function(ed)
    my.under(ed, "---")
  end,
}
```

- **Use one global table**, named for you (`my`), rather than many globals. A name AutoDoc's
  scripts or another file might use, such as `row`, is easily clobbered.
- **Leave `autodoc` alone.** Do not add your own fields to it: `autodoc.search` and the other
  domains are made when AutoDoc connects, and a field of yours in their place stops one being made.

## 9. Troubleshooting

**The command is not there.** `:` and Tab do not list it.

- Check **SPC h** for `a Lua command did not load`. It names the file, the line, and the error:
  a syntax error, a misspelt field, a name with a space.
- Check the folder: `~/.config/autodoc/lua/`, and the file ends in `.lua`.
- AutoDoc reads the folder when it starts and on **`:lua-reload`**: run it after an edit made
  outside AutoDoc (saving the file in AutoDoc reloads by itself).

**"the script ran past 2s and was stopped".** A command, a callback or a file's loading has 2
seconds. A loop that never ends is the usual cause, such as a `while` waiting for something that
cannot change while it runs. Work that takes long belongs in a plugin.

**Off by one.** Lines and columns are 1-based, and a range's end is just past its last character.
`ed:text(1, 1, 1, 6)` is the first line's first five characters. A column past the end of a line
is its end; a line past the last is the last.

**`bad argument #2 to line (no such line)`.** `ed:line(n)` refuses a line that is not there: 0, or
one past `ed:line_count()`. Check `n` first.

**`attempt to index a non-table object(nil) with key 'query'`.** `autodoc.search` and the other
`autodoc.<domain>` tables are made after AutoDoc connects to the daemon, from the verbs it offers.
At a file's top level, as it loads, they may not exist yet. **Call them inside `run`**, or inside a
callback, never at a file's top level.

**`the editor is only reachable inside a command's run`.** The same for `ed`: use it inside `run`
and its callbacks, not at a file's top level or in a function kept for later.

**The error names a file and a line.** `error("text")` adds them; `error("text", 0)` does not.
Use `0` for a message meant for the reader.

**Nothing happens, and no error.** The work may be in a callback whose call failed with no
callback to hear it, or the callback may have failed: **SPC h** has both, as `autodoc.call(…)` and
`a Lua callback failed` notifications.
