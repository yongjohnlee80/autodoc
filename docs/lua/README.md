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

In AutoDoc, **Help › Tutorial…** has a short version: its pages 9 to 11, the [tutorials](../tutorials/README.md) 09 to 11.

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

Save that as `~/.config/autodoc/lua/lines.lua`, run `:lua-reload` (or restart AutoDoc), and type
`:lines` and Enter.

**The command line.** **:** opens it in Vim's Normal and Visual modes, **Ctrl+;** in any mode
(the Text mode's way in), and **Search › Command line…** where a terminal cannot send Ctrl+;.
**Tab** completes a command's name and shows its arguments and help.

## Load order, and replacing a built-in

At start AutoDoc loads its own scripts first (`agents`, `mermaid`, `table`, `usage`), then yours, every `*.lua` in the
folder in file-name order.

- **A command named like one already loaded replaces it.** Yours can replace AutoDoc's `:table`, or
  the editor's own `:find` or `:w`; a later file replaces an earlier file's. A name AutoDoc's own
  command had moves to yours alone: its other names, and its id, stay its
  ([what a command takes](reference.md#what-a-command-takes)).
- **A file that fails to load** is one notification; the other files still load.
- **Every file shares one Lua state.** A global one file sets, a later file sees. Keep helpers
  `local` unless you mean to share them.

**Reloading.** AutoDoc reads the folder when it starts, and again on `:lua-reload`:

- **`:lua-reload` loads every script again**, AutoDoc's and yours, on a fresh Lua state. An edited
  file's commands are the new ones, a removed file's are gone, and a name your command had taken
  goes back to whatever had it before. Globals a script set are gone too.
- **Saving a script of the folder in AutoDoc reloads at once**: open
  `~/.config/autodoc/lua/lines.lua` (File › Open any file), edit it, and `:w`.
- **A daemon answer still on its way** to a callback of the old scripts is dropped when it comes.
- A script cannot reload from inside itself: `autodoc.run("scripts.reload")` answers `false` and
  why.

## Packages: scripts to share

A **package** is a folder of the `lua/` folder: `~/.config/autodoc/lua/<name>/`, holding `init.lua`
and a `package.toml`.

```toml
name = "wordcount"            # the folder's name
version = "1.0.0"
description = "word counts for the page"
requires = ["workspace.read"] # what it must be allowed to do
```

- **A package is held to what you allow.** It asks, once, for what its `requires` (and any
  `autodoc.requires{…}` in its code) names: reading the workspace, writing it, the clipboard, the
  agents, AutoDoc's settings, or running commands. Until you allow one, its calls of that kind
  answer `nil` and `needs <capability>: not granted`. **Not now** asks again at the next start.
- **Its globals are its own,** and so are its copies of `string`, `table` and `math`: it changes
  nothing another script sees.
- **`require("lib")` reads the package's own `lib.lua`** (or `lib/init.lua`), once; a name outside
  the folder is refused.
- **Your own `lua/*.lua` files are not packages:** they are yours, and are never asked about.
- **`:lua-install <git-url>`** clones a package whose repository has `package.toml` at its top. It
  asks before anything goes into `lua/`, naming the package, its version, its source and commit, and
  what it requires; **Install** puts it there and reloads the scripts. To remove one, delete its
  folder and run `:lua-reload`.

[The reference](reference.md#packages-and-capabilities) lists the capabilities a package can ask
for, and what each allows.

## Agents and the page

The agents AutoDoc starts (SPC a) can work on the page you are writing, its unsaved text included,
through `autodoc --editor` (the guide AutoDoc gives each agent lists the verbs). They are held as a
package is:

- **Each agent asks once** for what it does: reading the page (`editor.read`), editing it
  (`editor.write`) or running commands (`commands`). Until you allow it, its calls are refused and
  it is told why.
- **Each edit an agent makes is one undo step.** An edit of a page you changed since the agent read
  it is refused, not misplaced.
- **An agent can offer you a Lua command:** a card shows its source, and only **Add it** writes it
  into `lua/` and loads it. A file already there is never replaced.

## A Lua shell, and cards

**`:lua` opens a Lua shell**, a tab of the terminal panel (also **New Lua shell** on a tab's menu).
Type a line and Enter: an expression shows its value (`6*7` shows `42`), a statement runs. A line
that stops mid-statement (`for i = 1, 3 do`) asks for more with `..`; a pasted chunk runs whole.
Up and Down recall the lines typed, Ctrl+C drops the line, Ctrl+L clears, Ctrl+D on an empty line
closes the shell.

- **Its globals are its own:** `x = 1` in a shell is that shell's, never a script's; every global
  the scripts have is there to read. A shared table (`autodoc`, `string`) is the same table for all.
- **`ed` is the page,** and `print` writes into the shell, from a callback a chunk left as well.
- **It runs with your trust,** as your own `lua/*.lua` files do, under the same 2-second deadline.
- **`:lua-reload` gives it a fresh environment** on the new engine; it says so at its next line.
- **`:lua <chunk>`** runs one chunk without a shell and shows its values as a notification.

**A script can show a card** beside the page: `autodoc.ui.card{title, text, every, tick}` puts
Markdown in the card panel, and `tick(card)` refreshes it every `every` ms while it is open. In
the window (`--gui`) a ```` ```vega-lite ```` fence in the card is drawn as a chart
(`autodoc.ui.charts()` says whether it is); a terminal shows the Markdown. AutoDoc's own **`:usage`**
is one: the daemon's and the app's memory and CPU, and the workspace's index.

**`:go <name>` puts the keyboard on a widget** (`:go explorer`, `:go card`, `:go terminal/lua`),
opening a panel that is closed; `autodoc.widgets()` lists them, and `autodoc.run("widget.go", name)`
goes there from a script.

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
