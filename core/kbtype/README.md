# core/kbtype

A KB type, read and planned (ADR 1792349867). A KB type is a git repo with a `kbtype.toml` (what the
type is, its folders and their purposes, the workspace settings it proposes) and a `tree/` (the KB
as it is created). Nothing in a type runs: its text files are read and copied, nothing else.

| Name | What |
| --- | --- |
| `Read(dir)` | parses a staged clone's `kbtype.toml` and reads its `tree/` within the limits, then validates both; an error wraps `ErrInvalid` and names the rule broken |
| `Type` | `Name`, `Title`, `Description`, `Workspace` (`Settings`), `Folders`, and `Files` (tree/'s files by slash path) |
| `Settings` | the proposed `Include`, `EmbeddingPolicy` and `Schema` (a file of `tree/`) |
| `Folder` | a folder's `Path`, `Purpose`, and whether it is `Immutable` (the user's untouched material) |
| `Type.Plan(v, operations)` | every `Write` the type makes, in tree order with each folder's descriptor first, then AutoDoc's own `KB_OPERATIONS.md` (`operations`), marked `Managed` |
| `Write` | `Rel`, the rendered `Text`, `Managed`, and `Immutable` (the folder whose descriptor it is) |
| `Vars` | `{{autodoc.date}}`, `{{autodoc.version}}`, `{{kb_type.name}}`, `{{kb_type.url}}`, `{{kb_type.commit}}`; a template's Obsidian variables (`{{date}}`, `{{title}}`) pass through |
| `Descriptor(d)` | folder `d`'s descriptor: `_ABOUT.md` in a `_` folder, `ABOUT.md` elsewhere |
| `DeclaredVersion(text)`, `Older(have, want)` | the managed file's version rule: replaced only when its version parses and is older |
| `MaxManifestSize`, `MaxFileSize`, `MaxTreeSize`, `MaxFiles`, `MaxEntries` | the limits a type is read within |

The rules `Read` enforces:

- The folder list is the tree. Every folder listed has its descriptor in `tree/`, and every folder under `tree/` is listed.
- The root has `AGENTS.md` and `ABOUT.md`.
- `tree/` never holds `KB_OPERATIONS.md`: it is AutoDoc's.
- An immutable folder holds only its descriptor.
- Every entry is a regular file or a folder, with a clean relative name. A symlink is refused.
- Every file is UTF-8 text, within the limits. One byte over a limit is refused, never read as a valid prefix.
