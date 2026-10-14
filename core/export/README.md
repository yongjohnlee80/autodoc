# core/export

Renders a Markdown document as a standalone HTML page or as plain text, and draws one Mermaid
diagram as a page of its own, for an export and for a preview. A page's colours are a shipped
theme's, its Content-Security-Policy allows no network access, and a ```` ```mermaid ```` block is
drawn in the page by the vendored mermaid.js (`core/export/mermaid`), admitted by its digest. A
page without a diagram has no script.

| Name | What |
| --- | --- |
| `Format` | an output format: `HTML` (`"html"`) or `Text` (`"text"`) |
| `Options` | what a render reads besides the source: `Base`, the directory relative links are resolved from (each becomes a `file:` URL; `""` keeps links as written); `SourceSpans`, which puts each block's source byte range on it (`data-src`); `Document`, the source's file, whose embeds are drawn (below); and `TableRows` and `TableWidth`, which fit its tables to a narrower view |
| `Render(source, format, theme)` | `RenderWith` with no options |
| `RenderWith(source, format, theme, options)` | the page or text; an unknown theme (for HTML) or format is an error |
| `DiagramPage(source, theme)` | a page of one Mermaid diagram at its own size on the theme's background |
| `Themes()` | the theme names, sorted: `dark`, `light`, `mono`, `retro`, `sepia` |
| `ThemeOf(tuiTheme)` | the export theme for a TUI theme: its own where there is one, else `dark` |
| `Background(theme)` | a theme's page colour; `dark`'s for an unknown theme |

HTML shows an image as its alt text, unless `Options.Document` names the source's file. Then an
embed, `![alt](path)`, of a file beside the document is drawn, as xmd draws it:
- a Mermaid diagram (`.mmd`) as a diagram;
- a table (`.csv`) as a table, with its first row as the header. It shows 1000 rows, 64 columns
  and 20,000 cells at most, then says how many rows and columns are not shown. `Options.TableRows`
  and `Options.TableWidth` fit it to a narrower view: that many rows, and the first columns that fit
  in that many characters (one at least);
- an image (`.png`, `.jpg`, `.gif`, `.svg`, `.webp`, `.avif`, `.bmp`) as the image, written in as a
  `data:` URI, so the page stays whole.

The path is looked for beside the document, then in its resources folder (its name without `.x.md`
or `.md`: `plan.x.md`'s is `plan/`), and nowhere else. A path out of the document's folder, an
absolute path, a URL and a symlink pointing out are not followed.

A diagram or a table is drawn when it is its paragraph's only content. A file that is missing, too
large (1 MiB for a diagram or table, 6 MiB for an image, 32 MiB of images per page) or of another
kind is shown as its alt text.

Text drops frontmatter, link reference definitions and raw
HTML, keeps code blocks as written, and ignores the theme.

Every function is safe for concurrent use: shared state is read-only or built once under a
`sync.Once`. The only disk reads are `Options.Document`'s embeds, inside its folder; `RenderWith`
reads the working directory when `Options.Base` or `Options.Document` is relative.
