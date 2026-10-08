# Writing an AutoDoc plugin

A plugin is a program. AutoDoc's TUI starts it and the two speak msgpack-RPC **notifications**, both
ways, over the plugin's stdin and stdout. Nothing waits for an answer. A plugin is one of three
things:

- **a dialog** (protocol 1): AutoDoc draws a dialog over the page, the plugin fills it with frames,
  and it is sent every key but Esc, which closes it;
- **a card** (protocol 2): a dialog beside the page that takes the keys only when focused;
- **a service** (protocol 2): no surface at all. It works on the note, or serves something of its own.

A protocol-2 plugin can also read the open note (the document feed) and have commands on the Plugins
menu and the SPC p card. ADR 0209 and ADR 1791268009 are the decisions behind it.

[autodoc-tetris](https://github.com/yongjohnlee80/autodoc-tetris) is a complete protocol-1 example.
[`examples/python/echo_plugin.py`](../examples/python/echo_plugin.py) speaks the protocol with no
AutoDoc code at all.

## The manifest

`plugin.toml`, at the top of the plugin's directory (and of its repository). A dialog, protocol 1:

```toml
name     = "tetris"                   # unique: lower-case letters, digits and -
title    = "AutoTetris"               # the menu entry and the dialog's title
kind     = "dialog"                   # "dialog", or "service" (protocol 2)
protocol = 1                          # the plugin protocol it speaks
command  = ["./bin/autodoc-tetris"]   # argv: a bare name on PATH, or a path from this directory

[dialog]
width      = 44                       # the columns inside the border (10 to 300; 40 when unset)
height     = "80%"                    # the rows inside the border (4 to 100; 20 when unset),
                                      # or a share of the screen's height ("10%" to "100%")
placements = ["right", "left"]        # where it is designed to sit, the first its default:
                                      # center, top, bottom, left, right, or a corner
                                      # (top-left …); the user picks among them (Manage › Place)
esc        = "hide"                   # "close" (the default), or "hide" for a plugin with its
                                      # own quit: Esc hides the dialog, the plugin runs on

[install]
build = ["go", "build", "-o", "bin/autodoc-tetris", "."]   # run here when it is added or updated
```

A card on the feed, protocol 2:

```toml
name     = "doc-stats"
title    = "Doc stats"
kind     = "dialog"
protocol = 2                 # it declares points of protocol 2
command  = ["autodoc-doc-stats"]

[dialog]
modal = false                # a card; default true (a dialog that holds the keys)

[feed]
document = true              # the open note, as it is edited

[[commands]]
id    = "toggle"
title = "Show / hide the stats"
key   = "s"                  # its letter on the SPC p card; optional
```

A service on the feed, protocol 2. `start` is a top-level key, so it comes before the first table:

```toml
name     = "html-preview"
title    = "HTML preview"
kind     = "service"
protocol = 2
start    = "use"             # "use" (default) or "launch"; services only
command  = ["autodoc-html-preview"]

[feed]
document = true

[[commands]]
id    = "open"
title = "Open in the browser"
key   = "h"
```

A manifest AutoDoc cannot run is listed in the Plugins menu disabled, with why. Among the reasons:

- an unknown kind, or a protocol outside 1 to 2;
- an unknown key, at any level (`dialog.colour`): AutoDoc decodes the manifest strictly;
- a name taken, no command;
- a placement, `esc` or height it does not know;
- a protocol-2 key under `protocol = 1` ("declares [feed]: needs protocol = 2");
- `start` on a dialog, `[dialog]` on a service, `esc` on a card;
- a service that starts on use with nothing to start it (no command, no feed);
- a command id that is not lower-case letters, digits and -, or appears twice; a command with no
  title; a key that is not one lower-case letter or digit, or is the plugin's twice.

Adding a plugin from a git URL asks first. The question shows what its build runs, what it starts and
the risk, then what it declares: "a card beside the page · reads the open note's text (feed) ·
commands: Show / hide the stats (SPC p s)".

## Versions

AutoDoc speaks plugin protocols **1 and 2**, and speaks to each plugin the one its manifest says. A
protocol-1 plugin runs as it always has.

- **A manifest that declares any protocol-2 key says `protocol = 2`.** Those keys are
  `kind = "service"`, `start`, `[dialog] modal`, `[feed]` and `[[commands]]`. An AutoDoc that speaks
  only 1 lists such a plugin disabled, "protocol 2; this AutoDoc speaks 1", so a card never runs there
  as a modal dialog without its feed.
- **The protocol rises** when a manifest can declare something an older AutoDoc must not run
  without, when a meaning changes, or when a plugin must handle something new. Everything a
  protocol adds is declared in the manifest, so a plugin is never sent what it did not ask for.
- The Go SDK speaks 1 and 2, and answers `plugin.open` with the protocol it was opened at.

## The run

- The command starts in the plugin's directory, in a process group of its own. Its environment adds:
  - `AUTODOC_PLUGIN_PROTOCOL` (the manifest's protocol);
  - `AUTODOC_PLUGIN_DIR`;
  - `AUTODOC_SOCKET`, the daemon's socket, for a plugin that uses AutoDoc's API as any client does
    (`AGENTS.md`).
- **stdout is the protocol's.** Write anything else to stderr, which goes to the plugin's log.
- **When it starts.**
  - A dialog or a card starts from its Plugins menu entry, or from one of its commands.
  - A service with `start = "use"` starts on its first command, or on its feed's first document.
    One with `start = "launch"` starts with the TUI.
- The close is bounded. It starts on Esc (a dialog's), on the plugin's `host.close`, on its menu's
  Close, or when the TUI quits:
  1. AutoDoc sends `plugin.close`;
  2. after 2 s it sends the group SIGTERM;
  3. after 1 s more, SIGKILL.
- A plugin that exits unasked, does not answer `plugin.open` within 5 s, or answers with a protocol
  its manifest does not say is closed, and a toast gives the last line of its stderr. A failed
  service is not started again until its next use.

## The protocol

Each notification carries one map.

| From | Notification | Since | Its map |
|---|---|---|---|
| AutoDoc | `plugin.open` | 1 | `{protocol, width, height, theme}`: the first, once. A service's has no `width` or `height` |
| AutoDoc | `plugin.key` | 1 | `{key, text, ctrl, alt, shift}` |
| AutoDoc | `plugin.resize` | 1 | `{width, height}`: the dialog laid out at another size |
| AutoDoc | `plugin.theme` | 1 | `{theme}`: the theme switched |
| AutoDoc | `plugin.hide`, `plugin.show` | 1 | `{}`: under `esc = "hide"`, Esc hid the dialog, and its menu entry showed it again |
| AutoDoc | `plugin.focus` | 2 | `{focused}`: a card took the keys, or gave them back |
| AutoDoc | `plugin.document` | 2 | `{path, workspace, text, cursor, selection, version, too_large}`: the open note, under `[feed] document = true` |
| AutoDoc | `plugin.command` | 2 | `{id}`: one of the manifest's `[[commands]]`, chosen |
| AutoDoc | `plugin.close` | 1 | `{}`: the last |
| plugin | `host.ready` | 1 | `{protocol}`: the answer to `plugin.open`, with the protocol it was opened at |
| plugin | `host.frame` | 1 | `{rows}`: the whole dialog, at any time. A service's is dropped |
| plugin | `host.title` | 1 | `{title}` |
| plugin | `host.close` | 1 | `{}`: asks to close |

- **Keys.** `key` is a printable key's text (`" "` for Space), or one of `Up`, `Down`, `Left`,
  `Right`, `Enter`, `Tab`, `Backspace`, `Delete`, `Insert`, `Home`, `End`, `PageUp`, `PageDown`, `F1`
  to `F12`. A chord with no text (Ctrl+b) sends its letter with `ctrl` set.
- **Frames.** `rows` is a list of rows, each a list of runs `{t, fg, bg, b, i, u}`: text, colours, and
  bold, italic and underline.
  - Colours are the themes' vocabulary: `"default"` (the dialog's own), an ANSI name (`"red"`,
    `"brightwhite"`, `"gray"`), or `"#rrggbb"`.
  - Past the dialog's size a frame is clipped.
  - AutoDoc paints only the latest frame, so send as often as you like.
- **The theme** is `{name, colors}`: the theme's colours by dotted name (`"document.cursor"`,
  `"app.window"`), for a plugin that wears the theme.

### Cards (`[dialog] modal = false`)

- A card opens beside the page and takes the keys only when it is focused. A click on it focuses it,
  and so do its commands. While focused it is sent every key but Esc, as a dialog is.
- **Esc gives the keys back to the editor and leaves the card open.** The plugin is told
  `plugin.focus {focused: false}`; it was told `{focused: true}` when it took them.
- It closes through its menu's Close, or its `toggle` command (below).

### The document feed (`[feed] document = true`)

- **What is sent.** The note in the editor as it stands, saved or not:
  - `path`, relative to the workspace's root, `""` for a draft not yet saved;
  - `workspace`, the workspace's name;
  - `text`, the whole text;
  - `cursor`, `{line, col}`;
  - `selection`, a list of `{start, end}`, empty when nothing is selected. `end` is just after the
    selection's last character, and a line-wise selection runs to the end of its last line.
- **Lines and columns count from 1.** A column counts characters as the editor's cursor does:
  grapheme clusters (what shows as one character), not bytes or code points.
- **When it is sent.**
  - When the plugin starts.
  - 300 ms after editing, moving the cursor or the selection, or opening a note, has paused: one
    document per pause, not one per key.
- **`version`** rises with every edit and with every note opened or closed, never with a cursor
  move. A plugin can drop an answer it computed for an older version.
- **Too large.** A document whose notification would be larger than the link's limit (1 MiB, the
  whole notification as encoded) is sent as `{path, workspace, version, too_large: true}`, with no
  text, cursor or selection.
- **Only the newest waits.** A document is state, not an event. If the plugin has not read the last
  one when a newer one is sent, the older one is dropped. Keys, commands and everything else are
  never dropped or reordered.

### Services (`kind = "service"`)

A service has no surface: `plugin.open` has no size, and its frames and titles are dropped. It runs
until the TUI quits, or until it sends `host.close`. It may serve something of its own (a preview on
localhost, say) and use AutoDoc's API through `AUTODOC_SOCKET`.

### Commands (`[[commands]]`)

- Each command is on the Plugins menu, under the plugin's own entry, beside Open (a dialog's or a
  card's) and Close.
- **The SPC p card** lists every plugin command with its letter (SPC p, then the letter). AutoDoc's
  own leader letters are never a plugin's: SPC p is a card of its own.
- **Letters.** A command's `key` binds only if no plugin earlier by name holds that letter. The other
  is listed unbound, saying whose the letter is. The preference
  `tui.plugin.<name>.key.<id>` overrides a manifest's letter, and `""` unbinds the command. Set it
  with the daemon's `preference.set`, then restart the TUI.
- **What a command does.**
  - It starts its plugin first when it is not running, and is sent once the plugin has opened.
  - A card's commands focus the card.
  - A card's command `toggle` is AutoDoc's own and is never sent: it opens the card, focused, and
    closes it when it is open.

## The SDK, in Go

```go
package main

import (
	"context"
	"os"

	"github.com/yongjohnlee80/autodoc/plugin"
)

type hello struct{ peer *plugin.Peer }

func (h *hello) Open(p *plugin.Peer, o plugin.Open) {
	h.peer = p
	f := plugin.NewFrame(o.Width, o.Height)
	f.Text(1, 1, "hello from a plugin", plugin.Style{FG: "cyan", Bold: true})
	_ = p.Frame(f)
}
func (h *hello) Key(k plugin.Key) {
	if k.Key == "q" {
		_ = h.peer.Close()
	}
}
func (h *hello) Resize(w, hh int)     {}
func (h *hello) Theme(t plugin.Theme) {}
func (h *hello) Close()               {}

func main() {
	if err := plugin.Serve(context.Background(), &hello{}); err != nil {
		os.Exit(1)
	}
}
```

- `Serve` answers the handshake, and calls the Handler's methods one at a time, in the order sent.
- It queues what it is sent itself, so a slow Handler never overflows the link.
- A plugin with a clock of its own (a game's gravity) sends from another goroutine. The `Peer` is safe
  for concurrent use.
- `Close` is called once, after `Open`, when the dialog closes or AutoDoc goes.
- A Handler that is also a `plugin.Hider` (`Hide()`, `Show()`) is told of hiding. A game pauses.
  A Handler that is not one keeps running while it is hidden.
- `plugin.open` comes after the dialog's first layout, with the size it was given; `plugin.resize`
  follows when the screen changes it.

**Protocol 2** adds optional interfaces, which `Serve` probes for as it does `Hider`. A notification
no interface takes is dropped.

| Interface | Method | Takes |
|---|---|---|
| `plugin.DocumentReader` | `Document(d plugin.Document)` | the feed. Only the newest waits for it: a newer document replaces one not yet handled |
| `plugin.Commander` | `Command(id string)` | a command |
| `plugin.Focuser` | `Focus(focused bool)` | a card's focus |

`plugin.Base` is a Handler that does nothing. A service embeds it and adds what it needs:

```go
type preview struct{ plugin.Base }

func (p *preview) Document(d plugin.Document) { /* render d.Text, serve it */ }
func (p *preview) Command(id string)          { /* "open": open the browser */ }
```

## What a plugin may not do

- **Change the note.** A plugin reads the feed. Writing to the document (inserting a translation,
  applying a fix) needs host-side actions that answer and work with undo, and is for a later
  protocol.
- **Highlight the editor's text.** The highlighter runs on every frame, synchronously, which a
  process round-trip cannot meet. New languages are AutoDoc's own work.
- **Draw outside its surface, or add to AutoDoc's menus** other than its Plugins entry and its
  declared commands.
- **Run inside AutoDoc.** A plugin is a process: there are no in-process (JavaScript, Lua, WASM)
  plugins, and no hub. A plugin's source is its git URL.
