# core/edition

What this build of AutoDoc offers beyond its core: the one place a Community build and a Pro build
differ. Turning a feature off changes no schema and no protocol.

| Name | What |
| --- | --- |
| `Databases` | constant: whether the build offers a workspace's source database (its `.view` files) and its Postgres destination. When false, the daemon refuses those settings, `sys.capabilities` says so, and the TUI hides them |
| `ErrDatabases` | the error for a database setting refused by a build without `Databases`; wraps `errs.ErrUnsupported` |

`Databases` is `true` until the Community and Pro split is decided.

A constant and an error value that nothing reassigns: safe to read from any goroutine.
