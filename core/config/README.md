# core/config

AutoDoc's configuration file, and the places it keeps things: the daemon's unix socket, the state
directory, and the store. `Load` reads the TOML file, fills defaults and rejects unknown settings.
Workspaces and embedding providers are not configured here; they are rows of the store.

```toml
[server]
socket = ""        # default: $XDG_RUNTIME_DIR/autodoc.sock
state_dir = ""     # default: $XDG_STATE_HOME/autodoc
data_dir = ""      # default: $XDG_DATA_HOME/autodoc (the store: autodoc.db)

[follow]
poll_interval = "2s"

[embedding_queue]
max_inflight = 1   # 1 or 2
```

| Name | What |
| --- | --- |
| `Config` | the whole file, with defaults filled in: `Server`, `Follow`, `EmbeddingQueue` |
| `Server` | `Socket`, `StateDir`, `DataDir`; `""` resolves to the default |
| `Follow` | `PollInterval`, the watch fallback's listing interval |
| `EmbeddingQueue` | `MaxInflight`, concurrent provider calls across every workspace (1 or 2) |
| `Duration` | a `time.Duration` written as a string (`"2s"`); `UnmarshalText` parses it |
| `Load(file)` | reads, defaults and validates the file; a missing file is every default |
| `DefaultPath()` | `$XDG_CONFIG_HOME/autodoc/config.toml`, else `~/.config/autodoc/config.toml` |
| `Server.SocketPath()` | the socket: `server.socket`, else `$XDG_RUNTIME_DIR`, `$TMPDIR` on Darwin, or the state directory; at most 100 bytes |
| `Server.StateDirPath()` | `server.state_dir`, else `$XDG_STATE_HOME/autodoc`, else `~/.local/state/autodoc` |
| `Server.StorePath()` | `autodoc.db` in `server.data_dir`, else `$XDG_DATA_HOME/autodoc`, else `~/.local/share/autodoc` |
| `DefaultPollInterval` | 2s |
| `SocketName`, `StoreName` | `autodoc.sock`, `autodoc.db` |
| `ErrInvalid` | a setting that cannot be used, or a file that does not parse or names an unknown setting (`errs.ErrInvalidArgument`) |

The path methods create nothing. A mode that writes to a directory makes it (`internal/endpoint`).

No mutable package state: any goroutine may call `Load`, `DefaultPath` and the path methods.
`Load` reads the file; `DefaultPath` and the path methods read the environment. A `Config` is not
changed by the package after `Load` returns, so concurrent reads are safe; a caller that writes a
field must serialize that write with every reader.
