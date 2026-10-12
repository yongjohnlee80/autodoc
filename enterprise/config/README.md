# enterprise/config

Reads and validates `server.toml`, the enterprise search server's configuration. Every field is
checked before the server opens anything; every refusal names its field.

```go
import "github.com/yongjohnlee80/autodoc/enterprise/config"

cfg, err := config.Load("server.toml")
if config.IsRefusal(err) {
	// exit 2: err names every field that is wrong
}
```

## API

- **Loading:** `Load(path) (*Server, error)`. A file that can't be read is an error; one that is
  wrong is a `*Refusal` with every refused field (`Refusal.Fields`, `Refusal.Errors`), all at once.
  `IsRefusal` tells the two apart; `Refuse(path, field, reason)` makes one for a check made after
  `Load`, such as a view's.
- **The configuration:** `Server` holds `Postgres` (`DSNEnv`, `Schema`, and `DSN()`, which reads the
  environment), `Embedding`, `HTTP` (nil when the file has none) with its `Client`s and their
  grants, the `Source`s, and `Warnings` (a token shorter than `MinTokenBytes`).
- **Defaults and bounds:** `DefaultSchema`, `DefaultListen`, `DefaultSlots`, `DefaultRefresh`,
  `DefaultInterval`, `DefaultLag`, `DefaultOpenAI`, `DefaultOllama`; `MaxSlots`, `MinInterval`,
  `MinTokenBytes`.

Every field, its default and its refusals: [docs/enterprise/server-config.md](../../docs/enterprise/server-config.md).

## Secrets

No secret is ever in the file: the DSN and the embedding key are read from environment variables
it names, and a client's token is kept as its SHA-256 or read from the environment. A refusal
names the variable, never its value, and a token pasted where its hash belongs is refused without
being echoed.

## Concurrency

`Load` may be called from any goroutine; a `Server` is a value it returns and never changes after.
`Postgres.DSN` reads the environment when called.
