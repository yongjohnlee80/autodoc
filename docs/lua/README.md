# Lua in AutoDoc

AutoDoc's command line runs commands written in Lua. You write a few lines, save them in a file,
and the next time AutoDoc starts you have a new `:` command. AutoDoc's own `:table` and `:mermaid`
are written this way.

A Lua command can:

- **be a command of your own**: `:wc`, `:toc`, `:link kestrel`, with its own arguments, aliases and
  help, offered by Tab like the built-in ones;
- **read and edit the page**: the cursor, the selection, any line or range of text, and every
  edit one undo step;
- **ask the daemon**: search the workspace, read a document, list the workspaces, file a task.
  The answer comes to a callback a moment later.

The rest of this folder:

- **[Tutorial: your own Vim commands](tutorial-vim-commands.md)** builds commands step by step,
  from `:hello` to a `:link` that searches the workspace.
- **[Lua reference](reference.md)** lists every function a script can call: the `autodoc` table,
  the editor object `ed`, the daemon's verbs, and the errors each one raises.

In AutoDoc, **Help › Tutorial…** (SPC T) has a short version: its pages 7 to 9.

## Where scripts live

Your scripts go in **`~/.config/autodoc/lua/`**, beside `config.toml`, one or more commands to a
`*.lua` file. The folder is `$XDG_CONFIG_HOME/autodoc/lua/` when that is set, and beside the file
`--config` names when you start AutoDoc with one. Create it if it is not there.

```lua
-- lines.lua: :lines says how long the page is
autodoc.command{
  name = "lines",
  help = "say how many lines the page has",
  run = function(ed)
    autodoc.notify("the page has " .. ed:line_count() .. " lines")
  end,
}
```

Save that as `~/.config/autodoc/lua/lines.lua`, restart AutoDoc, and type `:lines` and Enter.

**The command line.** **:** opens it in Vim's Normal and Visual modes, **Ctrl+;** in any mode
(the Text mode's way in), and **Search › Command line…** where a terminal cannot send Ctrl+;.
**Tab** completes a command's name and shows its arguments and help.

## Load order, and replacing a built-in

At start AutoDoc loads its own scripts first (`table`, `mermaid`), then yours, every `*.lua` in the
folder in file-name order.

- **A command named like one already loaded replaces it.** Yours can replace AutoDoc's `:table`, or
  the editor's own `:find` or `:w`; a later file replaces an earlier file's.
- **A file that fails to load** is one notification; the other files still load.
- **Every file shares one Lua state.** A global one file sets, a later file sees. Keep helpers
  `local` unless you mean to share them.

**Reloading is a restart.** AutoDoc reads the folder once, when it starts. After you edit a script,
quit (`:q`) and start AutoDoc again.

## The sandbox

Scripts get Lua 5.1's **base**, **table**, **string** and **math** libraries, and the `autodoc`
table. There is no `os`, no `io`, no `require`, and no `load`, `loadstring`, `dofile` or
`loadfile`: a script reads no files, runs no programs and loads no modules. For those, write a
plugin ([`docs/plugins.md`](../plugins.md)).

**The deadline.** Each command's script may run for **2 seconds**, and so may each callback. A
script still running then is stopped: "wc: the script ran past 2s and was stopped". The page keeps
whatever it had edited by then, and one `u` takes it back. A file has 2 seconds to load too; one
that runs past them fails to load ("context deadline exceeded").

## How errors show

| What failed | Where you see it |
|---|---|
| A file that does not load (a syntax error, a bad command name) | a notification at start: `a Lua command did not load: wc.lua: wc.lua:3: …` |
| A command's `run` (an `error(…)`, a bad argument, the deadline) | the command line, which reopens with your line so you can correct it |
| A callback (the daemon's answer came, and the function failed) | a notification: `a Lua callback failed: search.query's callback: …` |
| A daemon call with no callback, that failed | a notification: `autodoc.call(doc.read): -32061 not found` |
| `autodoc.notify(…)` | a notification: a toast in the corner |

Notifications fade, but **SPC h** (Go › Notifications…) keeps the last 500. It is the first place
to look when a command seems to do nothing.

**Raise errors with `error("message", 0)`.** The `0` leaves out the file and line Lua otherwise
puts first, so the prompt reads `wc: select some text first`, not `wc: wc.lua:7: select some text
first`.
