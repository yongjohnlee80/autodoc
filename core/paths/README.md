# core/paths

The path helpers AutoDoc's core packages share, written once. Paths are slash-separated and
clean, relative to a workspace or absolute. An absolute path keeps its volume whole (a drive's
`C:/`, a share's `//server/share/`), and every operation on one goes through this package; a
relative path has no volume, and package path is enough for it. The functions follow this OS's
rule (Windows' where the separator is a backslash); inside, the rule is a parameter, so both are
tested on every OS.

| Function | Answers |
| --- | --- |
| `Under(p, dir)` | whether `p` is `dir` or inside it, by whole segments; every relative path is under `"."`, and every path on a volume under its root (`/`, `C:/`, `//server/share/`) |
| `Slash(p)` | the OS path `p` in the slash form: slash-separated and clean, its volume kept whole (`C:\` is `C:/`, `\\server\share\kb` is `//server/share/kb`; a verbatim `\\?\C:\x` is `C:/x`); `""` stays `""`. Where an OS path comes in (a disk's canonical path, a workspace's root, a caller's path), it is turned into this form once |
| `Clean(p)`, `Join(abs, elem...)`, `Dir(p)` | package path's, on an absolute path in the slash form with its volume kept: `Join("C:/", ".todo-list")` is `C:/.todo-list`, `Dir("//srv/sh/kb")` is `//srv/sh/` (package path alone would make `C:/` `C:` and `//srv/sh` `/srv/sh`) |
| `Root(p)`, `IsRoot(p)`, `Rel(dir, p)` | `p`'s volume root and the path inside it; whether `p` is a root; `p` relative to `dir` when under it, on the same volume |
| `IsAbs(p)` | whether `p`, in either form, is absolute: rooted (`/srv/kb`), or a drive's or share's on Windows (`C:/Users/u`, `C:\Users\u`, `//server/share/kb`; not `C:u`). The disk that opens it has the last word |
| `ExpandHome(p)` | `p` with a leading `~` or `~/` made the home directory (`ErrNoHome` when there is none); any other path as it is. Used by the config's paths and a workspace's root |

Functions with no disk I/O, safe for concurrent use (`ExpandHome` reads the home directory from
the environment). An SQL form of `Under` (a `dao.Predicate`)
lives with the queries that need it, in `core/index` and `core/pgstore`.
