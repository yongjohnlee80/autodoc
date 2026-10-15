# User themes: a reference for people and agents

AutoDoc reads user themes from `themes/` beside the selected `config.toml`. The default is
`$XDG_CONFIG_HOME/autodoc/themes/`, or `~/.config/autodoc/themes/`. `--config /path/config.toml`
selects `/path/themes/`. No daemon or window is needed to author or check a theme.

## Agent workflow

```sh
autodoc --theme-reference
autodoc --new-theme mytheme --from sepia
# Edit the printed file, then:
autodoc --check-theme /path/themes/mytheme.qml
```

Read this reference first. Preserve every required key, edit quoted colour values, and run the
checker until it exits zero. The checker prints every independent key/value problem with file,
line and column. A malformed QML file prints the parser's positioned syntax error. A valid result
names the file and the number of required colours. The starter never overwrites an existing file.

Theme names match `[a-z][a-z0-9_]*`. Built-in names (`dark`, `light`, `mono`, `retro`, `sepia`)
cannot be shadowed. Copy a built-in under a new name. Restart AutoDoc after adding a new filename
to register its module; edits to a registered theme reload live. User entries say `(user)` in
the Theme menu and Preferences; preferences store the plain name.

## Format and validation

A theme is one `Theme { ... }` with constant quoted string colours. Grouped properties and dotted
properties are equivalent QML spellings. No imports, handlers, property declarations or child
objects. Every key below is required exactly once; unknown keys and invalid colours are errors.
No omitted key silently inherits a colour.

Values use golib's colour vocabulary:

- `"#rrggbb"`: six hexadecimal digits, recommended for consistent terminal and GUI appearance.
- `"black"`, `"red"`, `"green"`, `"yellow"`, `"blue"`, `"magenta"`, `"cyan"`, `"white"`:
  terminal ANSI slots, plus `bright` before any name. `gray`/`grey` mean bright black.
- `"default"`: the terminal's own foreground/background, **not** the surrounding surface colour.

Prefer at least **5:1** contrast for readable text against its actual background, including syntax
colours against `document.base`. Check selected and inactive text as separate pairs. Cursor and
line numbers need to remain distinguishable. ANSI/default colours depend on the terminal palette;
their contrast cannot be guaranteed from the theme file alone. The checker validates the format,
not visual accessibility: inspect the page, menus, selection, dialogs, previews and status line.

## Required colours

| Key | Surface |
|---|---|
| `app.window` | Application chrome background |
| `app.windowText` | Application chrome text |
| `app.button` | Button background |
| `app.buttonText` | Button text |
| `app.highlight` | Focused control highlight background |
| `app.highlightedText` | Focused highlighted control text |
| `app.base` | Default content background |
| `app.text` | Default content text |
| `app.inactive.highlight` | Unfocused highlight background |
| `app.inactive.highlightedText` | Unfocused highlighted text |
| `app.mid` | Muted borders and separators |
| `app.light` | Emphasized borders and separators |
| `app.backdrop` | Area around the centred document page |
| `menu.window` | Menu background |
| `menu.windowText` | Menu text |
| `menu.highlight` | Selected menu entry background |
| `menu.highlightedText` | Selected menu entry text |
| `menu.accent` | Menu/dialog access keys |
| `document.window` | Document frame background |
| `document.windowText` | Document frame text |
| `document.highlight` | Selected document row background |
| `document.highlightedText` | Selected document row text |
| `document.base` | Editor and document preview background |
| `document.text` | Editor and document preview text |
| `document.selection` | Editor text-selection background |
| `document.selectedText` | Editor selected text |
| `document.cursor` | Editor and page-following terminal cursor |
| `document.lineNumber` | Editor line numbers |
| `status.window` | Status-line background |
| `status.windowText` | Status-line text |
| `syntax.keyword` | Keywords |
| `syntax.controlFlow` | Control-flow keywords |
| `syntax.dataType` | Type names |
| `syntax.attribute` | Attributes |
| `syntax.function` | Function names |
| `syntax.string` | Strings |
| `syntax.specialChar` | Escapes and special characters |
| `syntax.decVal` | Decimal integers |
| `syntax.float` | Floating-point numbers |
| `syntax.baseN` | Nondecimal numbers |
| `syntax.constant` | Constants |
| `syntax.comment` | Comments |
| `syntax.alert` | Alerts and diagnostic notices |
| `syntax.import` | Imports |
| `syntax.operator` | Operators |

## Complete example

This warm-paper example is based on golib's sepia theme. Keep document surfaces together so
syntax colours work in both the page and its previews; choose chrome colours against the chrome.

```qml
Theme {
    app {
        window: "#dacca9"; windowText: "#4a3826"
        button: "#cfbf9b"; buttonText: "#4a3826"
        highlight: "#8a5a2b"; highlightedText: "#fbf6ea"
        base: "#e6d9b9"; text: "#5b4636"
        inactive { highlight: "#cfbd96"; highlightedText: "#8a5a2b" }
        mid: "#b5a380"; light: "#8a5a2b"
        backdrop: "#d4c4a0" // screen outside the page
    }
    menu {
        window: "#d9caa6"; windowText: "#4a3826"
        highlight: "#8a5a2b"; highlightedText: "#fbf6ea"
        accent: "#9b3d1f"
    }
    document {
        window: "#e6d9b9"; windowText: "#8c7656"
        highlight: "#8a5a2b"; highlightedText: "#fbf6ea"
        base: "#e6d9b9"; text: "#5b4636"
        selection: "#d6b97a"; selectedText: "#3b2a1a"
        cursor: "#b5541f"; lineNumber: "#a8946c"
    }
    status { window: "#d9caa6"; windowText: "#5b4636" }
    syntax {
        keyword: "#7c3f12"; controlFlow: "#7c3f12"
        dataType: "#4f6b2f"; attribute: "#7a4b6b"; function: "#6b5200"
        string: "#8f3b2a"; specialChar: "#a8461f"
        decVal: "#2f5e6b"; float: "#2f5e6b"; baseN: "#2f5e6b"; constant: "#2f5e6b"
        comment: "#8a7350"; alert: "#b0301e"
        import: "#4f6b2f"; operator: "#6b5a44"
    }
}
```

## Loading, failure and export

Invalid files are diagnosed and cannot be selected. If an active file becomes invalid or is
deleted, AutoDoc falls back to its default theme (dark in a terminal, sepia in the GUI), preserving
the document, cursor and panes. It retains the chosen preference so a repaired file can recover.
The page-following terminals and plugins receive the custom palette too.

HTML export and preview use a **built-in style**, not every custom role. For custom themes using
RGB document background/text, AutoDoc selects the nearest built-in RGB palette by the sum of
squared RGB distances for those two colours. Ties keep the built-in name order. A document
background/text using ANSI slots or `default` maps to `mono`. Built-in theme mappings are unchanged.
Command-line export accepts the user name with the same `--config` path:

```sh
autodoc --config /path/config.toml --theme mytheme --output page.html --export html page.md
```
