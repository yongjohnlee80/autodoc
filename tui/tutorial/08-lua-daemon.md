# Lua calls the daemon

A script can ask the daemon what the TUI asks it: search, read a file, list the workspaces. Every verb the daemon offers scripts is a function under `autodoc`, by its name:

```lua
autodoc.command{ name = "kestrels", run = function(ed)
  autodoc.search.query("kb", "kestrel", {limit = 5}, function(res, err)
    if err then autodoc.notify("search failed: " .. err) return end
    autodoc.notify(#res.hits .. " hits; the first is " .. res.hits[1].path)
  end)
end }
```

**The answer comes later.** The call returns at once, and the daemon's answer goes to the callback, the last argument: `function(result, err)`. The command has finished by then, so the work happens in the callback. It runs as a command does: two seconds, `ed` reachable, its edits one undo. The page may have changed in the meantime, so read positions again there.

**The params** are the verb's, in order; optional ones at the end may be left out. The wrong number is an error at the call, naming them. A table with keys 1, 2, 3… goes as a list, any other as a map, whole numbers as integers; the result comes back as Lua tables.

**`autodoc.call(verb, params, cb)`** reaches a verb by its name, the params as a list: `autodoc.call("doc.read", {"kb", "a.md"}, cb)`. The `autodoc.search`-style functions are made when AutoDoc connects, from the daemon it is on: use them inside `run`, not at a file's top.

**Which verbs?** `autodoc --contracts` prints every verb, its params and its result; `autodoc --contracts 'search.*' --format md` one domain, as tables. The protocol's own verbs (`sys.hello`, `sys.shutdown`) are refused to scripts.

**Errors.** A failed call reaches the callback as `err`, the daemon's code and message: `"-32061 not found"` (`--contracts` lists the codes). With no callback, the failure is a notification. So is a callback that fails or runs too long: **SPC h** shows them all.
