# Chapter 4: Rich Authoring, Embeds, and Export

Technical architecture documents, performance postmortems, and engineering specifications require more than plain paragraphs. They rely on system topology diagrams, sequence flows, state machines, latency benchmarks, and structured comparison tables.

Yet embedding rich visuals in Markdown traditionally forces an unpleasant trade-off: either litter your prose with sprawling 150-line JSON/Mermaid blocks that obscure the text, or depend on external cloud charting services that break when offline. AutoDoc solves both problems through its **companion `<note-stem>/` resource directory convention** (`core/export/embed.go`), native GPU vector rendering in `autodoc --gui` (`golib/gui/mermaid` and `golib/gui/vegalite`), and single-file air-gapped HTML export (`autodoc --export`).

---

## 4.1 The Companion `<note-stem>/` Directory Convention

In AutoDoc, every Markdown document `<note-stem>.md` can have an optional companion resource directory alongside it named `<note-stem>/`. When you embed a diagram, chart, CSV table, or image using a relative path inside `<note-stem>.md`, AutoDoc's embed resolver (`core/export/embed.go`) automatically searches two locations in strict order:

1. **Beside the Note**: `<note-dir>/<target-path>`
2. **Inside the Companion Directory**: `<note-dir>/<note-stem>/<target-path>`

For example, for the chapter file `docs/tutorials/04-rich-authoring-embeds-and-export.md`, its companion directory is `docs/tutorials/04-rich-authoring-embeds-and-export/`, holding:

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

To distinguish rich block figures (diagrams, charts, and tables) from inline badge icons inside a sentence, AutoDoc enforces the **Standalone Paragraph Block-Embed Rule**:

- A `.mmd`, `.mermaid`, `.chart.json`, `.vl.json`, or `.csv` reference is promoted to a rendered `<figure>` block **only when `![Caption](path)` is the sole node inside its Markdown paragraph**—meaning it is preceded and followed by a blank line.
- The image alt text (`Caption`) becomes the rendered figure's `<figcaption>` title below the diagram, chart, or table.
- If a `.mmd`, `.chart.json`, or `.csv` link appears inline next to other text on the same line, it is left untouched rather than breaking paragraph flow.

---

## 4.3 Native Mermaid Diagrams (`.mmd` and `.mermaid`)

AutoDoc parses and renders Mermaid diagrams natively in Go via `golib/parse/mermaid` and `golib/gui/mermaid`—requiring zero Node.js, Puppeteer, or browser dependencies. You store each diagram in your note's companion directory (`<note-stem>/diagram.mmd`) and embed it on a standalone line with `![Diagram Caption](diagram.mmd)`.

### Supported Diagram Types and Compatibility Rules

AutoDoc's native Mermaid engine supports three primary diagram families:

1. **Flowcharts (`flowchart LR`, `flowchart TD`, `flowchart TB`)**: Directed graphs with rectangular, rounded, diamond, and cylinder nodes, labeled edges (`-->|"Yes"|`), and nested `subgraph` containers.
2. **Sequence Diagrams (`sequenceDiagram`)**: Participant lifelines, synchronous/asynchronous arrows (`->>`, `-->>`), `alt`/`else`/`loop` blocks, and activation bars.
3. **State Diagrams (`stateDiagram-v2`)**: Initial/final states (`[*]`), clean state aliases (`state "Display Name" as ID`), and labeled state transitions.

### Strict Compatibility Guidelines for `golib/parse/mermaid`

To ensure 100% deterministic parsing and layout across both `autodoc --gui` and `autodoc --export`, follow these syntax conventions in all `.mmd` files:

- **Always Double-Quote Complex Flowchart Labels**: Whenever a node label contains spaces, parentheses, colons, or slashes, enclose the label text in double quotes:
  - Valid: `A["Markdown Files (.md)"] --> B["SQLite Index (WAL)"]`
  - Invalid: `A[Markdown Files (.md)] --> B[SQLite Index: WAL]`
- **No HTML Tags in Labels**: Never use `<br/>`, `<b>`, or `<i>` inside node or edge labels; use concise phrases or split concepts into sequential nodes instead.
- **Clean State IDs in `stateDiagram-v2`**: Define human-readable state names using `state "Rendered View (Ctrl+P)" as RenderedView` and reference the clean identifier (`Normal --> RenderedView: Ctrl+p`) in transitions.

---

## 4.4 Native Vega-Lite Charts (`.chart.json` and `.vl.json`)

For quantitative benchmarks, latency distributions, and time-series metrics, AutoDoc embeds a native Vega-Lite specification parser and vector chart renderer (`golib/gui/vegalite`).

### Supported Vega-Lite Grammar Subset

AutoDoc's Vega-Lite engine renders single-view declarative JSON specifications with deterministic layout and an automatic **Tableau-10** categorical color palette:

- **Allowed Top-Level Keys**: `"$schema"`, `"title"`, `"description"`, `"width"`, `"height"`, `"data": {"values": [...]}`, `"mark"`, and `"encoding"`.
- **Supported Marks (`"mark"`)**:
  - `"bar"` (or `{"type": "bar"}`) for vertical, horizontal, stacked (`"stack": "zero"`), normalized (`"stack": "normalize"`), or grouped (`"stack": null`) bar charts.
  - `"line"` (or `{"type": "line", "point": true}`) for multi-series trend lines with optional vertex markers.
  - `"area"` for cumulative stacked or overlapping area charts.
  - `"point"` and `"circle"` for 2D scatter plots.
- **Supported Encoding Channels (`"encoding"`)**:
  - Allowed channels: `"x"`, `"y"`, and optional `"color"` (multi-view composition keys such as `"layer"`, `"hconcat"`, `"vconcat"`, `"xOffset"`, `"transform"`, or `"size"` are intentionally omitted to keep rendering fast and predictable).
  - Channel properties: `"field"` (use clean identifiers without dots), `"type"` (`"quantitative"`, `"nominal"`, `"ordinal"`, `"temporal"`), `"title"`, `"stack"` (`"zero"`, `"normalize"`, or `null`), `"sort"` (`"ascending"`, `"descending"`, or `null`), and `"axis"` (`{"labelAngle": 0}`).

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

To guarantee fast rendering and prevent malicious content injection, `resolveImage` in `core/export/embed.go` verifies every image's magic bytes with `http.DetectContentType` (and validates `<svg` XML structure for `.svg` assets) rather than trusting the file extension alone. Each individual image is bounded at **6 MiB**, with a cumulative per-document budget of **32 MiB** across all embedded images on a page.

---

## 4.7 Interactive Rendered View (`Ctrl+p`) and Click-to-Edit in `--gui`

While authoring in `autodoc --gui`, pressing **`Ctrl+p`** toggles the active editor pane into **Rendered View** (or opens Rendered View in a side-by-side split if you run `:vsplit` first).

Rendered View in `--gui` is not an embedded webview: every heading, code block, callout box, `.mmd` diagram, `.chart.json` chart, and `.csv` table is drawn directly onto Gio's GPU vector canvas. Furthermore, Rendered View is **bidirectionally interactive**:

1. **Hover Inspection**: Hovering over a rendered Mermaid diagram, Vega-Lite chart, or CSV table highlights its figure frame and displays the backing companion file path (`04-rich-authoring-embeds-and-export/embed-resolution-pipeline.mmd`).
2. **Click-to-Edit Backing Source**: Clicking any rendered `.mmd`, `.chart.json`, or `.csv` figure opens an inline modal source editor bound directly to that companion file on disk.
3. **Instant Live Re-Render**: When you modify a node label in `.mmd`, tweak a data value in `.chart.json`, or add a row to `.csv` and press `Ctrl+s` (or `Esc`), AutoDoc atomically saves the companion file and re-renders the vector graphic in place within milliseconds.

---

## 4.8 Self-Contained Offline HTML Export (`autodoc --export`)

When you need to share an ADR, technical specification, or review report with colleagues who do not have AutoDoc installed, you can compile any Markdown document—or an entire workspace book—into a **100% self-contained, single-file HTML document** that works completely offline:

```bash
# Export a single note (including all companion .mmd, .chart.json, .csv, and images)
autodoc --export docs/tutorials/04-rich-authoring-embeds-and-export.md -o /tmp/chapter-04.html

# Or press 'w' while viewing any document in Rendered View (Ctrl+p)
```

During export (`core/export`), AutoDoc resolves every companion `.mmd` diagram, `.chart.json` chart, `.csv` table, and image, renders diagrams and charts into crisp inline vector output, encodes raster images as `data:<mime>;base64,...` URIs, and bundles print-ready responsive CSS with light/dark theme support. The resulting `.html` file has **zero external network requests**, making it safe to archive in compliance artifacts, attach to pull requests, or view inside air-gapped environments.

---

## 4.9 Authoring Checklist and Diagnostic Validation

When preparing a technical specification, architecture blueprint, or chapter with rich companion assets, you can verify that every embedded `.mmd`, `.chart.json`, `.csv`, and image resolves cleanly and passes grammar validation before committing to Git.

### Headless Embed and Export Verification

Running `autodoc --export` in `--check` (or dry-run) mode parses the Markdown AST, resolves every companion file through `core/export/embed.go`, validates all Mermaid ASTs (`golib/parse/mermaid`) and Vega-Lite specifications (`golib/gui/vegalite`), checks CSV dimension bounds, and reports any broken references or syntax errors with exact line numbers:

```bash
# Verify a single document and all of its companion embeds
autodoc --export --check docs/tutorials/04-rich-authoring-embeds-and-export.md

# Batch-export an entire directory of chapters into a standalone HTML bundle
autodoc --export docs/tutorials/ --out-dir dist/book-html/
```

### Best Practices for Companion Asset Organization

1. **One Visual Concept per Companion File**: Keep each `.mmd` diagram focused on a single topology, sequence flow, or state machine (typically 8–20 nodes), and give it a descriptive slug such as `embed-resolution-pipeline.mmd` rather than `fig1.mmd`.
2. **Use Standalone Paragraphs for Block Figures**: Always place `![Caption](asset.ext)` on its own line with a blank line above and below so the parser promotes it to a full-width `<figure>` with a numbered or styled `<figcaption>`.
3. **Prefer `.csv` Companion Files for Wide Tables**: Any reference matrix with more than 4 columns or 10 rows is easier to diff, sort, and edit as a companion `.csv` file than as an inline Markdown pipe table.
4. **Keep Vega-Lite Field Names Simple**: Use clean snake_case or camelCase field names (such as `latency_ms`, `rss_mb`, `artifact_type`) without embedded periods so field lookups in `golib/gui/vegalite` remain unambiguous.
5. **Commit Companion Folders Alongside Notes**: When staging changes in Git, always stage both `<note-stem>.md` and `<note-stem>/` together (`git add docs/tutorials/04-rich-authoring-embeds-and-export.md docs/tutorials/04-rich-authoring-embeds-and-export/`) so every commit remains self-contained and reproducible.
