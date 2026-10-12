# core/export

Renders a Markdown document as a standalone HTML page or as plain text, and draws one Mermaid
diagram as a page of its own, for an export and for a preview. A page's colours are a shipped
theme's, its Content-Security-Policy allows no network access, and a ```` ```mermaid ```` block is
drawn in the page by the vendored mermaid.js (`core/export/mermaid`), admitted by its digest. A
page without a diagram has no script.

| Name | What |
| --- | --- |
| `Format` | an output format: `HTML` (`"html"`) or `Text` (`"text"`) |
| `Options` | what a render reads besides the source: `Base`, the directory relative links are resolved from (each becomes a `file:` URL; `""` keeps links as written), and `SourceSpans`, which puts each block's source byte range on it (`data-src`) |
| `Render(source, format, theme)` | `RenderWith` with no options |
| `RenderWith(source, format, theme, options)` | the page or text; an unknown theme (for HTML) or format is an error |
| `DiagramPage(source, theme)` | a page of one Mermaid diagram at its own size on the theme's background |
| `Themes()` | the theme names, sorted: `dark`, `light`, `mono`, `retro`, `sepia` |
| `ThemeOf(tuiTheme)` | the export theme for a TUI theme: its own where there is one, else `dark` |
| `Background(theme)` | a theme's page colour; `dark`'s for an unknown theme |

HTML shows an image as its alt text. Text drops frontmatter, link reference definitions and raw
HTML, keeps code blocks as written, and ignores the theme.

Every function is safe for concurrent use: shared state is read-only or built once under a
`sync.Once`. No disk reads; `RenderWith` reads the working directory when `Options.Base` is
relative.
