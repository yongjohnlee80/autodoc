# Writing an AutoDoc plugin

A plugin is a program. AutoDoc's TUI starts it from the Plugins menu and draws a dialog over the
page. The plugin fills the dialog with frames, and it is sent every key but Esc, which closes the
dialog. The two speak plugin protocol 1: msgpack-RPC **notifications**, both ways, over the plugin's
stdin and stdout. Nothing waits for an answer. ADR 0209 is the decision behind it.

[autodoc-tetris](https://github.com/yongjohnlee80/autodoc-tetris) is a complete example.

## The manifest

`plugin.toml`, at the top of the plugin's directory (and of its repository):

```toml
name     = "tetris"                   # unique: lower-case letters, digits and -
title    = "Tetris"                   # the menu entry and the dialog's title
kind     = "dialog"                   # the only kind in protocol 1
protocol = 1                          # the plugin protocol it speaks
command  = ["./bin/autodoc-tetris"]   # argv: a bare name on PATH, or a path from this directory

[dialog]
width  = 44                           # the columns inside the border (10 to 300; 40 when unset)
height = 21                           # the rows inside the border (4 to 100; 20 when unset)

[install]
build = ["go", "build", "-o", "bin/autodoc-tetris", "."]   # run here when it is added or updated
```

A manifest AutoDoc cannot run is listed in the Plugins menu disabled, with why: an unknown kind or
protocol, a name taken, no command.

## The run

- The command starts in the plugin's directory, in a process group of its own. Its environment adds:
  - `AUTODOC_PLUGIN_PROTOCOL` (`1`);
  - `AUTODOC_PLUGIN_DIR`;
  - `AUTODOC_SOCKET`, the daemon's socket, for a plugin that uses AutoDoc's API as any client does
    (`AGENTS.md`).
- **stdout is the protocol's.** Write anything else to stderr, which goes to the plugin's log.
- The close is bounded. It starts on Esc, on the plugin's `host.close`, or when the TUI quits:
  1. AutoDoc sends `plugin.close`;
  2. after 2 s it sends the group SIGTERM;
  3. after 1 s more, SIGKILL.
- A plugin that exits unasked, does not answer `plugin.open` within 5 s, or speaks another protocol
  loses its dialog, and a toast gives the last line of its stderr.

## The protocol

Each notification carries one map.

| From | Notification | Its map |
|---|---|---|
| AutoDoc | `plugin.open` | `{protocol, width, height, theme}`: the first, once |
| AutoDoc | `plugin.key` | `{key, text, ctrl, alt, shift}` |
| AutoDoc | `plugin.resize` | `{width, height}`: the dialog laid out at another size |
| AutoDoc | `plugin.theme` | `{theme}`: the theme switched |
| AutoDoc | `plugin.close` | `{}`: the last |
| plugin | `host.ready` | `{protocol}`: the answer to `plugin.open` |
| plugin | `host.frame` | `{rows}`: the whole dialog, at any time |
| plugin | `host.title` | `{title}` |
| plugin | `host.close` | `{}`: asks to close |

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
