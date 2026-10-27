# Chapter 4: Rich Authoring, Embeds, and Export

Technical architecture documents, performance postmortems, and engineering specifications require more than plain paragraphs. They rely on system topology diagrams, sequence flows, state machines, latency benchmarks, and structured comparison tables.

Yet embedding rich visuals in Markdown traditionally forces an unpleasant trade-off: either litter your prose with sprawling 150-line JSON/Mermaid blocks that obscure the text, or depend on external cloud charting services that break when offline. AutoDoc solves both problems through its **companion `<note-stem>/` resource directory convention** (`core/export/embed.go`), native GPU vector rendering in `autodoc --gui` (`golib/gui/mermaid` and `golib/gui/vegalite`), and single-file air-gapped HTML export (`autodoc --export`).

---

## 4.1 The Companion `<note-stem>/` Directory Convention

In AutoDoc, every Markdown document `<note-stem>.md` can have an optional companion resource directory alongside it named `<note-stem>/`. When you embed a diagram, chart, CSV table, or image using a relative path inside `<note-stem>.md`, AutoDoc's embed resolver (`core/export/embed.go`) automatically searches two locations in strict order:

1. **Beside the Note**: `<note-dir>/<target-path>`
2. **Inside the Companion Directory**: `<note-dir>/<note-stem>/<target-path>`

The file is looked for beside the note, then in its resources folder (the note's name without `.x.md` or `.md`: `plan.x.md`'s is `plan/`), and nowhere else.

For example, for the chapter file `docs/reference/04-rich-authoring-embeds-and-export.md`, its companion directory is `docs/reference/04-rich-authoring-embeds-and-export/`, holding:

- `04-rich-authoring-embeds-and-export/embed-resolution-pipeline.mmd`
- `04-rich-authoring-embeds-and-export/supported-vegalite-marks-gallery.chart.json`
- `04-rich-authoring-embeds-and-export/embed-limits-and-types.csv`

Inside `04-rich-authoring-embeds-and-export.md`, you reference those companion files by their bare filenames on standalone paragraph lines:

```markdown
![Embed Resolution and Security Pipeline](embed-resolution-pipeline.mmd)

![Workspace Growth by Artifact Type](supported-vegalite-marks-gallery.chart.json)

![Supported Embed Extensions and Resource Caps](embed-limits-and-types.csv)
```

This convention keeps your Markdown prose concise and readable in any terminal editor while storing each diagram, chart specification, and dataset as a clean, dedicated file with proper syntax highlighting and Git diff granularity.

![Embed Resolution and Security Pipeline](embed-resolution-pipeline.mmd)

### Path Containment and `os.Root` Security Guarantees

Because knowledge bases often ingest external repositories or untrusted PR branches, `core/export/embed.go` enforces strict path sandboxing before opening any embedded file:

- **Relative Subpaths Only**: Targets beginning with `/`, Windows volume prefixes, `http://` or `https://` URLs, or containing `..` parent traversal segments are rejected immediately by `embedPath`.
- **`os.Root` Kernel Containment**: All file reads execute through Go's `os.OpenInRoot` rooted at the note's directory (or the workspace root), preventing symlink escapes outside the workspace tree.

---

## 4.2 Block-Embed Syntax Rules

**Embeds are drawn.** In the preview, the GUI's pane and an HTML export, `![title](path)` of a file
beside the note is drawn in the page, as xmd draws it:

- a Mermaid diagram (`.mmd`) as the diagram;
- a `.csv` as a table, its first row the header;
- an image (`.png`, `.jpg`, `.gif`, `.svg`, `.webp`, …) as the image.

To distinguish rich block figures (diagrams, charts, and tables) from inline badge icons inside a sentence, AutoDoc enforces the **Standalone Paragraph Block-Embed Rule**:

- A `.mmd`, `.mermaid`, `.chart.json`, `.vl.json`, or `.csv` reference is promoted to a rendered `<figure>` block **only when `![Caption](path)` is the sole node inside its Markdown paragraph**—meaning it is preceded and followed by a blank line.
- The image alt text (`Caption`) becomes the rendered figure's `<figcaption>` title below the diagram, chart, or table.
- If a `.mmd`, `.chart.json`, or `.csv` link appears inline next to other text on the same line, it is left untouched rather than breaking paragraph flow.
- A file that is missing, too large or of another kind shows its title instead.

---

## 4.3 Mermaid Diagrams (`.mmd` and `.mermaid`)

You store each diagram in your note's companion directory (`<note-stem>/diagram.mmd`) and embed it on a standalone line with `![Diagram Caption](diagram.mmd)`, or write it in a ` ```mermaid ` fence.

### Two Renderers

A diagram is drawn by one of two renderers, and they don't support the same things:

- **An export** (`--export html`, a site) writes the diagram's source into the page with the vendored **mermaid.js**, which the reader's browser runs: full Mermaid. A diagram mermaid.js can't draw shows mermaid's own error in place.
- **The GUI** (`--gui`, the Rendered view, `Ctrl+T`) draws with **golib's native engine** (`golib/parse/mermaid` and `golib/gui/mermaid`), with no browser. (The HTML preview pane and `File › Preview Mermaid diagram…` use mermaid.js by default, and golib's engine when **File › Preview option** chooses native, or when no browser is found.) It draws a subset of Mermaid. `autodoc --diagram-reference` prints the kinds it draws, and the first word that starts each, read from golib's own tables, so the list is always this build's. Within a kind, the syntax drawn is in golib's package documentation: [`golib/parse/mermaid`](https://pkg.go.dev/github.com/yongjohnlee80/golib/parse/mermaid).

So a diagram can draw in an export and not in the GUI. In the GUI:

- a diagram outside golib's subset shows a notice in its place, saying why in golib's words: `diagram not drawn: <golib's error>`. Its source is in the Raw view, or in its `.mmd` file;
- a diagram with a syntax error is drawn as the error and its line, in place.

To draw the same in both, stay inside golib's subset. `autodoc --check-diagram <file>…` checks a diagram against the GUI's renderer with no UI: it prints `file:line: ok`, or golib's error, for each `.mmd` or `.mermaid`, and each `mermaid` fence of a `.md` (§4.4 has the details).

### Mermaid Itself, Vendored

**Mermaid diagrams are drawn by mermaid itself**: mermaid 12.1.0 is vendored in the binary
(`core/export/mermaid`, MIT), so every diagram kind it knows (flowcharts, sequence, state, class,
ER, gantt, mindmaps, …) is drawn as the Mermaid project draws it, offline. An HTML export with a
```` ```mermaid ```` block carries the script (about 5.5 MB) and draws its diagrams when opened; its
policy allows that script and the one that starts it, by digest, and no other, and a page without
a diagram has no script at all.

`File › Preview Mermaid diagram…` draws the block under the cursor (else the first) in the
terminal, as §4.11 describes; where it cannot, it shows the block's source and why. The GUI's
`Preview Mermaid diagram…` draws it the same way, by mermaid.js, and so does its HTML pane each
diagram of the page; with **File › Preview option** set to native, or in a window with no browser,
they are drawn natively, by golib's Mermaid, where golib draws their type. The terminal always uses
mermaid.js. A line embedding a `.mmd` file (`![flow](flow.mmd)`) is a diagram for it as a
fence is, read as the preview reads it.

---

## 4.4 Native Vega-Lite Charts (`.chart.json` and `.vl.json`)

For quantitative benchmarks, latency distributions, and time-series metrics, AutoDoc embeds a native Vega-Lite specification parser and vector chart renderer (`golib/gui/vegalite`).

### What It Draws: golib's Subset

AutoDoc draws a chart with golib's `gui/vegalite`, and what it draws is golib's subset. `autodoc --diagram-reference` prints it, read from the tables golib's reader checks a spec against: the marks, channels, field types, scale types and aggregates, layers, the limits, and the other keys taken in each place. It needs the GUI build, and exits 3 without it. The package documentation explains the subset: [`golib/gui/vegalite`](https://pkg.go.dev/github.com/yongjohnlee80/golib/gui/vegalite).

A chart outside the subset is not drawn, and AutoDoc says why in golib's words. An export (`--export html`, or a site) writes the chart's spec as code, with a caption above it:

```text
chart not drawn: unsupported: vegalite: a spec outside the subset drawn: a rule coloured by a field
```

A build without the GUI draws no chart in an export, so its charts are their specs, with no caption.

To check a chart before you show it, run `autodoc --check-diagram <file>…`. It lays out each `.chart.json` or `.vl.json`, each `.mmd` or `.mermaid`, and each `vega-lite` or `mermaid` fence of a `.md` with golib's renderer, as the GUI does, with no UI or daemon. It prints `file:line: ok (vega-lite)` or `file:line: not drawn: <golib's error>` for each, and exits:

| Code | Meaning |
|---|---|
| 0 | every diagram drawn |
| 1 | one or more not drawn, or a file it couldn't read or that holds no diagram kind |
| 2 | no file given |
| 3 | this build has no GUI, so it can't draw |

A bar chart, for example:

```json
{
  "$schema": "https://vega.github.io/schema/vega-lite/v5.json",
  "title": "Query Latency by Ranking Stage (ms)",
  "width": 600,
  "height": 280,
  "data": {
    "values": [
      {"stage": "FTS5 BM25", "ms": 1.2, "tier": "Lexical"},
      {"stage": "Vector Cosine", "ms": 2.8, "tier": "Semantic"},
      {"stage": "RRF Fusion", "ms": 0.4, "tier": "Hybrid"}
    ]
  },
  "mark": "bar",
  "encoding": {
    "x": {"field": "stage", "type": "nominal", "title": "Stage", "sort": null, "axis": {"labelAngle": 0}},
    "y": {"field": "ms", "type": "quantitative", "title": "Latency (ms)"},
    "color": {"field": "tier", "type": "nominal", "title": "Search Tier"}
  }
}
```

![Workspace Document Growth by Artifact Type](supported-vegalite-marks-gallery.chart.json)

---

## 4.5 CSV Data Tables (`.csv` and ```` ```csv ```` Fences)

Maintaining large Markdown pipe tables (`| Col A | Col B |`) by hand becomes tedious when columns grow wide or when data is exported from benchmark scripts. AutoDoc supports two cleaner ways to author tables:

1. **Companion `.csv` Files (Recommended)**: Place your dataset in `<note-stem>/table.csv` and embed it on a standalone line with `![Table Caption](table.csv)`.
2. **Inline ```` ```csv ```` Code Fences**: Paste RFC 4180 CSV rows directly inside a ```` ```csv ```` fenced block when a small inline dataset is needed.

In both cases, AutoDoc parses the first row as the table header, pads jagged rows safely, and renders a formatted, scrollable table widget. To protect UI responsiveness against accidental multi-gigabyte log dumps, `core/export/embed.go` caps embedded CSV files at **1 MiB**, **1,000 data rows**, and **64 columns**, displaying a clear `(showing first 1000 rows)` footer if a dataset exceeds the row cap.

![Supported Embed Extensions, Caps, and Surface Behavior](embed-limits-and-types.csv)

---

## 4.6 Raster and Vector Images (`.png`, `.svg`, `.webp`, `.jpg`, `.gif`, `.avif`)

Standard image embeds (`![Screenshot](ui-overview.png)`) resolve through the exact same companion directory lookup (`<note-dir>/` then `<note-dir>/<note-stem>/`).

In `--gui`'s Rendered View, a PNG, JPEG or GIF embedded alone on its line is drawn as the image, at most the page's width and in proportion; on the cursor's line it is its source. Other image types keep their line as written there, and show in the HTML preview and export.

To guarantee fast rendering and prevent malicious content injection, `resolveImage` in `core/export/embed.go` verifies every image's magic bytes with `http.DetectContentType` (and validates `<svg` XML structure for `.svg` assets) rather than trusting the file extension alone. Each individual image is bounded at **6 MiB**, with a cumulative per-document budget of **32 MiB** across all embedded images on a page.

---

## 4.7 Interactive Rendered View (`Ctrl+T`) in `--gui`

While authoring in `autodoc --gui`, pressing **`Ctrl+T`** toggles the editor between the Markdown source and **Rendered View**.

Rendered View in `--gui` is not an embedded webview: every heading, code block, callout box, `.mmd` diagram, `.chart.json` chart, and `.csv` table is drawn directly onto Gio's GPU vector canvas.

In the GUI's Rendered view the note stays editable, and a diagram or table embed alone in its
paragraph is drawn in its line's place, natively, as if the file were written there: a diagram as a
`mermaid` fence, a table as a Markdown table of 16 rows, its values as the file has them. While
typing on the line (Insert) it is its source again. Only the note's own editor draws them: the
search preview and a todo's details show other files' text, not the note's. An embed by reference
(`![flow][ref]`) stays as written there, as its file is named elsewhere in the note; the preview
draws it.

### The HTML Preview Pane (`File › Preview HTML`)

**`File › Preview HTML` opens a pane beside the editor in the GUI**, not an image or the browser.
It shows the note's export drawn natively, with proportional text and the export's own styles, so
it matches `autodoc --export html`. The text in it can be selected and copied (`Ctrl+C`).

- **Live:** it follows edits after a moment of quiet, unsaved ones included.
- **Follows the cursor:** the block under the editor's cursor comes to the pane's top.
- **Images** load from the note's folder only.
- **Links:** a click on a link opens what it names:
  - a note or a file, in the editor;
  - a web or mail address, in the browser.

In a terminal, `File › Preview HTML` is an image or the browser instead (§4.11).

### An HTML File Opened Raw

**An HTML file opened Raw** has its page as the editor's Rendered view in the GUI: `Ctrl+T` flips
between the HTML, which you edit, and the page, which is read-only. On the page:

- `j`/`k` or the arrows scroll a line, `Ctrl+D`/`Ctrl+U` half a page, `Space`/`PgDn`/`PgUp` a page,
  and `g`/`G` (or `Home`/`End`) go to the ends;
- a drag selects and `y` or `Ctrl+C` copies;
- links open as the preview pane's do.

The page is drawn with its own stylesheets, linked or inline, and its images, SVG included. CSS
layout, flex, grid and `<details>` work; JavaScript and form controls do not run.

- **In a workspace,** a file's stylesheets and images load from anywhere in that workspace, so a
  site whose pages share `../assets` shows them.
- **Outside every workspace,** they load from the file's own folder only.
  - A refused stylesheet or image is said once, in a notification.
  - Add the site's folder as a workspace to let its pages reach above themselves.

---

## 4.8 Self-Contained Offline HTML and Text Export (`autodoc --export`)

When you need to share an ADR, technical specification, or review report with colleagues who do not have AutoDoc installed, you can compile any Markdown document into a **100% self-contained, single-file HTML document** (or derived plain text) that works completely offline (`app/export.go`):

```bash
# Export a single Markdown note (with all companion .mmd, .chart.json, .csv, and images) to HTML
autodoc --export html --output /tmp/chapter-04.html docs/reference/04-rich-authoring-embeds-and-export.md

# Export with a specific built-in theme (light, dark, sepia, retro, or mono)
autodoc --export html --theme dark --output /tmp/chapter-04-dark.html docs/reference/04-rich-authoring-embeds-and-export.md

# Export a Markdown note as plain text
autodoc --export text --output /tmp/chapter-04.txt docs/reference/04-rich-authoring-embeds-and-export.md
```

Export a Markdown file without the daemon with
`autodoc --export=html --theme=light --output=/path/page.html /path/page.md` or use
`--export=text` for stripped text. Export requires an explicit destination and refuses to
overwrite its source. Remote images are represented by their alt text in HTML exports; scripts
and remote resources are not loaded. `--output`, `--theme` and `--base` are refused without
`--export`.

During HTML export (`core/export`), AutoDoc resolves every companion `.mmd` diagram, `.chart.json` chart, `.csv` table, and image, renders charts and tables into inline blocks, writes each Mermaid diagram for the vendored mermaid script to draw when the page is opened (§4.3), encodes raster images as `data:<mime>;base64,...` URIs, and bundles print-ready responsive CSS with theme support. The resulting `.html` file has **zero external network requests**, making it safe to archive in compliance artifacts, attach to pull requests, or view inside air-gapped environments.

### Relative Links in an HTML Export (`--base`)

**Relative links reach their files wherever the page is written.** An HTML export reads each
link to a path (`[x](notes/a.md#intro)`, `../x.md`, `<my notes/b c.md>`) from the source file's
own directory and writes it as the `file://` URL of the file it names, percent-encoded (a space,
a `#`, a byte past ASCII), its query and `#fragment` kept; an absolute path (`/etc/hosts`) becomes
its own `file://` URL. `--base <dir>` reads them from another directory instead: for a page made
from a copy of a file kept elsewhere, say. A URL with a scheme (`https:`, `mailto:`, …), one naming
another host (`//host/…`) and a bare `#fragment` are written as they are, and Obsidian
`[[wikilinks]]` are not resolved. A link is navigation, not a load, so the page's policy, which
loads nothing from outside it, stays as it is. The TUI's HTML preview reads them from the open
file's directory.

---

## 4.9 Authoring Checklist and Export Verification

When preparing a technical specification, architecture blueprint, or chapter with rich companion assets, you can verify that a document and all of its companion embeds render cleanly from the command line or a shell loop before committing to Git.

### Exporting Single Pages or a Directory Loop

Because `autodoc --export html --output <dest> <source.md>` exits with a non-zero status if the source file cannot be read or written, you can export a single chapter or loop over a directory of chapters in a standard shell script:

```bash
# Export one chapter to verify its HTML output
autodoc --export html --output /tmp/chapter-04.html docs/reference/04-rich-authoring-embeds-and-export.md

# Shell loop to export every chapter in docs/reference/ into /tmp/book-html/
mkdir -p /tmp/book-html
for md in docs/reference/*.md; do
  stem="$(basename "$md" .md)"
  autodoc --export html --theme dark --output "/tmp/book-html/${stem}.html" "$md"
done
```

### Best Practices for Companion Asset Organization

1. **One Visual Concept per Companion File**: Keep each `.mmd` diagram focused on a single topology, sequence flow, or state machine (typically 8–20 nodes), and give it a descriptive slug such as `embed-resolution-pipeline.mmd` rather than `fig1.mmd`.
2. **Use Standalone Paragraphs for Block Figures**: Always place `![Caption](asset.ext)` on its own line with a blank line above and below so the parser promotes it to a full-width `<figure>` with a numbered or styled `<figcaption>`.
3. **Prefer `.csv` Companion Files for Wide Tables**: Any reference matrix with more than 4 columns or 10 rows is easier to diff, sort, and edit as a companion `.csv` file than as an inline Markdown pipe table.
4. **Keep Vega-Lite Field Names Simple**: Use clean snake_case or camelCase field names (such as `latency_ms`, `rss_mb`, `artifact_type`) without embedded periods so field lookups in `golib/gui/vegalite` remain unambiguous.
5. **Commit Companion Folders Alongside Notes**: When staging changes in Git, always stage both `<note-stem>.md` and `<note-stem>/` together (`git add docs/reference/04-rich-authoring-embeds-and-export.md docs/reference/04-rich-authoring-embeds-and-export/`) so every commit remains self-contained and reproducible.

---

## 4.10 Lab Notes (`.lab.md`) and Cell Outputs

A **lab note** is a Markdown note named `*.lab.md`: an ordinary note to search, links and every renderer, whose fenced code cells a notebook plugin (autodoc-jupyter) runs in a Jupyter kernel. A cell's outputs are files in the note's companion folder, never written into the note:

```text
plan.lab.md
plan.lab/out/<cell key>/01.txt     text and tracebacks
plan.lab/out/<cell key>/02.png     an image
plan.lab/out/<cell key>/03.chart.json   a Vega-Lite chart (and .csv for a table)
```

The cell key is a hash of the fence's language and its code, so a cell's outputs belong to that exact code: editing a cell hides its old outputs until it runs again, and a fence of another language holding the same text never shows them. The HTML preview and export draw each cell's outputs right after its fence, in name order; `--gui`'s Rendered View draws them under the fence, off the cursor (text, every diagram and chart, tables and images). Opening an `.ipynb` hands it to the plugin, which writes the lab note beside it and opens that.

---

## 4.11 Previews in the Terminal (`File › Preview HTML`, `Preview Mermaid diagram`)

`File › Preview HTML` (in a terminal; the GUI opens [a pane](#the-html-preview-pane-file--preview-html) instead) renders the current Markdown editor buffer (including unsaved
edits) into an offline, light/dark themed cache file and opens it with the system browser. A
browser-launch error is reported in the TUI.

**Previews** (`File › Preview Mermaid diagram`, `Preview HTML`) are images in the terminal where
they can be: `View › Image previews` on, a terminal that confirms kitty's graphics protocol
(inside tmux, `set -g allow-passthrough on`), and a headless Chromium or Chrome, run offline.
The whole page (or the whole diagram) is rendered and **scrolls** in its dialog: the arrows,
`j` `k` `h` `l`, `Page Up`/`Page Down`, `[` `]`, `Home`/`End` and the mouse wheel. **Zoom in**
and **Zoom out** draw it again larger or smaller (50 % to 200 %, as a browser zooms), in the same
dialog. Elsewhere a diagram shows its source and HTML opens in the default browser, and the
preview says which was missing; turning `Image previews` off compares the two on the same file.
Both follow the active theme's colours (light, dark, sepia, retro, mono), as
`autodoc --export html --theme …` does.

---

[← Previous: Chapter 03: Modal Editor & Panels](./03-modal-editor-and-panels.md) | [Index](./README.md) | [Next: Chapter 05: Hybrid Search Pipeline →](./05-hybrid-search-pipeline.md)
