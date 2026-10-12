# core/follow

Keeps a workspace's index following its root. Watch events, poll events and scans are hints naming
paths: the follower hands each path to a `Queue`, and the indexer behind it (the store's one
writer) re-reads the file and decides whether to delete, skip or re-index it. The follower never
writes the index. It watches the root's golib `vfs.FS` natively when the driver can, else polls it;
when neither can be set up it is degraded, scans the whole root every poll interval and retries the
setup with backoff.

| Name | What |
| --- | --- |
| `New(fsys, store, queue, opts...)` | a `*Follower` of `fsys` that queues paths for the indexer behind `store` and `queue` |
| `(*Follower).Run(ctx)` | follows the root until `ctx` ends: watch, then a full reconcile; a watch that ends is set up again |
| `(*Follower).Rescan()` | asks `Run` to reconcile the whole root; never blocks, and requests made before one is served are served once |
| `(*Follower).Status()` | a snapshot of how the follower is following |
| `Store` | what the follower reads of the index: `Version(ctx, path)` and `PathsUnder(ctx, dir)` |
| `Queue` | takes paths for the indexer: `Touch(path)`, which must not block for long |
| `Status` | `Following` (one of the modes below), `Err` (why it is degraded), `Retrying` (subtrees a scan could not read, rescanned with backoff) |
| `Starting`, `Watching`, `Polling`, `Degraded` | the modes: `"starting"`, `"watch"`, `"poll"`, `"degraded"` |
| `WithPollInterval(d)` | the poll's listing interval and the degraded scan's interval (default 2 s) |
| `WithBackoff(first, most)` | a retry's first delay (default 1 s) and its cap (default 1 min) |
| `WithMatch(match)` | whether a path belongs to the workspace; default all |
| `WithExcluded(excluded)` | whether a path lies in an excluded directory, never walked, watched or polled; default none |

`Status` and `Rescan` are safe to call from any goroutine, also while `Run` runs. Call `Run` once at
a time per `Follower`. `Store`, `Queue` and the `WithMatch` function are called from `Run`'s
goroutine only; the `vfs.FS` and the `WithExcluded` function are also called from `vfs.Poll`'s
goroutine, so they must be safe for concurrent use.
