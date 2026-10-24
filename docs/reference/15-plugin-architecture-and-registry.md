# Chapter 15: Plugin Architecture & Registry

While sandboxed Lua scripts (Chapter 14) handle fast buffer edits and daemon queries inside AutoDoc's process, some workflows require persistent database connections, background HTTP servers, external toolchains, or rich custom panels. AutoDoc's **plugin architecture** (ADR 0209, ADR 1791268009, ADR 1791762474, ADR 1792078052, ADR 1792254331; see `docs/plugins.md`) runs every plugin as a separate OS process in its own process group, communicating over stdin and stdout via asynchronous **msgpack-RPC notifications**.

This chapter explains why out-of-process notification framing keeps the editor crash-proof, details the three plugin shapes (`dialog`, docked `card`, and headless `service`), traces the capability progression from **Protocol 1 through Protocol 8**, shows how the two UI kits (`plugin/card` and `plugin/decl`) draw a card for you, and shows how to build and install plugins from a Git URL (a plugin registry is coming; there is none yet).

---

## 15.1 Why Out-of-Process Msgpack-RPC Notifications?

In-process plugin hosts (shared libraries, unsandboxed interpreters, or synchronous RPC hooks) inevitably expose the editor to latency spikes, memory leaks, and thread deadlocks. AutoDoc avoids all three by enforcing three architectural rules:

1. **Process-Group Isolation**: Each plugin starts in its own directory inside a dedicated OS process group with `AUTODOC_PLUGIN_PROTOCOL`, `AUTODOC_PLUGIN_DIR`, and `AUTODOC_SOCKET` in its environment. Standard output (`stdout`) carries the msgpack-RPC wire stream; standard error (`stderr`) is captured to the plugin's log and surfaced in a corner toast if the process exits unexpectedly.
2. **Bidirectional Notifications Only (Zero Blocking Requests)**: Every wire message in both directions is a msgpack-RPC notification carrying a single map payload. Neither AutoDoc nor the plugin ever blocks waiting for a synchronous RPC return frame on the wire. Where a correlation is required—such as invoking a host command in Protocol 3 (`host.call` / `plugin.result`) or paging a result cursor in Protocol 4 (`plugin.table.fetch` / `host.rows`)—the correlation is carried explicitly by a `call` number or table `id`.
3. **Bounded 1 MiB Envelope and 3-Second Shutdown**: Every encoded notification is capped at **1 MiB**. When a plugin closes (via `Esc`, `host.close`, `Manage › Turn off`, or TUI exit), AutoDoc sends `plugin.close`, waits up to **2 seconds** for clean teardown, sends `SIGTERM` to the entire process group, and sends `SIGKILL` **1 second** later if any child remains.

---

## 15.2 The Three Plugin Shapes: Dialogs, Cards, and Services

Every plugin directory contains a strictly decoded `plugin.toml` manifest at its root that declares its shape:

- **Modal Dialog (`kind = "dialog"`, default `modal = true`)**: AutoDoc draws a bordered overlay over the page (`width`, `height`, `placements = ["right", "left", "center", ...]`). While open, the dialog receives every keystroke except `Esc`, which either terminates the plugin (`esc = "close"`, the default) or hides the overlay (`esc = "hide"`) while sending `plugin.hide`.
- **Non-Modal Docked Card (`kind = "dialog"`, `[dialog] modal = false`, Protocol 2+)**: A docked panel beside, above, or below the editor that behaves just like AutoDoc's built-in panels. It can be dragged and resized with `Alt/Option`-mouse or `Go › Arrange panel`. A card only receives keys when focused (`plugin.focus {focused: true}`). Pressing `Esc` closes the card's panel and returns focus to the editor while sending `plugin.hide`—the plugin process continues running, preserving its live database sessions or state until reopened (`plugin.show`) or explicitly turned off.
- **Background Service (`kind = "service"`, Protocol 2+)**: Has no visual surface (`host.frame` and `host.title` are ignored). Starts either on first command/document feed (`start = "use"`, default) or alongside the TUI (`start = "launch"`). Ideal for live browser preview servers or background linters.

---

## 15.3 Progression of Protocols 1 Through 10

AutoDoc's plugin contract evolves through ten cumulative protocol versions. A manifest must declare the minimum `protocol` version required for the features it uses, and AutoDoc refuses any manifest that mixes newer keys with an older protocol number—ensuring a plugin never runs half-broken on an older host.

![Evolution of AutoDoc Plugin Protocols 1 to 9](plugin-protocols.mmd)

### Protocol 1: Modal Dialogs and Styled Cell Frames

Protocol 1 establishes the handshake (`plugin.open {protocol, width, height, theme}` answered within 5 seconds by `host.ready {protocol}`), key forwarding (`plugin.key`), geometry/theme notifications (`plugin.resize`, `plugin.theme`), and `host.frame {rows}`. Each row in `host.frame` is a list of styled text runs `{t, fg, bg, b, i, u}` using theme semantic tokens, ANSI names, or `#rrggbb` hex colors. AutoDoc always paints the newest frame and drops intermediate frames if the plugin renders faster than the display refresh.

### Protocol 2: Docked Cards, Services, Document Feed, and Commands

Protocol 2 introduces `[dialog] modal = false` (docked cards), `kind = "service"`, the live document feed (`[feed] document = true`), and user commands (`[[commands]]`).

- **Debounced Document Feed (`plugin.document`)**: Sent once on startup and **300 ms** after typing, cursor movement, selection changes, or buffer switching pauses. Carries `{path, workspace, text, cursor, selection, version, too_large}`, and `root`, the workspace's absolute folder, for a workspace's note (`plugin.Document.File()` joins it with `path`). `version` increments only on content edits or buffer switches (never on cursor movement), allowing plugins to discard stale work. If the encoded notification exceeds 1 MiB, AutoDoc sends `{path, workspace, version, too_large: true}` without `text`.
- **The `SPC p` Plugin Command Card (`[[commands]]`)**: Each declared command (`id`, `title`, optional single-character `key`) appears under the Plugins menu and on the dedicated `SPC p` card.

### Protocol 3: Whitelisted Host Command Calls (`calls`)

Protocol 3 allows a plugin to invoke AutoDoc's own catalog commands—or user Lua commands (`lua.<name>`)—provided every target command ID is explicitly listed in top-level `calls = ["file.save", "buffer.new"]` in `plugin.toml` (wildcards like `todos.*` and cross-plugin `plugin.*` IDs are refused). The plugin sends `host.call {call, id, args}` with a unique in-flight integer `call` (up to 64 concurrent calls), and AutoDoc replies with `plugin.result {call, ok, error}`.

A flagship Protocol 3 capability is **`buffer.new`**: calling `peer.Call("buffer.new", "query.sql", "SELECT * FROM users LIMIT 100;\n")` opens a new unsaved tab up to 256 KiB with syntax highlighting inferred from the suggested filename, without touching the filesystem until the user presses `Ctrl+S`.

### Protocol 4: Hosted Paged `TableView` and UTF-8 Byte Offsets

Protocol 4 (`[table]`) offloads tabular result rendering from the plugin to AutoDoc's native bottom **Results panel** (`TableView`) and closes the race between document feed debouncing and command execution:

![Plugin Lifecycle and Paged Table Sequence](plugin-lifecycle-sequence.mmd)

1. **Exact Byte Offsets (`cursor_byte`, `selection_bytes`)**: Because `cursor` and `selection` count UTF-8 grapheme clusters, slicing `text` in a compiled language would otherwise require re-implementing Unicode grapheme segmentation. Protocol 4 adds `cursor_byte` and `selection_bytes` (`[start, end)` byte offsets) computed from the exact same cluster pass.
2. **Document Snapshot in `plugin.command`**: Because `plugin.document` waits for a 300 ms typing pause, invoking a command immediately after moving the cursor or typing could otherwise run against a stale buffer. Under Protocol 4 + `[feed] document = true`, `plugin.command {id, document}` embeds the exact buffer state as it stood at the instant the command was triggered.
3. **Demand-Paged `host.table` and `plugin.table.fetch`**: The plugin opens a result set with `host.table {id, title, fields, rows, more}`. As the user scrolls near the bottom of the loaded rows, AutoDoc sends `plugin.table.fetch {id, n}` (at most one fetch in flight at a time), and the plugin replies with `host.rows {id, rows, more}` or `host.table_end {id, error}` up to a 100,000-row safety cap.

![Hosted Table Page Transfer and Render Latency](plugin-frame-latency.chart.json)

### Protocol 5: Typed Cell `kinds` for NULLs, Numbers, Booleans, and JSON

In Protocol 4, every cell is a string, making SQL `NULL` indistinguishable from the four-letter string `'NULL'`. Protocol 5 adds an optional `kinds` array to `host.table` and `host.rows`—one string per row with exactly one byte per column:

- `t`: text (`"NULL"` is sorted and exported as a literal string)
- `z`: database `NULL` (rendered dimmed as `NULL`, sorted last in both `ASC` and `DESC`, exported as JSON `null`)
- `n`: exact numeric literal (sorted numerically by exact decimal value and exported as a raw JSON number without float64 precision loss)
- `b`: boolean (`false` sorted before `true`, exported as JSON `true`/`false`)
- `j`: structured JSON value (embedded directly into JSON exports)

### Protocol 6: The Pointer in a Card

Protocol 6 adds `plugin.pointer {kind, button, x, y, count}`: a left press (`"press"`, `"left"`) or a vertical wheel step (`"wheel"`, `"up"`/`"down"`) inside the plugin's frame, in frame coordinates from zero. `count` is bounded to 1–16. AutoDoc sends it only to a protocol-6 plugin, only for unmodified input inside the frame, and keeps the right press for the card's own context menu. A Go plugin takes it by implementing `plugin.PointerHandler`; a plugin can ignore it and keep its keyboard controls. AutoDB's card (Chapter 16) selects a connection or a tree row with it, and both UI kits below handle it for you.

### Protocol 7: A Session That Outlives the Process, and a Lua Package

Protocol 7 is for a plugin that signs in to something. AutoDoc keeps a small **session slot** for each plugin in its memory, never on disk, until it quits. The plugin replaces the slot with `host.session {values}` (the SDK's `Peer.SetSession`), and every later `plugin.open` hands it back as `Open.Session`. A plugin that is turned off and on, updated, or restarted after a crash reads its token back, checks it, and does not ask the user to sign in again. A slot is bounded (32 keys, 8 KiB a value, 64 KiB in all), and nothing logs its values.

`plugin.close` now carries a **reason**, which the plugin reads with `Peer.CloseReason()` inside `Close`. On `quit` (AutoDoc is going, and the slot with it) and `remove`, it logs out. On `off` and `update`, it keeps the token for the next start. A card's toggle only hides the card, so the plugin keeps running and stays signed in. Plugins › Turn off is what stops it.

A plugin may also ship a **Lua package** in its folder (`lua/package.toml`, `lua/init.lua`). AutoDoc loads it gated as `plugin:<name>`, and its prompt names the plugin. It can define `:` commands, keys and menu rows. `autodoc.plugin.run(id, args…)` sends one of its own plugin's commands, with arguments (`plugin.command {id, args}`, taken by a `plugin.CommandArgser`), and it reaches no other plugin. AutoDB's `:autodb query select 1` (Chapter 16) works this way. If you write a package of your own with the plugin's name, it replaces the plugin's.

### Protocol 8: Notifications

Protocol 8 lets a plugin speak where the user is, whatever panel is open. `host.notify {message}` (the SDK's `Peer.Notify`) shows a toast in AutoDoc's corner, kept in the notification history (SPC h). A long task gives the toast an id and `ongoing`: each notice with that id updates the same toast, and one without `ongoing` ends it, its last message kept in the history. The ids are the plugin's own, so two plugins never touch each other's, and a plugin that stops has its ongoing toasts ended for it. A message is at most 1 KiB; at most 16 toasts of one plugin run at once.

### Protocol 9: Opening Files

Protocol 9 lets a plugin open a kind of file AutoDoc does not read itself. Its manifest's `[open]` names the extensions (`extensions = [".ipynb"]`) and one of its commands (`command = "import"`). However the file is opened (the picker, File › Open file…, the explorer, a link, back and forward, a plugin's `file.open_path`), AutoDoc runs that command with the file's absolute path, starting the plugin first when it is not running; nothing opens as text. The plugin decides what opening means: autodoc-jupyter writes an `.ipynb` as a lab note beside it and opens that. A plugin never claims AutoDoc's own extensions (`.md`, `.txt`, `.yaml`, `.csv`, `.html`, …), and nothing on the wire changes.

### Protocol 10: Pixel Surfaces

Protocol 10 (`[surface]`) lets a card or dialog draw pixels: in AutoDoc's GUI its area is a native view drawing a golib gui stream, and in the terminal the plugin draws cells. Frames cross a side channel, a Unix socket inherited as fd 3, never stdout:
- The host allocates each generation's shared-memory buffers, sealed so they can't shrink, and maps them read-only.
- The plugin presents a buffer it holds credit for.
- The host answers each frame's outcome, then the buffer's credit, so every buffer has at most one frame in flight.

The pointer arrives in dp. The host keeps IME composition, drawing the preedit at the caret the plugin reports and sending committed text as keys. A malformed side-channel message closes the plugin. Linux ships first; macOS and plugin-exported DMA-BUFs follow (ADR 1792797354).

---

## 15.4 Anatomy of a `plugin.toml` Manifest

AutoDoc validates `plugin.toml` with strict unknown-key rejection before starting a plugin. Below is a complete Protocol-5 manifest showing every top-level table and capability declaration:

```toml
# plugin.toml — Protocol 5 docked card with document feed, host calls, and paged table
name        = "autodb"
title       = "AutoDB Workbench"
version     = "0.4.0"
description = "Live SQL sessions, schema browser, and paged query results"
protocol    = 5
kind        = "dialog"
command     = ["./autodoc-autodb"]
calls       = ["buffer.new", "file.save"]

[install]
build = ["go", "build", "-o", "autodoc-autodb", "./cmd/autodoc-autodb"]

[dialog]
modal      = false
width      = 44
height     = 24
placements = ["right", "left"]
esc        = "hide"

[feed]
document = true

[table]
title = "Query Results"

[[commands]]
id    = "connect"
title = "Connect / Unlock Session"
key   = "c"

[[commands]]
id    = "run"
title = "Run Statement at Cursor"
key   = "r"

[[commands]]
id    = "script"
title = "Run Full SQL Script"
key   = "s"
```

---

## 15.5 Writing a Plugin with the Go SDK

The `github.com/yongjohnlee80/autodoc/plugin` Go package implements Protocols 1–7 with concurrency-safe `Peer` methods and optional handler interfaces (`DocumentReader`, `Commander`, `CommandArgser`, `CommandDocer`, `Focuser`, `TableFetcher`, `Hider`, `PointerHandler`):

```go
package main

import (
	"context"
	"os"

	"github.com/yongjohnlee80/autodoc/plugin"
)

type demoPlugin struct {
	plugin.Base
	peer *plugin.Peer
	doc  plugin.Document
}

func (d *demoPlugin) Open(p *plugin.Peer, o plugin.Open) { d.peer = p }
func (d *demoPlugin) Document(doc plugin.Document)      { d.doc = doc }

func (d *demoPlugin) Command(id string) {
	if id == "inspect" && !d.doc.TooLarge {
		_ = d.peer.Table(plugin.TableOpen{
			ID:     "doc-meta-1",
			Title:  "Document Offsets",
			Fields: []string{"path", "version", "cursor_byte", "nullable_note"},
			Rows:   [][]string{{d.doc.Path, "1", "42", "NULL"}},
			Kinds:  []string{"tnnz"}, // text, number, number, NULL
			More:   false,
		})
	}
}

func main() {
	if err := plugin.Serve(context.Background(), &demoPlugin{}); err != nil {
		os.Exit(1)
	}
}
```

### Writing Plugins in Python or Any Other Language

Because the wire protocol is standard msgpack-RPC over `stdin` and `stdout`, a plugin can be written in Python, Rust, Zig, or Node.js without linking against Go. The repository includes a reference Python implementation (`examples/python/echo_plugin.py`) that reads `[2, method, [params]]` notification tuples from `sys.stdin.buffer` and writes `host.ready` and `host.frame` notifications to `sys.stdout.buffer` with `flush=True`.

---

## 15.6 Building the Card's UI: `plugin/card` and `plugin/decl`

A card or dialog can draw every cell itself into a `plugin.Frame`, as the example above could. AutoDB's first card did, in about 860 lines of layout, wrapping, focus, list and theme code before any database logic. The SDK's two **UI kits** (ADR 1792254331) do that work, so a card is its controls and its behaviour. Neither changes the wire: both end in the same `host.frame`.

![The two UI kits' render path](ui-kits-render-path.mmd)

- **`plugin/card`, the native kit.** The plugin keeps its controls (`card.Input`, including a masked passphrase field; `card.Buttons`; `card.List`; `card.Tree`) and returns a `card.Page` (header, scrolling body, button bar, keys line, status, problem) each time the card is drawn. The `card.Card` shares the height in a fixed order, so the header, the focused row, the first problem row, the buttons and the keys survive a short card. It draws the selection in the theme's highlight (the inactive one while the card lacks the keys), and handles Tab and Shift+Tab, j/k/g/G, PageUp/PageDown, l/h on trees, Enter, and the Protocol 6 pointer. Your Handler forwards its methods to the Card; `Card.Key` returns false for keys it did not use, so your own letters and `q` stay yours. In a field, every printable key is text, `q` included. `examples/card-go` is the reference: about a hundred lines of Go, with no drawing code.
- **`plugin/decl`, the QML adapter.** `decl.New(decl.Options{Program: …})` is a whole `plugin.Handler` that runs a golib QML layout (`tui/decl`, the language AutoDoc's own panels are written in) inside the plugin, on a backend whose every frame becomes a `host.frame`. Keys, resizes, focus and pointer presses arrive as terminal events. The layout binds its palette to `Theme.*` (module `autodoc.plugin 1.0`), so AutoDoc's theme switches apply without rebuilding the program, and typed text and scroll positions stay. State changes on the program's loop: in `Options.Setup` before the first frame, in a handler, or through `Card.Post`. `examples/card-qml` is the reference: a 19-line layout and about 60 lines of Go.

**Which to pick:** the native kit starts drawing at once and adds little to the binary (about 3 MB for a minimal stripped plugin); the QML adapter parses its layout first and brings golib's widgets with it (about 7.5 MB), in exchange for declarative layouts and widgets such as `TreeView` and `TableView`. Both measure text exactly as AutoDoc does: a CJK character takes two cells, and an accent written as a combining mark, a ZWJ emoji or a flag stays one character. `plugin.Frame` itself places characters this way, and AutoDoc's renderer draws them a grapheme cluster at a time.

Each example's test drives its plugin over the protocol, as AutoDoc does: open, type, press a button, switch the theme. See `docs/plugins.md` § "Drawing a card" for the full API.

---

## 15.7 Installing from a Git URL, Security Boundaries, and Debugging

Add a plugin with **Plugins › Add from a git URL…**; there is no registry yet (one is coming), and **Plugins › Manage plugins…** updates, places and removes the plugins you have. AutoDoc clones the repository and runs nothing; before it builds or starts the plugin (`[install] build = [...]`), it presents an explicit capability summary derived from `plugin.toml`—showing the build command, executable argv, surface type (`dialog`, `card`, or `service`), whether it reads the open note (`[feed]`), every `[[commands]]` shortcut on `SPC p`, every host command in `calls`, and whether it publishes to the `[table]` Results panel. You can also override any plugin command's `SPC p` key letter via the preference `tui.plugin.<name>.key.<id>`.

### What a Plugin May Not Do

By architectural contract (`docs/plugins.md`), a plugin cannot:
1. **Mutate the open buffer directly**: A plugin reads the document feed (`plugin.document`); it can only modify buffers by invoking a whitelisted command declared in `calls` (such as `buffer.new` or a user Lua command).
2. **Execute undeclared host commands or another plugin's commands**: Any `host.call` targeting an ID outside `calls` or starting with `plugin.` is rejected immediately.
3. **Block the editor's 60fps render loop**: Syntax highlighting and frame layout run synchronously in Go; a slow or wedged plugin process only delays its own card or table page and is terminated cleanly within 3 seconds on close.

![Plugin Notifications and Manifest Capabilities Reference](plugin-notifications-and-capabilities.csv)

---

[← Previous: Chapter 14: Lua Scripting & Custom Commands](./14-lua-scripting-and-custom-commands.md) | [Index](./README.md) | [Next: Chapter 16: AutoDB Live SQL Workbench →](./16-autodb-live-sql-workbench.md)

