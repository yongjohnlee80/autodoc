# core/paths

The path helpers AutoDoc's core packages share, written once. Paths are slash-separated and
clean, relative to a workspace or absolute.

| Function | Answers |
| --- | --- |
| `Under(p, dir)` | whether `p` is `dir` or inside it, by whole segments; every relative path is under `"."` |
| `ExpandHome(p)` | `p` with a leading `~` or `~/` made the home directory (`ErrNoHome` when there is none); any other path as it is. Used by the config's paths and a workspace's root |

Functions with no disk I/O, safe for concurrent use (`ExpandHome` reads the home directory from
the environment). An SQL form of `Under` (a `dao.Predicate`)
lives with the queries that need it, in `core/index` and `core/pgstore`.
