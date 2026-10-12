# core/workspace

Opens a workspace's root: its files as a `vfs.FS` and the `Matcher` of its include and exclude
patterns. The workspace itself is a row of the store (`core/store`). A pattern is a root-relative
glob: segments split on `/`, each a `path.Match` pattern or `**` for any number of whole segments,
with bounded, non-nested brace alternatives (`*.{md,txt}`, at most 64 expansions).

| Name | What |
| --- | --- |
| `Spec` | a workspace as it is added: `Name`, `Root`, `Include`, `Exclude` |
| `Normalize(s)` | checks a `Spec` and fills its defaults: a name with no path separator, a root absolute after `~/` is expanded, valid patterns (nil means the defaults) |
| `DefaultInclude()` | `**/*.md`, `**/*.txt`, `**/*.yaml`, `**/*.yml`; a fresh slice each call |
| `DefaultExclude()` | `.git/**`, `**/node_modules/**`; a fresh slice each call |
| `ErrInvalidSpec` | a `Spec` that cannot be added (`errs.ErrInvalidArgument`) |
| `Workspace` | one open root: `Name`, `Root`, `FS`, `Matcher` |
| `Open(spec)` | opens the root with golib's local driver |
| `Workspace.Close()` | closes the root |
| `ErrNotADirectory` | a root that is gone or is not a directory (`errs.ErrInvalidArgument`); a root it may not open is a permission error instead |
| `Matcher`, `NewMatcher(include, exclude)` | compiled patterns; a pattern `ExpandPattern` refuses is skipped, so check them with `ValidPattern` first |
| `Matcher.Match(p)` | whether `p` matches an include pattern and no exclude pattern |
| `Matcher.Excluded(p)` | whether `p`, or a directory above it, matches an exclude pattern |
| `ValidPattern(p)` | nil for a valid pattern, else an error saying why |
| `ExpandPattern(p)` | `p` with its brace alternatives expanded |

A pattern ending in `/**` also matches the directory itself.

`ExpandPattern`, `ValidPattern`, the defaults and `Normalize` are functions of their arguments,
safe for concurrent use (`Normalize` reads the home directory from the environment). `Open`
reads the disk. A `Matcher` is read-only after `NewMatcher` and safe for concurrent use. An open
`Workspace`'s `FS` is safe for concurrent use and its `Close` may be called more than once; a
caller that writes a `Workspace`'s fields must serialize that with every reader.
