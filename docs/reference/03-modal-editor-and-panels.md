# Chapter 3: Modal Editor and Panels

Both `autodoc --ui` (the terminal TUI) and `autodoc --gui` (the native Gio desktop application) are built around the same high-speed, zero-lag authoring environment: a **120-column centered page editor** surrounded by workspace awareness panels. Rather than forcing technical writers to choose between a heavyweight IDE and a bare-bones note taker, AutoDoc brings authentic modal editing, structural Markdown folding, multi-buffer tabs, window splits, and live knowledge-graph panels into a unified workspace.

---

## 3.1 The 120-Column Centered Page Editor

Prose readability degrades rapidly when lines stretch across an entire ultrawide monitor. AutoDoc formats the central editor stage as a balanced **120-column page** with automatic responsive margins, soft-wrap awareness at word boundaries, and hanging indents for bulleted lists, numbered lists, and blockquotes.

Key structural features of the page editor include:

- **Frontmatter Header Folding**: YAML frontmatter blocks at the top of a document are syntax-highlighted and can be collapsed into a compact single-line badge showing `type`, `status`, and `tags` so you can focus immediately on the prose body.
- **Heading Breadcrumb Bar**: As you scroll through long specifications or ADRs, a pinned top breadcrumb bar displays your exact structural location (e.g., `GlobalKB > adrs/0042-sqlite-wal-lease.md > §1.2 Single-Writer Lease`).
- **Live Wikilink & Tag Completion**: Typing `[[` in Insert mode triggers an inline completion menu backed by the daemon's `graph` index, allowing fuzzy matching by document title, slug, or heading anchor (`[[0042-sqlite-wal-lease#1.2 Single-Writer Lease]]`).

![Illustrative Editor Frame Render Profile vs 16.6ms (60 FPS) Budget](editor-frame-latency.chart.json)

Because the buffer engine maintains an incremental piece-table and row-level syntax cache in pure Go rather than a browser DOM, interactive editing operations stay well inside the **16.6 ms** frame budget required for 60 FPS rendering (as illustrated in the latency profile above).

---

## 3.2 Vim Modal Editing vs. Modeless Mode

AutoDoc defaults to **Vim modal editing**, giving experienced terminal and Neovim users immediate muscle-memory compatibility. However, teams with mixed editor preferences can toggle **Modeless Mode** at any time via `:modeless` or inside the Preferences dialog (`SPC ,`, stored in the SQLite preferences table via `preference.set`).

![Editor Mode State Machine and Transitions](editor-mode-state-machine.mmd)

### Modal Editing States (`Normal`, `Insert`, `Visual`, `Command`)

1. **Normal Mode**: The default navigation and manipulation state. Supports standard motions (`h`, `j`, `k`, `l`, `w`, `b`, `e`, `0`, `$`, `gg`, `G`), structural Markdown section motions (`]]` next heading, `[[` previous heading), operators (`d`, `c`, `y`, `>`, `<`), text objects (`iw`, `aw`, `i"`, `i[`, `ip` inner paragraph, `is` inner section), and dot-repeat (`.`).
2. **Insert Mode**: Entered via `i`, `a`, `I`, `A`, `o`, or `O`. Supports smart Markdown list continuation (pressing `Enter` on a `- ` or `1. ` list item automatically inserts the next bullet or incremented number; pressing `Tab` / `Shift+Tab` indents or outdents the list item).
3. **Visual Mode**: Supports character-wise (`v`), line-wise (`V`), and rectangular block (`Ctrl+v`) selection. Selecting lines in Visual mode and pressing `>` or `<` shifts indentation cleanly, while pressing `[[` wraps the selection in a wikilink.
4. **Command Mode (`:`)**: Pressing `:` opens the command-line bar at the bottom of the screen for file operations (`:w`, `:q`, `:wq`, `:e <path>`), split creation (`:split`, `:vsplit`), search/replace (`:%s/old/new/g`), and direct RPC execution (`:call index.status`).

### Modeless Mode (`F2` or `:modeless`)

When Modeless Mode is active, typing inserts text directly without mode transitions, and standard desktop shortcuts apply:

- `Ctrl+S`: Save current buffer
- `Ctrl+F`: Find in buffer / `Ctrl+Shift+F`: Hybrid workspace search
- `Ctrl+T`: Toggle Rendered View (`--gui` only: the terminal's `--ui` has no Rendered View)
- `Ctrl+Z` / `Ctrl+Y`: Undo and Redo
- `Ctrl+B` / `Ctrl+I`: Toggle `**bold**` or `*italic*` around selection

---

## 3.3 Multi-Buffer Tabs, Window Splits, and Folding

Synthesizing architecture documents or conducting code reviews frequently requires cross-referencing multiple documents side by side.

### Multi-Buffer Tabs

Every opened document resides in an in-memory buffer tab shown across the top of the editor stage. Unsaved buffers display a modified indicator (`●`).

- Cycle forward and backward across tabs with `gt` and `gT` (or `Alt+1` through `Alt+9` to jump by tab index).
- Switch across open buffers with `SPC B` (a picker over them).
- Close the active buffer tab with `SPC C` or `:bd`.

### Horizontal and Vertical Window Splits

You can subdivide the central editor stage into multiple independent viewports viewing either different documents or different sections of the same document:

- **`Ctrl+w v` (or `:vsplit`)**: Opens a vertical side-by-side split. A common workflow is keeping an ADR open in the left split while editing its companion `.mmd` Mermaid diagram or `.chart.json` specification in the right split.
- **`Ctrl+w s` (or `:split`)**: Opens a horizontal stacked split.
- **`Ctrl+w h / j / k / l`**: Moves cursor focus cleanly across editor splits and into adjacent docked panels.
- **`Ctrl+w =`**: Rebalances all open splits to equal width and height.
- **`Ctrl+w q`**: Closes the focused split viewport while keeping the underlying buffer loaded.

### Structural Markdown Folding (`z` Operators)

AutoDoc computes fold ranges automatically from your document's YAML frontmatter, heading hierarchy (`#` through `######`), and fenced code blocks:

- **`za`**: Toggles the fold under the cursor open or closed.
- **`zo` / `zc`**: Explicitly opens or closes the current section fold.
- **`zM`**: Folds all sections in the document down to top-level headings—giving you an instant structural birds-eye view that you can reorder with line-wise Visual delete/paste (`V` + `d` + `p`).
- **`zR`**: Unfolds every section across the entire buffer.

---

## 3.4 Leader (`SPC`) Chords and Command Pickers

In Normal mode, pressing the **Spacebar (`SPC`)** opens SPC's card, listing every leader key by group. A capital is the key with Shift (`SPC O` is Shift+O). The keys, as `tui/keys.go` binds them:

- **Find:** `SPC /` (or `SPC SPC`) searches the workspace; `SPC o` opens a document (a filter over the workspace's files, with a preview); `SPC f` opens a file anywhere; `SPC r` lists recent files; `SPC j` goes to the link under the cursor; `SPC b` goes back; `SPC B` switches buffer.
- **File:** `SPC n` a new file; `SPC C` closes the buffer; `SPC O` the file's outline; `SPC d` views the diagram; `SPC H` previews HTML.
- **Panels:** `SPC e` the explorer; `SPC l` the links (Relations); `` SPC ` `` the terminal; `SPC x` the todos; `SPC M` maximizes a panel; `SPC h` the notifications.
- **Agents:** `SPC g` the AI agent; `SPC G` the agent profiles.
- **Manage:** `SPC w` switches workspace; `SPC W` manages workspaces; `SPC s` the search models; `SPC ,` the editor preferences; `SPC k` the editor mode (Vim, Text).
- **Window:** `SPC m` shows, focuses or hides the menu; `SPC .` the right-click menu; `SPC t` the status line; `SPC L` arranges a panel; and `SPC Tab`, below.
- **Help:** `SPC ?` help; `SPC A` about; `SPC Q` quit.
- **`SPC Tab` (Go to a widget)**: Opens the Go-to picker over the places the keyboard can go: the editor, every panel, and each terminal and agent tab, never the menu bar. Tab and Shift+Tab move its cursor; Enter goes there, opening a closed panel. `:go <name>` does the same from the command line (`:go explorer`, `:go terminal/lua`), and **F6 / Shift+F6** step the keyboard through the open widgets.
- **The dock (the menu bar's right end)**: one icon per widget. **✎** is the editor, and it comes first. Then comes each panel that is open, or running on with its drawer closed: a terminal with a shell, an agent, a plugin card whose plugin runs. A tab belongs to its panel's icon. A click on a shown panel hides it, not closed: it keeps everything it holds (a GlobeMap keeps its picture, view and filter), its icon is dimmed, and the keyboard goes back to the editor. A click on a hidden panel shows it again as it was, with the keyboard in it, as the Go-to picker does; a closed panel opens. **✎** hides every shown panel and remembers them; its next click shows the same ones again, unless a panel was opened, shown or closed in between. Opening a note from the GlobeMap, the Mindmap or the Links drawer hides it the same way, so the dock brings it back. A hidden panel leaves the dock only when it is closed for real. With the bar on auto-hide, it stays shown while the dock lists more than the editor, and the page moves down a row while it does.

![Complete Modal Editor, Leader Chord, and Panel Keybinding Reference](complete-keybinding-reference.csv)

---

## 3.5 Docking and Arranging Workspace Panels

Surrounding the central 120-column editor stage are three collapsible dock slots (**Left**, **Right**, and **Bottom**). Each panel can be toggled individually via its Leader chord, resized with `Ctrl+w <` / `Ctrl+w >`, or maximized temporarily with `Ctrl+w |`.

![Workspace Panel Docking Layout Around the 120-Column Editor](panel-docking-layout.mmd)

### Left Dock: `Files`, and the Outline

1. **`Files` (`SPC e`)**: Displays the canonical workspace directory tree, filtered by your workspace's `include` and `exclude` globs. Companion resource directories (`<note-stem>/`) are visually grouped beneath their parent `.md` document so diagrams and charts never clutter the top-level file list. Pressing `a` inside `Files` creates a new document from a `_templates/` archetype; `r` renames the file and automatically updates incoming `[[wikilinks]]` across the workspace.
2. **The Outline (`SPC O`)**: Go › Outline… lists the file's headings, unsaved ones too, and `Enter` jumps to the one chosen. It is a list over the page, not a docked panel.

### Right Dock: `Relations`, `Preview`, and `Plugin Cards`

1. **`Relations` (`SPC l`)**: Displays real-time graph context for the active document, divided into three collapsible sections:
   - **Incoming Backlinks**: Every document in the workspace that links to the current file, including the surrounding sentence snippet where the `[[wikilink]]` appears.
   - **Outgoing Links**: All resolved and unresolved (`broken`) links originating from the current document.
   - **Frontmatter & Todo Links**: Tasks in `.todo-list/` whose `adr:` or `review:` field references the active document.
2. **`Preview` (`SPC H`)**: An HTML file's page (File › Preview HTML). The note editor's own Rendered View is `Ctrl+T`, in `--gui`.
3. **`Plugin Cards`**: Renders custom contextual widgets registered by Lua plugins—such as schema validation diagnostics from `doc.validate`, Git blame summaries, or related semantic chunks.

### Bottom Dock: `Todos`, `Results`, and `TerminalTabs`

1. **`Todos` (`SPC x`)**: An interactive kanban/table view of the workspace's `.todo-list/{open,deferred,completed,archived}/` store. You can filter tasks by tag, priority, or assignee, press `Enter` to jump to a task's linked ADR/review document, or press `s` to transition status through the daemon's `todos.status` verb.
2. **`Results`**: Whenever you execute a workspace-wide search (`SPC /`) and press `Ctrl+q`, the ranked hits pin into the persistent bottom `Results` table so you can step through matches one by one using `]q` and `[q`.
3. **`TerminalTabs` (`SPC \``)**: A built-in multiplexed PTY terminal drawer rooted at the workspace directory. You can run `git status`, execute test suites, or invoke `autodoc --call` scripts in multiple terminal tabs without leaving the AutoDoc window.

---

## 3.6 Customizing Editor and Panel Layout (`config.toml`)

Every aspect of the modal editor, 120-column page geometry, and initial panel docking layout can be tailored in `~/.config/autodoc/config.toml` so both `autodoc --ui` and `autodoc --gui` open in your preferred configuration:

```toml
[editor]
mode = "vim"                  # "vim" (default) or "modeless"
page_columns = 120            # Target centered prose column width
tab_width = 2                 # Spaces per indentation level in Markdown & YAML
soft_wrap = true              # Wrap long prose lines at word boundaries cleanly
fold_frontmatter = false      # Collapse YAML frontmatter header on buffer open
auto_pair = true              # Auto-close brackets, quotes, and [[wikilinks]]
relative_numbers = true       # Show relative line numbers in Normal mode

[layout]
left_dock = ["files", "outline"]
right_dock = ["relations"]
bottom_dock = ["todos"]
left_width_cols = 32
right_width_cols = 38
bottom_height_rows = 12
show_breadcrumbs = true
show_tabbar = true
```

### Walkthrough: Reviewing an Architecture Decision

To see how the editor and the docked panels work together in practice, consider reviewing a proposed ADR (`adrs/0048-hybrid-rrf-fusion.md`) and its linked benchmark task:

1. **Open the Document (`SPC o`)**: Press `SPC o`, type `0048 rrf`, and press `Enter` to open `adrs/0048-hybrid-rrf-fusion.md` in the center page.
2. **Survey Its Structure (`SPC O`)**: Press `SPC O` (Go › Outline…) to list the file's headings, choose `## 3. Reciprocal Rank Fusion Formula`, and press `Enter` to jump there.
3. **Inspect What Links Here (`SPC l`)**: Press `SPC l` to open the **Relations** panel: the ADR's links and its backlinks, such as the synthesis notes and reviews that link to `[[0048-hybrid-rrf-fusion]]`, and a task whose `adr:` names it.
4. **Follow a Link (`SPC j`)**: Move the cursor onto the `[[wikilink]]` naming the benchmark report and press `SPC j` to open it. `SPC b` goes back to the ADR, where you left it.
5. **Check the Diagrams (`Ctrl+T`)**: In `autodoc --gui`, press `Ctrl+T` to switch the page into **Rendered View**, its diagrams, Vega-Lite charts, and CSV tables drawn in place. `Ctrl+T` again shows the source.
6. **Update the Linked Task (`SPC x`)**: Press `SPC x` to open the **Todos** panel, select the review task, and press `s` to choose its new status (the daemon's `todos.status` verb).

---

## 3.7 Synchronized External Editing and Conflict Prevention

Because AutoDoc is file-canonical, you might occasionally edit a document in `autodoc --ui` or `autodoc --gui` while an external tool (such as `git pull` or a background AI agent) modifies other files in the workspace:

- **Clean Buffers Auto-Reload**: If a buffer open in AutoDoc has no unsaved local modifications (`modified == false`) and `core/follow` detects an external change on disk, the buffer reloads the new content seamlessly while preserving your cursor line and scroll offset.
- **Modified Buffer Protection**: If your buffer has unsaved edits (`modified == true`) when an external process modifies the underlying file on disk, AutoDoc **never overwrites your in-memory work**. Instead, a non-intrusive status banner warns you of the external change and offers `:diffdisk` to inspect the delta or `:w!` to overwrite.

---

## 3.8 Command-Line (`:`) Ex Commands and Search-and-Replace

In addition to `SPC` Leader chords, AutoDoc's Command mode (`:`) provides a familiar suite of Ex commands for buffer management, split layout, and workspace-assisted editing:

```text
:w [path]              Write current buffer to disk (atomically via fsync + rename)
:wa                    Write all modified buffers across open tabs
:q / :q!               Close active window or quit (with force-discard option)
:wq / :x               Save current buffer and close window
:e <relative-path>     Open or reload a document relative to workspace root
:split [path]          Split viewport horizontally (:sp)
:vsplit [path]         Split viewport vertically (:vs)
:tabnew [path]         Open a new buffer tab
:bnext / :bprev        Switch to next or previous buffer in tab list
:modeless              Toggle between Vim modal mode and Modeless CUA mode
:foldopen / :foldclose Open or close the heading fold under the cursor
:export [output.html]  Compile current buffer and companion embeds to offline HTML
```

### Structural Search and Substitution (`:%s`)

AutoDoc supports regular-expression search (`/pattern`, `?pattern`, `n`, `N`, `*`, `#`) and buffer-wide substitution (`:%s/pattern/replacement/g`). When you rename a heading or refactor a frontmatter tag across a document, you can preview matches interactively with the `c` confirmation flag:

```text
:%s/\[\[old-adr-slug/\[\[0042-sqlite-wal-lease/gc
```

For workspace-wide link refactoring, renaming a file inside the `Files` panel (`r`) or invoking `doc.rename` over RPC automatically rewrites every incoming `[[wikilink]]` across all documents in the workspace in a single coordinated batch.

### Integrated `TerminalTabs` (`SPC \``) Workflows

The bottom `TerminalTabs` panel spawns a real pseudoterminal (PTY) shell with `$AUTODOC_WORKSPACE` and `$AUTODOC_CURRENT_DOC` exported into the shell environment. Because `$AUTODOC_CURRENT_DOC` always points to the active editor buffer, you can run one-liner outline, validation, or Git inspection commands directly against the file you are editing:

```bash
# Inside TerminalTabs: inspect the heading outline of the focused editor document
autodoc --call doc.outline '["'"$AUTODOC_WORKSPACE"'", "'"$AUTODOC_CURRENT_DOC"'"]'

# Inspect recent git commits touching the active document and its companion folder
git log -n 5 --oneline -- "$AUTODOC_CURRENT_DOC" "${AUTODOC_CURRENT_DOC%.md}/"
```


---

## 3.9 Moving Your Keys (`keymap.xml`)

Every key AutoDoc binds, and every key its widgets bind, is a command with an id. A `keymap.xml`
beside `config.toml` moves any of them. The full reference, with every id and its default keys, is
[`docs/keymap.md`](../keymap.md), or `autodoc --keymap-reference` on an installed binary.

### Walkthrough: moving search to F5 and recent files to a capital R

1. **Write the file.** Create `~/.config/autodoc/keymap.xml`:

   ```xml
   <mapping>
     <shortcut id="autodoc.window.openSearch"><key value="F5"/></shortcut>
     <shortcut id="autodoc.leader.openRecent"><key value="Shift+R"/></shortcut>
   </mapping>
   ```

2. **Check it.** `autodoc --check-keymap` reads the file as AutoDoc does and prints
   `…/keymap.xml: valid (0 warnings)`. A misspelled id is refused with its line.
3. **Watch it apply.** With AutoDoc open, the save applies on the next frame: F5 opens search,
   and Ctrl+G no longer does. Press `SPC`: the card lists `Shift+R  recent files`.
4. **See what you moved.** F1's help opens with **YOUR KEYS**, each moved command with its file and
   line.
5. **Break it on purpose.** Change `F5` to `F55` and save: a notice names `keymap.xml, line 2`, and
   F5 still opens search. A refused file changes nothing.

Unbind a key with `<key value=""/>`. Give a command several keys with several `<key>` elements. A
menu's underlined letter, a plugin's letters (their own preference) and your Lua keys can't be moved
here; nor can the key after a prefix, such as the second g of gg.

---

[← Previous: Chapter 02: Quickstart & Workspaces](./02-quickstart-and-workspaces.md) | [Index](./README.md) | [Next: Chapter 04: Rich Authoring, Embeds & Export →](./04-rich-authoring-embeds-and-export.md)

